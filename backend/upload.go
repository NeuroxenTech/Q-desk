package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Evidence upload policy
// ---------------------------------------------------------------------------

// Sentinel errors the shared evidence-file ingest helper returns so each caller
// can map them to the HTTP semantics the upload flow always used. Keep these in
// sync with the plain-text messages the frontend upload dialog matches on (see
// describeError in UploadEvidenceDialog.tsx).
var (
	errFileTypeNotAllowed   = errors.New("file type not allowed: expected jpeg, png, pdf, mp4, mp3, or wav")
	errStorageNotConfigured = errors.New("evidence storage not configured")
)

// canUploadEvidence reports whether a role may ingest evidence files. The LEA
// role hierarchy reserves content-grade actions for investigating officers:
// - INSPECTOR / SUB_INSPECTOR / HEAD_CONSTABLE / CONSTABLE  -> may upload
// - SHO_SUPERVISOR  -> station-level supervisor, audit-only (no content write)
// - SYSTEM_ADMIN    -> platform administration, never touches evidence content
// Enforcement happens server-side here; the UI mirrors this helper for
// visibility only (defense-in-depth, never trust the client).
func canUploadEvidence(role string) bool {
	switch role {
	case "SYSTEM_ADMIN", "SHO_SUPERVISOR":
		return false
	}
	return true
}

// allowedUploadTypes is the MIME allowlist for evidence files, kept in sync
// with the storage bucket's allowed_mime_types at ensureBucket time.
func allowedUploadTypes() []string {
	return []string{"image/jpeg", "image/png", "application/pdf", "video/mp4", "audio/mpeg", "audio/wav"}
}

var safeFileRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// safeFileName strips everything that is not URL-safe from an uploaded
// filename. Only the basename is ever used — never the client-supplied path.
func safeFileName(name string) string {
	base := filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	return safeFileRe.ReplaceAllString(base, "_")
}

// sniffMimeType identifies a file from its magic bytes, returning the MIME
// type + extension for the allowlist. Declared Content-Type from the client is
// never trusted; bytes are authoritative.
func sniffMimeType(head []byte) (mime, ext string, ok bool) {
	if len(head) < 4 {
		return "", "", false
	}
	switch {
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return "image/jpeg", ".jpg", true
	case len(head) >= 8 && bytes.Equal(head[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png", ".png", true
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return "application/pdf", ".pdf", true
	case len(head) >= 8 && bytes.Equal(head[4:8], []byte("ftyp")):
		return "video/mp4", ".mp4", true
	case bytes.HasPrefix(head, []byte("ID3")):
		return "audio/mpeg", ".mp3", true
	case len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		return "audio/mpeg", ".mp3", true
	case len(head) >= 12 && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WAVE")):
		return "audio/wav", ".wav", true
	}
	return "", "", false
}

// storageKeyFor builds the object key for an uploaded file inside the private
// bucket: fir/<FIR>/<sha256>/<safe-filename>.
func storageKeyFor(fir, sha256hex, name string) string {
	return fir + "/" + sha256hex + "/" + name
}

// splitStorageKey splits an object key into its path segments so each can be
// individually URL-escaped when building Storage API paths.
func splitStorageKey(key string) []string {
	return strings.Split(key, "/")
}

// ---------------------------------------------------------------------------
// Malware scanning (structured for a real backend later)
// ---------------------------------------------------------------------------

// malwareScanner is the interface a real scanner (e.g. ClamAV) will implement.
// The prototype wires no scanner; when MALWARE_SCAN_ENABLED=true the handler
// fails closed (503/500) rather than silently passing unscanned files.
type malwareScanner interface {
	ready() bool
	// Scan inspects a staged temp file and reports whether it is clean.
	Scan(ctx context.Context, path string) (bool, error)
}

// unconfiguredScanner is the placeholder scanner. ready() is false, so
// enabling MALWARE_SCAN_ENABLED without wiring a real backend rejects uploads.
type unconfiguredScanner struct{}

func (unconfiguredScanner) ready() bool                         { return false }
func (unconfiguredScanner) Scan(context.Context, string) (bool, error) {
	return false, errors.New("no malware scanner backend wired")
}

// scanEvidence runs the malware policy on a staged temp file and returns the
// scan_status to record in audit_logs ("clean" | "skipped_prototype_mode").
// In prototype mode the file is deliberately not scanned and a warning is
// logged so operators are never silently "protected".
func (a *app) scanEvidence(ctx context.Context, path string) (string, error) {
	if !a.cfg.MalwareScanEnabled {
		log.Printf("malware scan DISABLED (prototype mode) — evidence upload not scanned; marked skipped_prototype_mode in audit trail")
		return "skipped_prototype_mode", nil
	}
	if a.malware == nil || !a.malware.ready() {
		return "", errors.New("malware scanner not configured (set MALWARE_SCAN_ENABLED=true requires a scanner backend)")
	}
	clean, err := a.malware.Scan(ctx, path)
	if err != nil {
		return "", err
	}
	if !clean {
		return "infected", errors.New("evidence blocked: malicious content detected")
	}
	return "clean", nil
}

// ---------------------------------------------------------------------------
// POST /api/upload
// ---------------------------------------------------------------------------

// uploadForm is the parsed multipart body of an upload request.
type uploadForm struct {
	SessionID        string
	TicketID         string
	FirNumber        string
	IsNewDoc         bool
	DocumentID       string
	Title            string
	Classification   string
	VersionNumber    int
	TreePath         string
	ParentHash       string
	OriginalFilename string
	TempPath         string
	SizeBytes        int64
}

type uploadResponse struct {
	Version  map[string]any `json:"version"`
	Consumed bool           `json:"consumed"`
	ScanStatus string       `json:"scan_status"`
}

// handleUpload ingests a single evidence file (multipart/form-data) under an
// append-scope ticket, verifies its magic bytes against the allowlist, uploads
// the bytes to the private storage bucket, then writes a hash-chained
// document_versions row signed by the server. The single-use ticket is
// consumed only after the DB row commits.
//
// Form fields:
//
//	session_id, ticket_id, fir_number                     (required)
//	is_new_document          "true" | "false"
//	document_id              required when is_new_document=false
//	title                    required when is_new_document=true
//	classification_level     optional when is_new_document=true (RESTRICTED)
//	version_number, tree_path, parent_sha256_hash         (chain inputs)
//	file                     the evidence bytes (required)
func (a *app) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Cap the whole request body a little above the file cap so multipart
	// field overhead never pushes an in-spec file over the limit.
	maxBytes := a.stg.maxBytes()
	overhead := int64(64 * 1024)
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+overhead)

	f, err := a.parseUploadMultipart(r)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer os.Remove(f.TempPath)

	if f.SizeBytes > maxBytes {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}

	ctx := r.Context()

	// Session + ticket.
	if !a.validSession(f.SessionID) {
		http.Error(w, "invalid or expired session", http.StatusUnauthorized)
		return
	}
	t, err := a.st.getTicket(ctx, f.TicketID)
	if err != nil {
		http.Error(w, "ticket not found or consumed", http.StatusForbidden)
		return
	}
	if t.Scope != TicketScopeAppend && t.Scope != TicketScopeAll {
		http.Error(w, "ticket lacks append scope", http.StatusForbidden)
		return
	}
	if time.Now().UTC().After(t.ExpiresAt) {
		http.Error(w, "ticket expired", http.StatusForbidden)
		return
	}
	role, err := a.st.userRole(ctx, t.UserID)
	if err != nil {
		log.Printf("upload role lookup error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !canUploadEvidence(role) {
		http.Error(w, "role may not upload evidence", http.StatusForbidden)
		return
	}
	if f.FirNumber != t.FirNumber {
		http.Error(w, "fir mismatch with ticket", http.StatusForbidden)
		return
	}
	if assigned, _, err := a.st.validateAssignment(ctx, t.UserID, role, t.UserID, t.FirNumber); err != nil || !assigned {
		http.Error(w, "no case assignment for this fir", http.StatusForbidden)
		return
	}

	// Validate + store the evidence file via the shared ingest helper (magic-byte
	// sniff, malware policy, SHA-256, storage upload). The returned key is used
	// to record the object location on the document/version rows.
	ingested, scanStatus, err := a.ingestEvidenceFile(ctx, f.TempPath, f.OriginalFilename, f.SizeBytes, t.FirNumber)
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
		log.Printf("upload ingest error: %v", err)
		http.Error(w, "evidence storage upload failed", http.StatusInternalServerError)
		return
	}
	key := ingested.StorageKey
	mime := ingested.Mime
	payloadMeta := ingested.Meta
	sum := ingested.Meta.Sha256

	// Resolve the parent document.
	docID := f.DocumentID
	if f.IsNewDoc {
		if strings.TrimSpace(f.Title) == "" {
			a.stg.deleteObject(ctx, key)
			http.Error(w, "title is required for a new document", http.StatusBadRequest)
			return
		}
		if f.Classification == "" {
			f.Classification = "RESTRICTED"
		}
		existing, err := a.st.documentIDForFirTitle(ctx, t.UserID, role, t.FirNumber, f.Title)
		if err != nil {
			log.Printf("upload document lookup error: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if existing != "" {
			a.stg.deleteObject(ctx, key)
			http.Error(w, fmt.Sprintf("a document already exists with this title under %s", t.FirNumber), http.StatusConflict)
			return
		}
		docID, err = a.st.insertDocument(ctx, t.UserID, role, t.FirNumber, f.Title, f.Classification, key, t.UserID)
		if err != nil {
			a.stg.deleteObject(ctx, key)
			log.Printf("upload document insert error: %v", err)
			http.Error(w, "document create failed", http.StatusInternalServerError)
			return
		}
	} else {
		if docID == "" {
			a.stg.deleteObject(ctx, key)
			http.Error(w, "document_id is required when is_new_document=false", http.StatusBadRequest)
			return
		}
		fir, err := a.st.documentFirByID(ctx, t.UserID, role, docID)
		if err != nil {
			a.stg.deleteObject(ctx, key)
			http.Error(w, "document not accessible", http.StatusForbidden)
			return
		}
		if fir != t.FirNumber {
			a.stg.deleteObject(ctx, key)
			http.Error(w, "document does not belong to this fir", http.StatusForbidden)
			return
		}
	}

	// Build the content payload + hash chain (mirrors /api/append exactly so
	// the shared /api/versions verification keeps working for uploads).
	payload, err := json.Marshal(payloadMeta)
	if err != nil {
		a.stg.deleteObject(ctx, key)
		log.Printf("upload payload marshal error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC()
	contentHash := hashChain(payload)
	parentHash := f.ParentHash
	badge := t.BadgeNumber
	createdAt := now.Format(time.RFC3339)
	sha := hashChain([]byte(contentHash + parentHash + badge + createdAt))
	sig, err := a.cry.sign([]byte(sha))
	if err != nil {
		a.stg.deleteObject(ctx, key)
		log.Printf("upload sign error: %v", err)
		http.Error(w, "version signing failed", http.StatusInternalServerError)
		return
	}

	v := &DocumentVersion{
		DocumentID:       docID,
		VersionNumber:    f.VersionNumber,
		TreePath:         f.TreePath,
		Sha256Hash:       sha,
		ParentSha256Hash: parentHash,
		Signature:        base64.StdEncoding.EncodeToString(sig),
		ContentPayload:   payload,
		CreatedBy:        t.UserID,
		CreatedAt:        createdAt,
	}
	// tree_path must be valid ltree; the UI always passes it, but guard against
	// a swallowed empty value so the insert cannot fail on a NULL.
	if v.TreePath == "" {
		v.TreePath = fmt.Sprintf("v%d", v.VersionNumber)
	}
	if err := a.st.insertDocumentVersion(ctx, t.UserID, role, v); err != nil {
		// Roll the storage object back so no orphaned bytes survive the failed
		// chain write.
		a.stg.deleteObject(ctx, key)
		log.Printf("upload version write error: %v", err)
		http.Error(w, "version write failed", http.StatusInternalServerError)
		return
	}

	// Keep the documents row pointing at where the file was saved (non-fatal:
	// the version row is already committed).
	if err := a.st.setDocumentStoragePath(ctx, t.UserID, role, docID, key); err != nil {
		log.Printf("upload storage_path update error: %v", err)
	}

	// Hash-chained audit record for the UPLOAD action, incl. scan status.
	auditMeta := map[string]any{
		"document_id":       docID,
		"version_number":    v.VersionNumber,
		"filename":          payloadMeta.OriginalFilename,
		"mime_type":         mime,
		"size_bytes":        f.SizeBytes,
		"sha256":            sum,
		"scan_status":       scanStatus,
		"classification":    f.Classification,
		"tree_path":         f.TreePath,
	}
	if err := a.st.insertAuditLog(ctx, t.UserID, role, "UPLOAD", t.FirNumber, auditMeta); err != nil {
		log.Printf("upload audit error: %v", err)
	}

	// Single-use append ticket consumed only after the row commits.
	consumed, err := a.st.consumeTicket(ctx, f.TicketID)
	if err != nil {
		log.Printf("upload consume ticket error: %v", err)
	}

	writeJSON(w, http.StatusOK, uploadResponse{
		Version: map[string]any{
			"document_id":       docID,
			"version_id":        v.ID,
			"version_number":    v.VersionNumber,
			"tree_path":         v.TreePath,
			"sha256_hash":       v.Sha256Hash,
			"content_hash":      contentHash,
			"signature":         v.Signature,
			"mime_type":         mime,
			"filename":          payloadMeta.OriginalFilename,
			"size_bytes":        f.SizeBytes,
			"storage_path":      key,
			"created_at":        createdAt,
			"classification":    f.Classification,
		},
		Consumed:   consumed,
		ScanStatus: scanStatus,
	})
}

// extRecheck appends a file extension when the sanitised original filename has
// none (e.g. "IMG_1234" -> "IMG_1234.jpg") so downloaded files stay openable.
func extRecheck(name, ext string) string {
	lower := strings.ToLower(name)
	switch ext {
	case ".jpg":
		if strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") {
			return ""
		}
	case ".png", ".pdf", ".mp4", ".mp3", ".wav":
		if strings.HasSuffix(lower, ext) {
			return ""
		}
	}
	return ext
}

// ingestedFile is the result of validating + storing an uploaded evidence file.
type ingestedFile struct {
	Meta       payloadMeta
	Mime       string
	StorageKey string
}

// ingestEvidenceFile validates + stores an uploaded evidence file: sniffed the
// magic bytes against the allowlist, runs the malware policy, computes the
// SHA-256, uploads it to the private bucket and returns the metadata to
// persist. It is shared by /api/upload and /api/versions/branch (file variant)
// so the validation/storage logic is never duplicated.
//
// On success the storage object exists in the bucket; the caller owns rolling it
// back (deleteObject) if a later DB write fails. The scanStatus string is the
// value to record in audit_logs.
func (a *app) ingestEvidenceFile(ctx context.Context, tempPath, originalFilename string, sizeBytes int64, fir string) (*ingestedFile, string, error) {
	// Magic-byte sniffing (client-declared type is never trusted).
	head, err := readHead(tempPath, 512)
	if err != nil {
		return nil, "", err
	}
	mime, ext, ok := sniffMimeType(head)
	if !ok {
		return nil, "", errFileTypeNotAllowed
	}

	// Malware policy.
	scanStatus, err := a.scanEvidence(ctx, tempPath)
	if err != nil {
		return nil, scanStatus, err
	}

	// Server-computed SHA-256 of the raw bytes + storage object key.
	sum := hashFile(tempPath)
	key := storageKeyFor(fir, sum, safeFileName(originalFilename)+extRecheck(originalFilename, ext))

	if !a.stg.ready() {
		return nil, scanStatus, errStorageNotConfigured
	}
	file, err := os.Open(tempPath)
	if err != nil {
		return nil, scanStatus, err
	}
	if err := a.stg.uploadObject(ctx, key, mime, file); err != nil {
		file.Close()
		return nil, scanStatus, err
	}
	file.Close()

	meta := payloadMeta{
		StoragePath:      key,
		OriginalFilename: safeFileName(originalFilename),
		MimeType:         mime,
		SizeBytes:        sizeBytes,
		Sha256:           sum,
	}
	return &ingestedFile{Meta: meta, Mime: mime, StorageKey: key}, scanStatus, nil
}

// hashFile computes the lowercase hex SHA-256 of a file.
func hashFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, n)
	got, err := io.ReadFull(f, b)
	if err == io.ErrUnexpectedEOF {
		return b[:got], nil
	}
	if err != nil {
		return nil, err
	}
	return b, nil
}

// parseUploadMultipart streams the multipart body into a temp file, capturing
// the form fields alongside. The file part is the only large field; everything
// else is read in full.
func (a *app) parseUploadMultipart(r *http.Request) (*uploadForm, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, errors.New("multipart body required")
	}
	f := &uploadForm{}
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
			file, err := os.CreateTemp("", "qdesk-evidence-*")
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
				return nil, err
			}
		case "is_new_document":
			v, _ := io.ReadAll(part)
			f.IsNewDoc = strings.TrimSpace(string(v)) == "true"
			part.Close()
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
			case "document_id":
				f.DocumentID = set
			case "title":
				f.Title = set
			case "classification_level":
				f.Classification = set
			case "version_number":
				f.VersionNumber = atoiDefault(set)
			case "tree_path":
				f.TreePath = set
			case "parent_sha256_hash":
				f.ParentHash = set
			}
		}
	}

	if !seenFile {
		os.Remove(f.TempPath)
		return nil, errors.New("file part is required")
	}
	return f, nil
}

func atoiDefault(s string) int {
	n := 0
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// ---------------------------------------------------------------------------
// GET /api/files/{version_id}/download-url
// ---------------------------------------------------------------------------

type downloadURLResponse struct {
	URL            string `json:"url"`
	MimeType       string `json:"mime_type"`
	Filename       string `json:"filename"`
	SizeBytes      int64  `json:"size_bytes"`
	Version        int    `json:"version_number"`
	FIR            string `json:"fir_number"`
	DocumentID     string `json:"document_id"`
	ExpiresSeconds int    `json:"expires_seconds"`
}

// handleDownloadURL returns a capability URL for a stored evidence version: a
// GET to /api/files/{version_id}/content streams the bytes through the
// backend. It requires a live view-scope (stream/all) ticket for the same FIR —
// the version's document FIR must match the ticket FIR. No permanent or public
// URL is ever returned; the capability expires when the ticket does.
func (a *app) handleDownloadURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	versionID := r.PathValue("versionID")
	if versionID == "" {
		http.Error(w, "version id is required", http.StatusBadRequest)
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	ticketID := r.URL.Query().Get("ticket_id")
	if sessionID == "" || !a.validSession(sessionID) {
		http.Error(w, "invalid or expired session", http.StatusUnauthorized)
		return
	}
	if ticketID == "" {
		http.Error(w, "ticket_id is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	t, err := a.st.getTicket(ctx, ticketID)
	if err != nil {
		http.Error(w, "ticket not found or consumed", http.StatusForbidden)
		return
	}
	if t.Scope != TicketScopeStream && t.Scope != TicketScopeAll {
		http.Error(w, "ticket lacks view scope", http.StatusForbidden)
		return
	}
	if time.Now().UTC().After(t.ExpiresAt) {
		http.Error(w, "ticket expired", http.StatusForbidden)
		return
	}

	role, err := a.st.userRole(ctx, t.UserID)
	if err != nil {
		log.Printf("download role lookup error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	v, fir, err := a.st.versionByID(ctx, t.UserID, role, versionID)
	if err != nil {
		http.Error(w, "version not found", http.StatusNotFound)
		return
	}
	if fir != t.FirNumber {
		http.Error(w, "version does not match ticket fir", http.StatusForbidden)
		return
	}

	var meta payloadMeta
	if err := json.Unmarshal(v.ContentPayload, &meta); err != nil || meta.StoragePath == "" {
		http.Error(w, "version has no file content", http.StatusBadRequest)
		return
	}
	if !a.stg.ready() {
		http.Error(w, "evidence storage not configured", http.StatusServiceUnavailable)
		return
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	contentURL := scheme + "://" + r.Host + "/api/files/" + url.PathEscape(versionID) + "/content" +
		"?session_id=" + url.QueryEscape(sessionID) + "&ticket_id=" + url.QueryEscape(ticketID)

	writeJSON(w, http.StatusOK, downloadURLResponse{
		URL:            contentURL,
		MimeType:       meta.MimeType,
		Filename:       meta.OriginalFilename,
		SizeBytes:      meta.SizeBytes,
		Version:        v.VersionNumber,
		FIR:            fir,
		DocumentID:     v.DocumentID,
		ExpiresSeconds: 60,
	})
}

// silence linter unused-import guard for multipart (used in parse signature).