package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Shared append/ticket validation for the branching endpoints.
// ---------------------------------------------------------------------------

// validatedAppendTicket groups a live append-scope ticket with the requester's
// role, mirroring the validation path used by /api/append and /api/upload.
type validatedAppendTicket struct {
	t    *Ticket
	role string
}

// validateAppendTicket enforces session validity, the ticket's existence +
// append scope + unexpired status, the role's write eligibility and the FIR
// case-assignment. Identical semantics to the append/upload handlers.
func (a *app) validateAppendTicket(ctx context.Context, sessionID, ticketID string) (*validatedAppendTicket, error) {
	if !a.validSession(sessionID) {
		return nil, statusError{Code: http.StatusUnauthorized, Msg: "invalid or expired session"}
	}
	t, err := a.st.getTicket(ctx, ticketID)
	if err != nil {
		return nil, statusError{Code: http.StatusForbidden, Msg: "ticket not found or consumed"}
	}
	if t.Scope != TicketScopeAppend && t.Scope != TicketScopeAll {
		return nil, statusError{Code: http.StatusForbidden, Msg: "ticket lacks append scope"}
	}
	if time.Now().UTC().After(t.ExpiresAt) {
		return nil, statusError{Code: http.StatusForbidden, Msg: "ticket expired"}
	}
	role, err := a.st.userRole(ctx, t.UserID)
	if err != nil {
		return nil, statusError{Code: http.StatusInternalServerError, Msg: "internal error"}
	}
	if !canUploadEvidence(role) {
		// Branching writes content — SYSTEM_ADMIN / SHO_SUPERVISOR cannot.
		return nil, statusError{Code: http.StatusForbidden, Msg: "role may not write evidence versions"}
	}
	if assigned, _, err := a.st.validateAssignment(ctx, t.UserID, role, t.UserID, t.FirNumber); err != nil || !assigned {
		return nil, statusError{Code: http.StatusForbidden, Msg: "no case assignment for this fir"}
	}
	return &validatedAppendTicket{t: t, role: role}, nil
}

// statusError carries an HTTP status for the error writer.
type statusError struct {
	Code int
	Msg  string
}

func (e statusError) Error() string { return e.Msg }

// ---------------------------------------------------------------------------
// POST /api/versions/branch
// ---------------------------------------------------------------------------

// branchForm is the parsed multipart body of a branch request: a text
// annotation OR an evidence file, chained to a source version.
type branchForm struct {
	SessionID      string
	TicketID       string
	FirNumber      string
	SourceVersionID string
	DocumentID     string // optional; derived from source when empty
	Annotation     string
	// File fields (empty when this is a text-annotation branch).
	OriginalFilename string
	TempPath         string
	SizeBytes        int64
}

type branchResponse struct {
	Version *DocumentVersion `json:"version"`
	Consumed bool            `json:"consumed"`
	ScanStatus string        `json:"scan_status"`
	ParentHash string        `json:"parent_sha256_hash"`
}

// handleBranch creates a new document_versions row branching off an existing
// version WITHOUT linearly superseding it. The new row's tree_path extends the
// source version's path (source "v2" -> "v2.1", "v2.2", ...), its
// parent_sha256_hash chains cryptographically to the SOURCE version's hash, and
// the row is signed by the branching officer. The single-use append ticket is
// consumed only after the insert commits.
func (a *app) handleBranch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Cap the request body a little above the file cap (mirrors /api/upload).
	maxBytes := a.stg.maxBytes()
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+int64(64*1024))

	f, err := a.parseBranchMultipart(r)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if f.TempPath != "" {
		defer os.Remove(f.TempPath)
	}

	ctx := r.Context()
	vat, err := a.validateAppendTicket(ctx, f.SessionID, f.TicketID)
	if err != nil {
		writeStatusError(w, err)
		return
	}
	t, role := vat.t, vat.role

	if f.FirNumber != t.FirNumber {
		http.Error(w, "fir mismatch with ticket", http.StatusForbidden)
		return
	}
	if f.SourceVersionID == "" {
		http.Error(w, "source_version_id is required", http.StatusBadRequest)
		return
	}

	// Load the source version being branched FROM. It must live in the same FIR
	// as the ticket (RLS also limits visibility).
	source, sourceFir, err := a.st.versionByID(ctx, t.UserID, role, f.SourceVersionID)
	if err != nil {
		http.Error(w, "source version not found", http.StatusNotFound)
		return
	}
	if sourceFir != t.FirNumber {
		http.Error(w, "source version does not match ticket fir", http.StatusForbidden)
		return
	}

	// Determine the next available branch index by querying existing children of
	// the source's tree_path (never guessed or hardcoded).
	childPath, err := a.st.nextBranchTreePath(ctx, t.UserID, role, source.DocumentID, source.TreePath)
	if err != nil {
		log.Printf("branch tree path error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Build the content payload: either a text annotation or a stored evidence
	// file (reusing the exact upload validation/storage path).
	payload, scanStatus, mime, rollbackKey, err := a.buildBranchPayload(ctx, t, role, f)
	if err != nil {
		if errors.Is(err, errFileTypeNotAllowed) {
			http.Error(w, err.Error(), http.StatusUnsupportedMediaType)
			return
		}
		if errors.Is(err, errStorageNotConfigured) {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		if strings.Contains(err.Error(), "malicious content detected") {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if rollbackKey != "" {
		defer a.stg.deleteObject(ctx, rollbackKey)
	}

	// Next globally-unique version_number for this document (branches don't
	// advance the mainline counter).
	versionNumber, err := a.st.nextVersionNumber(ctx, t.UserID, role, source.DocumentID)
	if err != nil {
		log.Printf("branch version number error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Hash chain: sha256 = SHA256(content_hash || parent_hash || badge || ts).
	// parent_sha256_hash is cryptographically bound to the SOURCE version.
	now := time.Now().UTC()
	contentHash := hashChain(payload)
	parentHash := source.Sha256Hash
	badge := t.BadgeNumber
	createdAt := now.Format(time.RFC3339)
	sha := hashChain([]byte(contentHash + parentHash + badge + createdAt))
	sig, err := a.cry.sign([]byte(sha))
	if err != nil {
		log.Printf("branch sign error: %v", err)
		http.Error(w, "version signing failed", http.StatusInternalServerError)
		return
	}

	v := &DocumentVersion{
		DocumentID:       source.DocumentID,
		VersionNumber:    versionNumber,
		TreePath:         childPath,
		Sha256Hash:       sha,
		ParentSha256Hash: parentHash,
		Signature:        base64.StdEncoding.EncodeToString(sig),
		ContentPayload:   payload,
		CreatedBy:        t.UserID,
		CreatedAt:        createdAt,
		Kind:             "branch",
	}
	if err := a.st.insertDocumentVersionBranch(ctx, t.UserID, role, v); err != nil {
		log.Printf("branch version write error: %v", err)
		http.Error(w, "version write failed", http.StatusInternalServerError)
		return
	}

	// Hash-chained audit record for the VERSION_BRANCHED action.
	auditMeta := map[string]any{
		"document_id":       source.DocumentID,
		"source_version_id": source.ID,
		"branch_tree_path":  childPath,
		"source_sha256":     source.Sha256Hash,
		"version_id":        v.ID,
		"scan_status":       scanStatus,
		"content_type":      mime,
	}
	if err := a.st.insertAuditLog(ctx, t.UserID, role, "VERSION_BRANCHED", t.FirNumber, auditMeta); err != nil {
		log.Printf("branch audit error: %v", err)
	}

	// Single-use append ticket consumed only after the row commits.
	consumed, err := a.st.consumeTicket(ctx, f.TicketID)
	if err != nil {
		log.Printf("branch consume ticket error: %v", err)
	}

	writeJSON(w, http.StatusCreated, branchResponse{
		Version:    v,
		Consumed:   consumed,
		ScanStatus: scanStatus,
		ParentHash: parentHash,
	})
}

// buildBranchPayload returns the content_payload bytes for a branch: either the
// raw UTF-8 text annotation or the JSON metadata for an uploaded evidence file.
// When the file variant is used, the shared ingestEvidenceFile helper performs
// the magic-byte validation + malware policy + storage upload. rollbackKey is
// non-empty when a file object was written to storage (caller deletes on error).
func (a *app) buildBranchPayload(ctx context.Context, t *Ticket, role string, f *branchForm) (payload []byte, scanStatus, mime, rollbackKey string, err error) {
	if f.TempPath != "" {
		ingested, scan, err := a.ingestEvidenceFile(ctx, f.TempPath, f.OriginalFilename, f.SizeBytes, t.FirNumber)
		if err != nil {
			return nil, scan, "", "", err
		}
		metaJSON, err := json.Marshal(ingested.Meta)
		if err != nil {
			a.stg.deleteObject(ctx, ingested.StorageKey)
			return nil, scan, "", ingested.StorageKey, err
		}
		return metaJSON, scan, ingested.Mime, ingested.StorageKey, nil
	}
	annotation := strings.TrimSpace(f.Annotation)
	if annotation == "" {
		return nil, "", "", "", errors.New("an annotation or a file is required to branch")
	}
	return []byte(annotation), "text_annotation", "text/plain", "", nil
}

// parseBranchMultipart parses a branch request: it accepts a text `annotation`
// field OR a `file` part (streamed to a temp file). Fields parallel upload.
func (a *app) parseBranchMultipart(r *http.Request) (*branchForm, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, errors.New("multipart body required")
	}
	f := &branchForm{}
	seenFile := false

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch part.FormName() {
		case "file":
			if seenFile {
				part.Close()
				continue
			}
			seenFile = true
			file, err := os.CreateTemp("", "qdesk-branch-*")
			if err != nil {
				part.Close()
				return nil, errors.New("internal error")
			}
			f.TempPath = file.Name()
			f.OriginalFilename = part.FileName()
			n, err := io.Copy(file, part)
			file.Close()
			f.SizeBytes = n
			if err != nil {
				os.Remove(f.TempPath)
				f.TempPath = ""
				return nil, err
			}
		default:
			v, err := io.ReadAll(part)
			part.Close()
			if err != nil {
				return nil, err
			}
			set := strings.TrimSpace(string(v))
			switch part.FormName() {
			case "session_id":
				f.SessionID = set
			case "ticket_id":
				f.TicketID = set
			case "fir_number":
				f.FirNumber = set
			case "source_version_id":
				f.SourceVersionID = set
			case "document_id":
				f.DocumentID = set
			case "annotation":
				f.Annotation = set
			}
		}
	}
	return f, nil
}

// ---------------------------------------------------------------------------
// POST /api/versions/merge
// ---------------------------------------------------------------------------

type mergeRequest struct {
	SessionID          string `json:"session_id"`
	TicketID           string `json:"ticket_id"`
	FirNumber          string `json:"fir_number"`
	BranchVersionID    string `json:"branch_version_id"`
	MergeTargetVersionID string `json:"merge_target_version_id"`
}

type mergeResponse struct {
	Version         *DocumentVersion `json:"version"`
	Consumed        bool             `json:"consumed"`
	MergedFromHash  string           `json:"merged_from_hash"`
}

// handleMerge brings a branch back into a chosen mainline (merge target) by
// writing a NEW merge-point version. Its tree_path extends the target's
// mainline path, parent_sha256_hash chains to the target, and the new
// merged_from_hash column records the branch hash so BOTH parents are
// cryptographically traceable. Document-level RBAC applies (the officer must be
// able to append to the document overall); no per-branch permissions.
func (a *app) handleMerge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req mergeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	vat, err := a.validateAppendTicket(ctx, req.SessionID, req.TicketID)
	if err != nil {
		writeStatusError(w, err)
		return
	}
	t, role := vat.t, vat.role

	if req.FirNumber != t.FirNumber {
		http.Error(w, "fir mismatch with ticket", http.StatusForbidden)
		return
	}
	if req.BranchVersionID == "" || req.MergeTargetVersionID == "" {
		http.Error(w, "branch_version_id and merge_target_version_id are required", http.StatusBadRequest)
		return
	}

	branch, branchFir, err := a.st.versionByID(ctx, t.UserID, role, req.BranchVersionID)
	if err != nil {
		http.Error(w, "branch version not found", http.StatusNotFound)
		return
	}
	target, targetFir, err := a.st.versionByID(ctx, t.UserID, role, req.MergeTargetVersionID)
	if err != nil {
		http.Error(w, "merge target version not found", http.StatusNotFound)
		return
	}
	if branchFir != t.FirNumber || targetFir != t.FirNumber {
		http.Error(w, "version does not match ticket fir", http.StatusForbidden)
		return
	}
	// Both parents must belong to the same document for a valid merge.
	if branch.DocumentID != target.DocumentID {
		http.Error(w, "branch and merge target must belong to the same document", http.StatusBadRequest)
		return
	}

	// The merge row extends the TARGET's (mainline) path.
	mergePath, err := a.st.mergeTreePathForTarget(ctx, t.UserID, role, target.DocumentID, target.TreePath)
	if err != nil {
		log.Printf("merge tree path error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Content payload references BOTH parents' hashes + ids so the merge point's
	// full lineage is recorded (merged_from_hash also stores the branch parent).
	content, err := json.Marshal(map[string]any{
		"type":                "merge",
		"branch_version_id":   branch.ID,
		"branch_sha256":       branch.Sha256Hash,
		"target_version_id":   target.ID,
		"target_sha256":       target.Sha256Hash,
		"tree_path":           mergePath,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	versionNumber, err := a.st.nextVersionNumber(ctx, t.UserID, role, target.DocumentID)
	if err != nil {
		log.Printf("merge version number error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC()
	contentHash := hashChain(content)
	parentHash := target.Sha256Hash
	marshHash := branch.Sha256Hash
	badge := t.BadgeNumber
	createdAt := now.Format(time.RFC3339)
	// Chain binds the merge target as primary parent; the branch parent is
	// recorded in merged_from_hash AND referenced inside the content payload.
	sha := hashChain([]byte(contentHash + parentHash + badge + createdAt))
	sig, err := a.cry.sign([]byte(sha))
	if err != nil {
		log.Printf("merge sign error: %v", err)
		http.Error(w, "version signing failed", http.StatusInternalServerError)
		return
	}

	v := &DocumentVersion{
		DocumentID:       target.DocumentID,
		VersionNumber:    versionNumber,
		TreePath:         mergePath,
		Sha256Hash:       sha,
		ParentSha256Hash: parentHash,
		MergedFromHash:   marshHash,
		Signature:        base64.StdEncoding.EncodeToString(sig),
		ContentPayload:   content,
		CreatedBy:        t.UserID,
		CreatedAt:        createdAt,
		Kind:             "merge",
	}
	if err := a.st.insertDocumentVersionBranch(ctx, t.UserID, role, v); err != nil {
		log.Printf("merge version write error: %v", err)
		http.Error(w, "version write failed", http.StatusInternalServerError)
		return
	}

	auditMeta := map[string]any{
		"document_id":     target.DocumentID,
		"branch_sha256":   branch.Sha256Hash,
		"target_sha256":   target.Sha256Hash,
		"merge_tree_path": mergePath,
		"version_id":      v.ID,
	}
	if err := a.st.insertAuditLog(ctx, t.UserID, role, "VERSION_MERGED", t.FirNumber, auditMeta); err != nil {
		log.Printf("merge audit error: %v", err)
	}

	consumed, err := a.st.consumeTicket(ctx, req.TicketID)
	if err != nil {
		log.Printf("merge consume ticket error: %v", err)
	}

	writeJSON(w, http.StatusOK, mergeResponse{
		Version:        v,
		Consumed:       consumed,
		MergedFromHash: marshHash,
	})
}

// writeStatusError maps a statusError to the HTTP response.
func writeStatusError(w http.ResponseWriter, err error) {
	var se statusError
	if errors.As(err, &se) {
		http.Error(w, se.Msg, se.Code)
		return
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}
