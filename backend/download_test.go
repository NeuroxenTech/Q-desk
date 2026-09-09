package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Pure logic tests — always run, no database required.
// ---------------------------------------------------------------------------

// TestDownloadNextStatus exercises the dual-approval transition rule: two
// distinct approvals unlock 'approved', a single rejection vetoes the request
// even after approval count was reached, and anything else stays pending.
func TestDownloadNextStatus(t *testing.T) {
	logic := []struct {
		approved int
		rejected int
		required int
		want     string
	}{
		{0, 0, 2, "pending"},
		{1, 0, 2, "pending"},  // first approval does NOT unlock
		{2, 0, 2, "approved"}, // second approval unlocks
		{1, 1, 2, "rejected"}, // a rejection vetoes the first approval
		{2, 1, 2, "rejected"}, // one rejection kills even a fully-approved request
		{1, 0, 1, "approved"}, // single-approval configs still work
	}
	for _, c := range logic {
		if got := downloadNextStatus(c.approved, c.rejected, c.required); got != c.want {
			t.Errorf("downloadNextStatus(%d,%d,%d) = %q, want %q", c.approved, c.rejected, c.required, got, c.want)
		}
	}
}

// TestCanApproveDownloads verifies only SHO_SUPERVISOR / SYSTEM_ADMIN can sign
// a decision; investigating officers never can.
func TestCanApproveDownloads(t *testing.T) {
	cases := map[string]bool{
		"SYSTEM_ADMIN":   true,
		"SHO_SUPERVISOR": true,
		"INSPECTOR":      false,
		"SUB_INSPECTOR":  false,
		"HEAD_CONSTABLE": false,
		"CONSTABLE":      false,
		"":               false,
	}
	for role, want := range cases {
		if got := canApproveDownloads(role); got != want {
			t.Errorf("canApproveDownloads(%q) = %v, want %v", role, got, want)
		}
	}
}

// TestApprovalMessageDeterminism ensures the per-approval signed message is
// exactly request_id|decision|decided_at in RFC3339, so a verifier can
// recompute it from the certificate JSON.
func TestApprovalMessageDeterminism(t *testing.T) {
	at, _ := time.Parse(time.RFC3339, "2026-09-05T12:30:00Z")
	want := "req-uuid-1|approved|2026-09-05T12:30:00Z"
	if got := string(approvalMessage("req-uuid-1", "approved", at)); got != want {
		t.Fatalf("approvalMessage = %q, want %q", got, want)
	}
}

// mkChainRow builds a self-consistent chained version row exactly the way the
// upload/append/branch handlers do: sha256_hash = SHA256(content_hash || parent
// || badge || created_at).
func mkChainRow(version int, path, badge, content, parent, createdAt string) downloadChainRow {
	contentHash := hashChain([]byte(content))
	sha := hashChain([]byte(contentHash + parent + badge + createdAt))
	ts, _ := time.Parse(time.RFC3339, createdAt)
	return downloadChainRow{
		Version:     version,
		TreePath:    path,
		Sha256Hash:  sha,
		ParentHash:  parent,
		Badge:       badge,
		CreatedAt:   ts,
		Content:     []byte(content),
		Signature:   "sig-" + path,
		ContentType: classifyPayload([]byte(content)),
	}
}

func validChainRows() []downloadChainRow {
	createdAt := "2026-09-05T09:00:00Z"
	v1 := mkChainRow(1, "v1", "IND-IO-402", "statement page 1", "", createdAt)
	v2 := mkChainRow(2, "v2", "IND-IO-402", "statement page 2", v1.Sha256Hash, "2026-09-05T09:05:00Z")
	return []downloadChainRow{v1, v2}
}

// TestVerifyChainedVersions checks the fail-closed integrity gate: a valid
// chain verifies, tampering with any version's content trips it.
func TestVerifyChainedVersions(t *testing.T) {
	rows := validChainRows()
	if verdict, reason := verifyChainedVersions(rows); verdict != "valid" {
		t.Fatalf("valid chain reported %q (%s)", verdict, reason)
	}

	tampered := validChainRows()
	tampered[1].Content = []byte("tampered page 2!")
	if verdict, _ := verifyChainedVersions(tampered); verdict != "valid" {
		t.Log("tampered content correctly detected")
	} else {
		t.Fatal("tampered chain should be invalid")
	}

	reparented := validChainRows()
	reparented[1].ParentHash = hashChain([]byte("not the real parent"))
	if verdict, _ := verifyChainedVersions(reparented); verdict == "valid" {
		t.Fatal("chain rewired to a different parent should be invalid")
	}
}

// TestBuildCertificateSignVerify builds a real certificate with generated
// ML-DSA-65 keys and checks the outer signature covers exactly the manifest
// JSON with server_signature empty.
func TestBuildCertificateSignVerify(t *testing.T) {
	cry, err := newServerCrypto(&Config{})
	if err != nil {
		t.Fatalf("newServerCrypto: %v", err)
	}
	reqAt, _ := time.Parse(time.RFC3339, "2026-09-05T10:00:00Z")
	expAt, _ := time.Parse(time.RFC3339, "2026-09-07T10:00:00Z")
	dlAt, _ := time.Parse(time.RFC3339, "2026-09-05T10:05:00Z")
	req := &DownloadRequest{
		ID:                "req-11111111-2222-3333-4444-555555555555",
		DocumentID:        "doc-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		Reason:            "Referral to prosecution",
		RequestedByBadge:  "IND-IO-402",
		RequesterName:     "Inspector Rajesh Kumar",
		RequiredApprovals: 2,
		RequestedAt:       reqAt,
		ExpiresAt:         expAt,
		FIR:               "FIR-2026-0089",
		Title:             "Crime scene statement",
	}
	approvals := []DownloadApproval{
		{ApprovedByBadge: "IND-SHO-999", ApproverName: "Station House Officer", Decision: "approved", DecidedAt: reqAt, Signature: "s1"},
		{ApprovedByBadge: "IND-SHO-001", ApproverName: "Sub Inspector Anita Desai", Decision: "approved", DecidedAt: expAt, Signature: "s2"},
	}
	ref := requestRef{FIR: "FIR-2026-0089", Title: "Crime scene statement", Classification: "RESTRICTED"}

	cert, manifest, err := buildCertificate(cry, req, ref, validChainRows(), approvals, dlAt)
	if err != nil {
		t.Fatalf("buildCertificate: %v", err)
	}
	if cert.ChainValid != "valid" {
		t.Fatalf("certificate chain_valid = %q, want valid", cert.ChainValid)
	}
	if len(cert.Approvals) != 2 || cert.Approvals[0].DecidedAt != "2026-09-05T10:00:00Z" {
		t.Fatalf("approvals embedded incorrectly: %+v", cert.Approvals)
	}
	if cert.ServerSignature == "" {
		t.Fatal("certificate must carry a server signature")
	}
	sigBytes, err := base64.StdEncoding.DecodeString(cert.ServerSignature)
	if err != nil {
		t.Fatalf("server signature is not base64: %v", err)
	}

	// Independent verification: unmarshal the shipped manifest, zero the
	// signature, re-marshal with the same struct → bytes must match toSign.
	var parsed chainOfCustodyCertificate
	if err := json.Unmarshal(manifest, &parsed); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	parsed.ServerSignature = ""
	rebuilt, err := json.Marshal(&parsed)
	if err != nil {
		t.Fatalf("re-marshal manifest: %v", err)
	}
	if !cry.verifySignature(cry.signPK, rebuilt, sigBytes) {
		t.Fatal("server signature does not verify against the canonical manifest bytes")
	}

	// Tampering must invalidate the signature.
	forged := append([]byte{}, rebuilt...)
	forged[len(forged)/2] ^= 0xFF
	if cry.verifySignature(cry.signPK, forged, sigBytes) {
		t.Fatal("signature must not verify over altered manifest bytes")
	}
}

// TestBuildDownloadZip assembles a real export package and verifies its
// structure: signed manifest + PDF certificate + one entry per version, with a
// full archive re-read (the same guard the execute handler performs).
func TestBuildDownloadZip(t *testing.T) {
	cry, err := newServerCrypto(&Config{})
	if err != nil {
		t.Fatalf("newServerCrypto: %v", err)
	}
	reqAt, _ := time.Parse(time.RFC3339, "2026-09-05T10:00:00Z")
	expAt, _ := time.Parse(time.RFC3339, "2026-09-07T10:00:00Z")
	dlAt, _ := time.Parse(time.RFC3339, "2026-09-05T10:05:00Z")
	req := &DownloadRequest{
		ID: "req-11111111-2222-3333-4444-555555555555", DocumentID: "doc-1",
		Reason: "export", RequestedByBadge: "IND-IO-402", RequiredApprovals: 2,
		RequestedAt: reqAt, ExpiresAt: expAt,
	}
	cert, manifest, err := buildCertificate(cry, req, requestRef{FIR: "FIR-2026-0089"},
		validChainRows(), nil, dlAt)
	if err != nil {
		t.Fatalf("buildCertificate: %v", err)
	}
	pdf, err := renderCertificatePDF(cert)
	if err != nil {
		t.Fatalf("renderCertificatePDF: %v", err)
	}
	if len(pdf) < 200 {
		t.Fatalf("pdf looks truncated: %d bytes", len(pdf))
	}

	zipBytes, err := buildDownloadZip(manifest, pdf, validChainRows())
	if err != nil {
		t.Fatalf("buildDownloadZip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("zip reopen failed (corrupt archive?): %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", f.Name, err)
		}
		n, err := io.Copy(io.Discard, rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read entry %s: %v", f.Name, err)
		}
		if n == 0 {
			t.Errorf("entry %s is empty", f.Name)
		}
		names[f.Name] = true
	}
	for _, want := range []string{"chain-of-custody.json", "chain-of-custody-certificate.pdf", "v1/v1-v1.bin", "v2/v2-v2.bin"} {
		if !names[want] {
			t.Errorf("zip is missing entry %q (got %v)", want, names)
		}
	}
}

// TestDownloadZipNames covers slug normalisation of package filenames.
func TestDownloadZipNames(t *testing.T) {
	fir := "FIR-2026/0089 (confidential)"
	if got := safeSlug(fir); got != "FIR-2026-0089--confidential-" {
		t.Errorf("safeSlug(%q) = %q", fir, got)
	}
	name := downloadZipName("FIR-2026-0089", "12345678-request")
	if !strings.HasPrefix(name, "qdesk-coc-FIR-2026-0089-") || !strings.HasSuffix(name, ".zip") {
		t.Errorf("downloadZipName produced unexpected name %q", name)
	}
}

// ---------------------------------------------------------------------------
// Integration test — full dual-approval workflow against a real database.
// Skipped unless the Supabase/Redis/Storage environment is provisioned.
// ---------------------------------------------------------------------------

// TestDownloadApprovalWorkflow drives the documented flow end to end:
//
//	IO requests → SUP1 approves (still pending) → SUP2 approves (approved) →
//	SUP1 tries to execute (403) → IO executes (downloaded, signed URL issued)
//	→ IO executes again (409 one-shot). Also checks self-approval is rejected.
//
// The test seeds its own users, case assignment, document and a valid chained
// version set, and removes them afterwards.
func TestDownloadApprovalWorkflow(t *testing.T) {
	if os.Getenv("SUPABASE_DATABASE_URL") == "" {
		t.Skip("SUPABASE_DATABASE_URL not set — integration test skipped")
	}
	if os.Getenv("UPSTASH_REDIS_URL") == "" {
		t.Skip("UPSTASH_REDIS_URL not set — integration test skipped")
	}
	if os.Getenv("SUPABASE_URL") == "" || os.Getenv("SUPABASE_SERVICE_ROLE_KEY") == "" {
		t.Skip("SUPABASE_URL / SUPABASE_SERVICE_ROLE_KEY not set — storage-backed steps skipped")
	}

	cfg := &Config{
		DatabaseURL:             os.Getenv("SUPABASE_DATABASE_URL"),
		RedisURL:                os.Getenv("UPSTASH_REDIS_URL"),
		SupabaseURL:             os.Getenv("SUPABASE_URL"),
		SupabaseServiceKey:      os.Getenv("SUPABASE_SERVICE_ROLE_KEY"),
		MaxUploadSizeMB:         50,
		TicketTTLSeconds:        600,
		DownloadRequestTTLHours: 48,
	}
	pool, err := newPgxPool(context.Background(), cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()
	cry, err := newServerCrypto(cfg)
	if err != nil {
		t.Fatalf("crypto: %v", err)
	}
	a := &app{
		cfg:      cfg,
		cry:      cry,
		st:       &store{db: pool},
		sessions: map[string]*session{"integration-session": {SessionID: "integration-session"}},
		stg:      newStorageClient(cfg.SupabaseURL, cfg.SupabaseServiceKey, cfg.MaxUploadSizeMB),
		malware:  unconfiguredScanner{},
	}

	ctx := context.Background()
	fir := "FIR-TEST-DL-0001"
	createdAt := time.Now().UTC().Format(time.RFC3339)

	// --- seed users + assignment + document + chained versions ---------------
	uidIO := ""
	for _, u := range []struct{ badge, name, rank string }{
		{"TEST-DL-IO", "Test Inspector", "INSPECTOR"},
		{"TEST-DL-SUP1", "Test Supervisor One", "SHO_SUPERVISOR"},
		{"TEST-DL-SUP2", "Test Supervisor Two", "SHO_SUPERVISOR"},
	} {
		var id string
		if err := pool.pool.QueryRow(ctx,
			`INSERT INTO users (badge_number, full_name, rank) VALUES ($1,$2,$3::lea_role) RETURNING id::text`,
			u.badge, u.name, u.rank).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", u.badge, err)
		}
		if u.badge == "TEST-DL-IO" {
			uidIO = id
		}
	}
	defer func() {
		_, _ = pool.pool.Exec(ctx, `DELETE FROM users WHERE badge_number IN ('TEST-DL-IO','TEST-DL-SUP1','TEST-DL-SUP2')`)
	}()
	if _, err := pool.pool.Exec(ctx, `INSERT INTO case_assignments (fir_number, user_id) VALUES ($1,$2)`, fir, uidIO); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}
	defer func() {
		_, _ = pool.pool.Exec(ctx, `DELETE FROM case_assignments WHERE fir_number = $1`, fir)
	}()

	var docID string
	if err := pool.pool.QueryRow(ctx,
		`INSERT INTO documents (fir_number, title, classification_level, created_by) VALUES ($1,$2,$3,$4) RETURNING id::text`,
		fir, "Integration evidence doc", "RESTRICTED", uidIO).Scan(&docID); err != nil {
		t.Fatalf("seed document: %v", err)
	}
	defer func() {
		_, _ = pool.pool.Exec(ctx, `DELETE FROM documents WHERE id = $1`, docID)
	}()

	v1 := mkChainRow(1, "v1", "TEST-DL-IO", "first evidence page", "", createdAt)
	docVersionRows := []downloadChainRow{
		v1,
		mkChainRow(2, "v2", "TEST-DL-IO", "second evidence page", v1.Sha256Hash, time.Now().UTC().Format(time.RFC3339)),
	}
	for _, r := range docVersionRows {
		_, err := pool.pool.Exec(ctx, `
			INSERT INTO document_versions
				(document_id, version_number, tree_path, sha256_hash, parent_sha256_hash, signature, content_payload, created_by, created_at)
			VALUES ($1,$2,$3::ltree,$4,NULLIF($5,''),$6,$7,$8,$9)`,
			docID, r.Version, r.TreePath, r.Sha256Hash, r.ParentHash, r.Signature, r.Content, uidIO, r.CreatedAt)
		if err != nil {
			t.Fatalf("seed version %d: %v", r.Version, err)
		}
	}

	// --- helpers -------------------------------------------------------------
	post := func(path, badge string, body map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		switch {
		case strings.HasSuffix(path, "/decide"):
			a.handleDownloadDecide(rr, req)
		case strings.HasSuffix(path, "/execute"):
			a.handleDownloadExecute(rr, req)
		default:
			a.handleDownloadRequest(rr, req)
		}
		return rr
	}

	// 1. IO requests a download.
	rr := post("/api/downloads/request", "TEST-DL-IO", map[string]any{
		"session_id": "integration-session", "badge_number": "TEST-DL-IO",
		"document_id": docID, "reason": "Referral to prosecutor",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("request: status %d body %s", rr.Code, rr.Body.String())
	}
	var created downloadRequestOut
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("request decode: %v", err)
	}
	reqID := created.Request.ID
	if created.Request.Status != "pending" {
		t.Fatalf("new request status = %s, want pending", created.Request.Status)
	}
	if created.Request.RequiredApprovals != 2 {
		t.Fatalf("required approvals = %d, want 2", created.Request.RequiredApprovals)
	}

	// 1b. The requester cannot approve their own request.
	rr = post("/api/downloads/"+reqID+"/decide", "TEST-DL-IO", map[string]any{
		"session_id": "integration-session", "badge_number": "TEST-DL-IO", "decision": "approved",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("self-approve: status %d body %s", rr.Code, rr.Body.String())
	}

	// 2. First approval — request stays pending.
	rr = post("/api/downloads/"+reqID+"/decide", "TEST-DL-SUP1", map[string]any{
		"session_id": "integration-session", "badge_number": "TEST-DL-SUP1", "decision": "approved",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("approve 1: status %d body %s", rr.Code, rr.Body.String())
	}
	var d1 decideOut
	_ = json.Unmarshal(rr.Body.Bytes(), &d1)
	if d1.Status != "pending" || d1.ApprovedCount != 1 {
		t.Fatalf("after first approval: status=%s approved=%d, want pending/1", d1.Status, d1.ApprovedCount)
	}

	// 2b. One official cannot decide twice.
	rr = post("/api/downloads/"+reqID+"/decide", "TEST-DL-SUP1", map[string]any{
		"session_id": "integration-session", "badge_number": "TEST-DL-SUP1", "decision": "rejected",
	})
	if rr.Code != http.StatusConflict {
		t.Fatalf("repeat decision: status %d body %s", rr.Code, rr.Body.String())
	}

	// 3. Second (distinct) approval unlocks the request.
	rr = post("/api/downloads/"+reqID+"/decide", "TEST-DL-SUP2", map[string]any{
		"session_id": "integration-session", "badge_number": "TEST-DL-SUP2", "decision": "approved",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("approve 2: status %d body %s", rr.Code, rr.Body.String())
	}
	var d2 decideOut
	_ = json.Unmarshal(rr.Body.Bytes(), &d2)
	if d2.Status != "approved" || d2.ApprovedCount != 2 {
		t.Fatalf("after second approval: status=%s approved=%d, want approved/2", d2.Status, d2.ApprovedCount)
	}

	// 4. A non-requester (supervisor) cannot execute.
	rr = post("/api/downloads/"+reqID+"/execute", "TEST-DL-SUP1", map[string]any{
		"session_id": "integration-session", "badge_number": "TEST-DL-SUP1",
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-requester execute: status %d body %s", rr.Code, rr.Body.String())
	}

	// 5. Requester executes — one-shot download with a signed package URL.
	rr = post("/api/downloads/"+reqID+"/execute", "TEST-DL-IO", map[string]any{
		"session_id": "integration-session", "badge_number": "TEST-DL-IO",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("execute: status %d body %s", rr.Code, rr.Body.String())
	}
	var ex executeOut
	if err := json.Unmarshal(rr.Body.Bytes(), &ex); err != nil {
		t.Fatalf("execute decode: %v", err)
	}
	if ex.URL == "" || ex.CertificateID == "" || ex.ChainValid != "valid" {
		t.Fatalf("execute response incomplete: %+v", ex)
	}
	if ex.ExpiresSeconds != downloadSignedURLTTL {
		t.Fatalf("execute ttl = %d, want %d", ex.ExpiresSeconds, downloadSignedURLTTL)
	}

	// 6. Second execute is refused — the one-shot transition already fired.
	rr = post("/api/downloads/"+reqID+"/execute", "TEST-DL-IO", map[string]any{
		"session_id": "integration-session", "badge_number": "TEST-DL-IO",
	})
	if rr.Code != http.StatusConflict {
		t.Fatalf("second execute: status %d body %s", rr.Code, rr.Body.String())
	}
}

// TestRandomIDs ensures the certificate/ticket id generator produces unique
// values (used by newSessionID and certificate IDs alike).
func TestRandomIDs(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id, err := newTicketID()
		if err != nil {
			t.Fatalf("newTicketID: %v", err)
		}
		if seen[id] {
			t.Fatalf("duplicate id generated: %s", id)
		}
		seen[id] = true
	}
}

// TestDownloadRoutesRegistered proves the five chain-of-custody endpoints are
// wired into the production mux (handlers never run, so no DB is needed). It
// guards the "404 page not found" class of breakage where a path is added
// client-side but never registered server-side.
func TestDownloadRoutesRegistered(t *testing.T) {
	var a app
	mux := http.NewServeMux()
	a.routes(mux)

	cases := []struct {
		method, path, pattern string
	}{
		{http.MethodPost, "/api/downloads/request", "/api/downloads/request"},
		{http.MethodGet, "/api/downloads/pending", "/api/downloads/pending"},
		{http.MethodGet, "/api/downloads", "/api/downloads"},
		{http.MethodPost, "/api/downloads/some-request-id/decide", "/api/downloads/{requestID}/decide"},
		{http.MethodPost, "/api/downloads/some-request-id/execute", "/api/downloads/{requestID}/execute"},
		{http.MethodGet, "/api/cases", "/api/cases"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		_, pattern := mux.Handler(req)
		if pattern != c.pattern {
			t.Errorf("route %s %s resolved to pattern %q, want %q", c.method, c.path, pattern, c.pattern)
		}
	}

	// A path under the same tree that was never registered must still 404 —
	// proving the wildcard/literal segments don't shadow one another.
	req := httptest.NewRequest(http.MethodGet, "/api/downloads/unknown-route", nil)
	if _, pattern := mux.Handler(req); pattern != "" {
		t.Errorf("unregistered /api/downloads/unknown-route matched pattern %q", pattern)
	}
}
