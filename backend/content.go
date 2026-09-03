package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

// GET /api/files/{version_id}/content
// ---------------------------------------------------------------------------
//
// Streams the evidence file bytes through the backend. The storage object is
// fetched server-side with the service key (the object lives in a private,
// non-public bucket) and written straight to the client. This endpoint is the
// real download source that GET /api/files/{version_id}/download-url points at;
// it exists because some Supabase hosts reject REST-signed URLs, while
// authenticated object reads are always reliable.
//
// Security mirrors handleDownloadURL: a live stream/all-scope ticket for the
// same FIR as the version is required, plus a valid session.

func (a *app) handleFileContent(w http.ResponseWriter, r *http.Request) {
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
		log.Printf("file content role lookup error: %v", err)
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

	src, err := a.stg.openObject(ctx, meta.StoragePath, r.Header.Get("Range"))
	if err != nil {
		log.Printf("file content read error: %v", err)
		http.Error(w, "evidence storage unavailable", http.StatusInternalServerError)
		return
	}
	defer src.Body.Close()

	w.Header().Set("Content-Type", meta.MimeType)
	w.Header().Set("Cache-Control", "private, no-store, no-transform")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Inline display only: tell browsers and proxies not to prompt a download,
	// and forbid framing the raw object anywhere but the Q-DESK origins.
	w.Header().Set("Content-Disposition", "inline; filename="+url.PathEscape(meta.OriginalFilename))
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; frame-ancestors http://localhost:3000 http://tauri.localhost tauri://localhost")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive, nosnippet")
	for _, h := range []string{"Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := src.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(src.StatusCode)
	io.Copy(w, src.Body)
}