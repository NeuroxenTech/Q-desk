package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Chain-of-custody download requests.
//
// Flow: investigating officer requests a download for a document → N=2 distinct
// SHO_SUPERVISOR/SYSTEM_ADMIN officials must approve (never the requester) →
// once approved, ONLY the original requester may execute a one-shot download =
// ZIP bundle of every version (raw content) + a JSON chain-of-custody manifest
// + a human-readable PDF certificate. A single rejection kills the request;
// unapproved requests auto-expire after the approval window.
//
// Signing model: the project keeps a SINGLE server ML-DSA-65 key pair (there is
// no per-officer key stack). Approval decisions are therefore signed with the
// server key (message = request_id|decision|decided_at), as is the outer
// certificate signature over the canonical manifest JSON. A verifier recomputes
// the per-approval digests from the JSON fields and checks both levels against
// the server public key served by GET /api/handshake.
//
// TODO (post-prototype): replace server-level approval signatures with
// per-officer ML-DSA-65 keys so each approver signs their own decision.
// ---------------------------------------------------------------------------

// downloadRequiredApprovals is the number of distinct officials that must
// approve a download request before it can be executed.
const downloadRequiredApprovals = 2

// downloadSignedURLTTL is how long the final signed ZIP URL stays valid. The
// browser should follow it immediately (60s window, not persisted in the UI).
const downloadSignedURLTTL = 60

// witnessRejected marks a request terminal as soon as any approver rejects.
// (Defined as a package-level helper so decide + tests share the wording.)
const witnessRejected = "rejected"

func canApproveDownloads(role string) bool {
	return role == "SYSTEM_ADMIN" || role == "SHO_SUPERVISOR"
}

// downloadNextStatus is the pure dual-approval transition rule:
//   - a single rejection kills the request (witness veto), regardless of how
//     many approvals already landed;
//   - otherwise the request is approved once required_approvals distinct
//     officials have approved;
//   - otherwise it stays pending.
//
// It is a pure function so the workflow logic is unit-testable without a
// database.
func downloadNextStatus(approved, rejected, required int) string {
	if rejected > 0 {
		return witnessRejected
	}
	if approved >= required {
		return "approved"
	}
	return "pending"
}

// ---------------------------------------------------------------------------
// Request DTOs
// ---------------------------------------------------------------------------

type downloadRequestIn struct {
	SessionID   string `json:"session_id"`
	BadgeNumber string `json:"badge_number"`
	DocumentID  string `json:"document_id"`
	Reason      string `json:"reason"`
}

type downloadRequestOut struct {
	Request *DownloadRequest `json:"request"`
}

type pendingDownloadsOut struct {
	Requests []DownloadRequest `json:"requests"`
}

type downloadsListOut struct {
	CanApprove bool              `json:"can_approve"`
	Requests   []DownloadRequest `json:"requests"` // requester's own history
	Pending    []DownloadRequest `json:"pending"`  // awaiting this official's decision
}

type decideIn struct {
	SessionID   string `json:"session_id"`
	BadgeNumber string `json:"badge_number"`
	Decision    string `json:"decision"` // approved | rejected
}

type decideOut struct {
	RequestID         string `json:"request_id"`
	Decision          string `json:"decision"`
	Status            string `json:"status"`
	ApprovedCount     int    `json:"approved_count"`
	RejectedCount     int    `json:"rejected_count"`
	RequiredApprovals int    `json:"required_approvals"`
}

type executeIn struct {
	SessionID   string `json:"session_id"`
	BadgeNumber string `json:"badge_number"`
}

type executeOut struct {
	RequestID      string `json:"request_id"`
	URL            string `json:"url"`
	ExpiresSeconds int    `json:"expires_seconds"`
	Filename       string `json:"filename"`
	CertificateID  string `json:"certificate_id"`
	ChainValid     string `json:"chain_valid"`
}

// ---------------------------------------------------------------------------
// POST /api/downloads/request
// ---------------------------------------------------------------------------

func (a *app) handleDownloadRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req downloadRequestIn
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !a.validSession(req.SessionID) {
		http.Error(w, "invalid or expired session", http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	uid, err := a.st.userIDByBadge(ctx, req.BadgeNumber)
	if err != nil {
		http.Error(w, "invalid badge", http.StatusUnauthorized)
		return
	}
	role, err := a.st.userRole(ctx, uid)
	if err != nil {
		log.Printf("download request role lookup error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// SYSTEM_ADMIN is never allowed to touch evidence content (file view,
	// downloads, requests) — same constraint as the content-access tickets.
	if role == "SYSTEM_ADMIN" {
		http.Error(w, "system admin may not request evidence downloads", http.StatusForbidden)
		return
	}
	req.DocumentID = strings.TrimSpace(req.DocumentID)
	req.Reason = strings.TrimSpace(req.Reason)
	if req.DocumentID == "" {
		http.Error(w, "document_id is required", http.StatusBadRequest)
		return
	}
	if req.Reason == "" {
		http.Error(w, "reason is required", http.StatusBadRequest)
		return
	}
	if len(req.Reason) > 2000 {
		http.Error(w, "reason is too long (max 2000 chars)", http.StatusBadRequest)
		return
	}

	ref, err := a.st.documentRefByID(ctx, uid, role, req.DocumentID)
	if err != nil {
		http.Error(w, "document not found", http.StatusNotFound)
		return
	}
	if ref.FIR == "" {
		http.Error(w, "document not found", http.StatusNotFound)
		return
	}
	canRead, err := a.st.canAccessDocument(ctx, uid, role, ref.FIR)
	if err != nil {
		log.Printf("download request access check error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !canRead {
		http.Error(w, "no case assignment for this document", http.StatusForbidden)
		return
	}

	now := time.Now().UTC()
	expires := now.Add(time.Duration(a.cfg.DownloadRequestTTLHours) * time.Hour)
	created, err := a.st.insertDownloadRequest(ctx, uid, role, req.DocumentID, req.BadgeNumber, req.Reason, downloadRequiredApprovals, expires)
	if err != nil {
		log.Printf("insert download request error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	created.FIR = ref.FIR
	created.Title = ref.Title

	auditMeta := map[string]any{
		"document_id":         req.DocumentID,
		"download_request_id": created.ID,
		"reason":              req.Reason,
		"required_approvals":  downloadRequiredApprovals,
		"expires_at":          expires.Format(time.RFC3339),
	}
	if err := a.st.insertAuditLog(ctx, uid, role, "DOWNLOAD_REQUESTED", ref.FIR, auditMeta); err != nil {
		log.Printf("download request audit error: %v", err)
	}

	writeJSON(w, http.StatusCreated, downloadRequestOut{Request: created})
}

// ---------------------------------------------------------------------------
// GET /api/downloads/pending — the polled "notify approvers" surface.
// ---------------------------------------------------------------------------

func (a *app) handlePendingDownloads(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := r.URL.Query().Get("session_id")
	badge := r.URL.Query().Get("badge_number")
	if sessionID == "" || !a.validSession(sessionID) {
		http.Error(w, "invalid or expired session", http.StatusUnauthorized)
		return
	}
	if badge == "" {
		http.Error(w, "badge_number is required", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	uid, err := a.st.userIDByBadge(ctx, badge)
	if err != nil {
		http.Error(w, "invalid badge", http.StatusUnauthorized)
		return
	}
	role, err := a.st.userRole(ctx, uid)
	if err != nil {
		log.Printf("pending downloads role lookup error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !canApproveDownloads(role) {
		http.Error(w, "only supervisors may approve download requests", http.StatusForbidden)
		return
	}
	if err := a.expireOverdueRequests(ctx, uid, role); err != nil {
		log.Printf("expire overdue download requests error: %v", err)
	}
	list, err := a.st.pendingDownloadRequests(ctx, uid, role, badge)
	if err != nil {
		log.Printf("pending download requests error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, pendingDownloadsOut{Requests: list})
}

// ---------------------------------------------------------------------------
// GET /api/downloads — the Downloads page data (own history + approval inbox).
// ---------------------------------------------------------------------------

func (a *app) handleDownloadsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := r.URL.Query().Get("session_id")
	badge := r.URL.Query().Get("badge_number")
	if sessionID == "" || !a.validSession(sessionID) {
		http.Error(w, "invalid or expired session", http.StatusUnauthorized)
		return
	}
	if badge == "" {
		http.Error(w, "badge_number is required", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	uid, err := a.st.userIDByBadge(ctx, badge)
	if err != nil {
		http.Error(w, "invalid badge", http.StatusUnauthorized)
		return
	}
	role, err := a.st.userRole(ctx, uid)
	if err != nil {
		log.Printf("downloads list role lookup error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := a.expireOverdueRequests(ctx, uid, role); err != nil {
		log.Printf("expire overdue download requests error: %v", err)
	}

	out := downloadsListOut{CanApprove: canApproveDownloads(role)}
	history, err := a.st.downloadRequestsForRequester(ctx, uid, role, badge)
	if err != nil {
		log.Printf("downloads history error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out.Requests = history
	if out.CanApprove {
		pending, err := a.st.pendingDownloadRequests(ctx, uid, role, badge)
		if err != nil {
			log.Printf("pending download requests error: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out.Pending = pending
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// POST /api/downloads/{requestID}/decide
// ---------------------------------------------------------------------------

func (a *app) handleDownloadDecide(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req decideIn
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !a.validSession(req.SessionID) {
		http.Error(w, "invalid or expired session", http.StatusUnauthorized)
		return
	}
	requestID := r.PathValue("requestID")
	if requestID == "" {
		http.Error(w, "request id is required", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	uid, err := a.st.userIDByBadge(ctx, req.BadgeNumber)
	if err != nil {
		http.Error(w, "invalid badge", http.StatusUnauthorized)
		return
	}
	role, err := a.st.userRole(ctx, uid)
	if err != nil {
		log.Printf("decide role lookup error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !canApproveDownloads(role) {
		http.Error(w, "only supervisors may approve download requests", http.StatusForbidden)
		return
	}
	req.Decision = strings.ToLower(strings.TrimSpace(req.Decision))
	if req.Decision != "approved" && req.Decision != witnessRejected {
		http.Error(w, "decision must be 'approved' or 'rejected'", http.StatusBadRequest)
		return
	}

	if err := a.expireOverdueRequests(ctx, uid, role); err != nil {
		log.Printf("expire overdue download requests error: %v", err)
	}
	dl, err := a.st.downloadRequestByID(ctx, uid, role, requestID)
	if err != nil {
		http.Error(w, "download request not found", http.StatusNotFound)
		return
	}
	switch dl.Status {
	case "downloaded":
		http.Error(w, "request already downloaded", http.StatusConflict)
		return
	case witnessRejected:
		http.Error(w, "request already rejected", http.StatusConflict)
		return
	case "expired":
		http.Error(w, "request expired", http.StatusConflict)
		return
	case "approved":
		http.Error(w, "request already fully approved", http.StatusConflict)
		return
	}
	if time.Now().UTC().After(dl.ExpiresAt) {
		if err := a.st.setDownloadRequestStatus(ctx, uid, role, dl.ID, "expired", nil); err == nil {
			_ = a.st.insertAuditLog(ctx, uid, role, "DOWNLOAD_EXPIRED", dl.FIR, map[string]any{"download_request_id": dl.ID})
		}
		http.Error(w, "request expired", http.StatusConflict)
		return
	}
	if dl.RequestedByBadge == req.BadgeNumber {
		http.Error(w, "requesters may not approve their own request", http.StatusBadRequest)
		return
	}
	if prior, err := a.st.downloadApprovalByBadge(ctx, uid, role, dl.ID, req.BadgeNumber); err == nil && prior != nil {
		http.Error(w, "you have already decided this request: "+prior.Decision, http.StatusConflict)
		return
	}

	decidedAt := time.Now().UTC()
	sig, err := a.cry.sign(approvalMessage(dl.ID, req.Decision, decidedAt))
	if err != nil {
		http.Error(w, "decision signing failed", http.StatusInternalServerError)
		return
	}
	signature := base64.StdEncoding.EncodeToString(sig)
	if err := a.st.insertDownloadApproval(ctx, uid, role, dl.ID, req.BadgeNumber, req.Decision, decidedAt, signature); err != nil {
		log.Printf("insert download approval error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	approvedCount, rejectedCount, err := a.st.countDownloadApprovals(ctx, uid, role, dl.ID)
	if err != nil {
		log.Printf("count download approvals error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	status := downloadNextStatus(approvedCount, rejectedCount, dl.RequiredApprovals)
	finalMeta := map[string]any{
		"download_request_id": dl.ID,
		"decision":            req.Decision,
		"signature":           signature,
		"approved_count":      approvedCount,
		"rejected_count":      rejectedCount,
		"required_approvals":  dl.RequiredApprovals,
	}
	if status != "pending" {
		if err := a.st.setDownloadRequestStatus(ctx, uid, role, dl.ID, status, nil); err != nil {
			log.Printf("set download request status error: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		finalMeta["request_status"] = status
	}
	action := "DOWNLOAD_APPROVED"
	if req.Decision == witnessRejected {
		action = "DOWNLOAD_REJECTED"
	}
	if err := a.st.insertAuditLog(ctx, uid, role, action, dl.FIR, finalMeta); err != nil {
		log.Printf("download decision audit error: %v", err)
	}

	writeJSON(w, http.StatusOK, decideOut{
		RequestID:         dl.ID,
		Decision:          req.Decision,
		Status:            status,
		ApprovedCount:     approvedCount,
		RejectedCount:     rejectedCount,
		RequiredApprovals: dl.RequiredApprovals,
	})
}

// ---------------------------------------------------------------------------
// POST /api/downloads/{requestID}/execute
// ---------------------------------------------------------------------------

func (a *app) handleDownloadExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req executeIn
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !a.validSession(req.SessionID) {
		http.Error(w, "invalid or expired session", http.StatusUnauthorized)
		return
	}
	requestID := r.PathValue("requestID")
	if requestID == "" {
		http.Error(w, "request id is required", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	uid, err := a.st.userIDByBadge(ctx, req.BadgeNumber)
	if err != nil {
		http.Error(w, "invalid badge", http.StatusUnauthorized)
		return
	}
	role, err := a.st.userRole(ctx, uid)
	if err != nil {
		log.Printf("execute role lookup error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := a.expireOverdueRequests(ctx, uid, role); err != nil {
		log.Printf("expire overdue download requests error: %v", err)
	}
	dl, err := a.st.downloadRequestByID(ctx, uid, role, requestID)
	if err != nil {
		http.Error(w, "download request not found", http.StatusNotFound)
		return
	}
	if dl.RequestedByBadge != req.BadgeNumber {
		http.Error(w, "only the requesting officer may execute this download", http.StatusForbidden)
		return
	}
	switch dl.Status {
	case "pending":
		http.Error(w, "request not yet approved", http.StatusForbidden)
		return
	case witnessRejected:
		http.Error(w, "request was rejected", http.StatusForbidden)
		return
	case "expired":
		http.Error(w, "request expired", http.StatusConflict)
		return
	case "downloaded":
		http.Error(w, "request already downloaded; submit a new request", http.StatusConflict)
		return
	}

	// Chain integrity is re-verified server-side and FAILS CLOSED: a corrupt or
	// fragmented chain never reaches the ZIP, even though the certificate would
	// also carry the offending version's chain_valid marker.
	chain, err := a.st.downloadChainByDocument(ctx, uid, role, dl.DocumentID)
	if err != nil {
		log.Printf("load download chain error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(chain) == 0 {
		http.Error(w, "document has no versions", http.StatusConflict)
		return
	}
	chainValid, reason := verifyChainedVersions(chain)
	if chainValid != "valid" {
		log.Printf("chain-of-custody verification failed for request %s: %s", dl.ID, reason)
		http.Error(w, "chain integrity verification failed", http.StatusInternalServerError)
		return
	}

	approvals, err := a.st.downloadApprovalsForRequest(ctx, uid, role, dl.ID)
	if err != nil {
		log.Printf("load download approvals error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	ref := requestRef{FIR: dl.FIR, Title: dl.Title}
	if ref.FIR == "" {
		if dr, err := a.st.documentRefByID(ctx, uid, role, dl.DocumentID); err == nil {
			ref = *dr
		}
	}
	downloadedAt := time.Now().UTC()

	cert, manifest, err := buildCertificate(a.cry, dl, ref, chain, approvals, downloadedAt)
	if err != nil {
		log.Printf("build certificate error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	pdfBytes, err := renderCertificatePDF(cert)
	if err != nil {
		log.Printf("render certificate pdf error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	zipName := downloadZipName(ref.FIR, dl.ID)
	zipBytes, err := buildDownloadZip(manifest, pdfBytes, chain)
	if err != nil {
		log.Printf("build download zip error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if !a.stg.ready() {
		http.Error(w, "evidence storage not configured", http.StatusServiceUnavailable)
		return
	}
	key := "downloads/" + dl.ID + "/" + zipName
	if err := a.stg.uploadObject(ctx, exportBucket, key, "application/zip", bytes.NewReader(zipBytes)); err != nil {
		log.Printf("upload download package error: %v", err)
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	url, err := a.stg.signedURL(ctx, exportBucket, key, downloadSignedURLTTL, true)
	if err != nil {
		log.Printf("sign download package error: %v", err)
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}

	// One-shot: only the first caller to transition 'approved' → 'downloaded'
	// wins; the bytes we already staged stay under the request's folder but the
	// URL is never returned to a second caller.
	committed, err := a.st.markDownloadExecuted(ctx, uid, role, dl.ID, downloadedAt)
	if err != nil {
		log.Printf("mark download executed error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !committed {
		http.Error(w, "request already downloaded; submit a new request", http.StatusConflict)
		return
	}

	meta := map[string]any{
		"download_request_id": dl.ID,
		"certificate_id":      cert.CertificateID,
		"document_id":         dl.DocumentID,
		"versions":            len(chain),
		"zip_filename":        zipName,
	}
	if err := a.st.insertAuditLog(ctx, uid, role, "DOWNLOAD_EXECUTED", ref.FIR, meta); err != nil {
		log.Printf("download execute audit error: %v", err)
	}

	writeJSON(w, http.StatusOK, executeOut{
		RequestID:      dl.ID,
		URL:            url,
		ExpiresSeconds: downloadSignedURLTTL,
		Filename:       zipName,
		CertificateID:  cert.CertificateID,
		ChainValid:     chainValid,
	})
}

// expireOverdueRequests flips 'pending' requests past their approval window to
// 'expired' and records one DOWNLOAD_EXPIRED audit row per affected request.
// Runs under the calling official's RLS identity (requester history views and
// approver inboxes both trigger it); failures are non-fatal for the caller.
func (a *app) expireOverdueRequests(ctx context.Context, uid, role string) error {
	expired, err := a.st.expirePendingDownloadRequests(ctx, uid, role)
	if err != nil {
		return err
	}
	for _, id := range expired {
		if err := a.st.insertAuditLog(ctx, uid, role, "DOWNLOAD_EXPIRED", "", map[string]any{"download_request_id": id}); err != nil {
			log.Printf("download request expire audit error: %v", err)
		}
	}
	return nil
}

// approvalMessage is the canonical bytes signed for a download decision:
// request_id|decision|decided_at (RFC3339, second precision). The certificate
// re-embeds the same fields so a verifier can recompute the digest.
func approvalMessage(requestID, decision string, decidedAt time.Time) []byte {
	return []byte(requestID + "|" + decision + "|" + decidedAt.Format(time.RFC3339))
}

// verifyChainedVersions re-derives every version's content hash and re-runs the
// stored-signature verification (exact same check as the Versions audit view),
// reporting "valid" only when every version — mainline, branch and merge —
// passes. Any failure returns "invalid" with a human-readable reason.
func verifyChainedVersions(rows []downloadChainRow) (string, string) {
	for i := range rows {
		r := &rows[i]
		contentHash := hashChain(r.Content)
		createdAt := r.CreatedAt.UTC().Format(time.RFC3339)
		verdict := verifyChain(r.Sha256Hash, contentHash, r.ParentHash, r.Badge, createdAt)
		if verdict != "valid" {
			return "invalid", fmt.Sprintf("version %d (%s): %s", r.Version, r.TreePath, verdict)
		}
	}
	return "valid", ""
}

// downloadZipName builds a stable, URL-safe ZIP filename for a package.
func downloadZipName(fir, requestID string) string {
	shortID := requestID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return fmt.Sprintf("qdesk-coc-%s-%s.zip", safeSlug(fir), shortID)
}

// safeSlug normalizes a string into a filesystem/URL-safe slug.
func safeSlug(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "case"
	}
	return b.String()
}

// buildDownloadZip assembles the downloadable package: the signed JSON
// chain-of-custody manifest, the PDF certificate, and every document version's
// raw content payload under <tree-path>/<version>.zip entries mirror the
// version structure. The archive is re-opened and fully re-read before it is
// returned so a ZIP bomb or truncated write can never reach the client.
func buildDownloadZip(manifest, pdf []byte, chain []downloadChainRow) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	manifestName := "chain-of-custody.json"
	mw, err := zw.Create(manifestName)
	if err != nil {
		return nil, err
	}
	if _, err := mw.Write(manifest); err != nil {
		return nil, err
	}
	pw, err := zw.Create("chain-of-custody-certificate.pdf")
	if err != nil {
		return nil, err
	}
	if _, err := pw.Write(pdf); err != nil {
		return nil, err
	}
	for i := range chain {
		r := &chain[i]
		// ZIP entry paths are always forward-slash separated (ZIP spec), so
		// join manually instead of filepath.Join (which is OS-dependent).
		base := safeSlug(r.TreePath) + "/" + downloadVersionFileName(r)
		fw, err := zw.Create(base)
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write(r.Content); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(io.Discard, rc); err != nil {
			rc.Close()
			return nil, err
		}
		rc.Close()
	}
	return buf.Bytes(), nil
}

// downloadVersionFileName picks the original evidence filename when one was
// recorded in the version payload, and falls back to a deterministic
// v<number>-<tree-path> name otherwise. The original is only used when it is a
// safe base name (no path separators, not "." or "..").
func downloadVersionFileName(r *downloadChainRow) string {
	if _, orig := parsePayloadMeta(r.Content); orig != "" && safeBaseName(orig) != "" {
		return safeBaseName(orig)
	}
	return fmt.Sprintf("v%d-%s.bin", r.Version, safeSlug(r.TreePath))
}

// safeBaseName returns the final path component of a filename, or "" when the
// name is empty or resolves to "." / ".." (path-traversal guard for ZIP entry
// names and download filenames).
func safeBaseName(name string) string {
	n := name
	if i := strings.LastIndexAny(n, `/\`); i >= 0 {
		n = n[i+1:]
	}
	if n == "" || n == "." || n == ".." {
		return ""
	}
	return n
}
