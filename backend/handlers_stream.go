package main

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// wsUpgrader provides a safe WebSocket upgrader that validates the Origin
// header against the configured allowlist before any upgrade is accepted.
type wsUpgrader struct {
	upgrader websocket.Upgrader
	allowed  func(origin string) bool
}

func newWSUpgrader(allowed func(origin string) bool) wsUpgrader {
	return wsUpgrader{
		upgrader: websocket.Upgrader{
			// WebSocket upgrades are NOT covered by the standard CORS
			// middleware, so we enforce the same strict origin allowlist
			// here explicitly. CheckOrigin receives the request's Origin
			// and must return true only for an exact allowlist match.
			CheckOrigin:     func(r *http.Request) bool { return allowed(r.Header.Get("Origin")) },
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
		allowed: allowed,
	}
}

// streamMessage is the framing used on the /api/stream websocket.
type streamMessage struct {
	Type     string         `json:"type"` // chunk | close
	TicketID string         `json:"ticket_id,omitempty"`
	Payload  string         `json:"payload,omitempty"`
	Meta     map[string]any `json:"meta,omitempty"`
}

// handleStream upgrades the connection to a WebSocket and relays chunks while
// the backing ticket remains live in Redis. On every chunk the ticket TTL is
// re-validated; if it has expired or vanished the socket is closed with
// close code 4008 (policy/expiry).
func (a *app) handleStream(w http.ResponseWriter, r *http.Request) {
	conn, err := a.up.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade error: %v", err)
		return
	}
	defer conn.Close()

	ctx := r.Context()

	// Expect an initial auth frame carrying a ticket id.
	_, first, err := conn.ReadMessage()
	if err != nil {
		closeWS(conn, websocket.ClosePolicyViolation, "missing auth")
		return
	}
	var auth streamMessage
	if err := json.Unmarshal(first, &auth); err != nil || auth.TicketID == "" {
		closeWS(conn, websocket.ClosePolicyViolation, "bad auth")
		return
	}

	// Load the ticket so we know which FIR to stream and under whose identity.
	t, err := a.st.getTicket(ctx, auth.TicketID)
	if err != nil {
		closeWS(conn, 4008, "ticket not live")
		return
	}
	if time.Now().UTC().After(t.ExpiresAt) {
		closeWS(conn, 4008, "ticket expired")
		return
	}
	if t.Scope != TicketScopeStream && t.Scope != TicketScopeAll {
		closeWS(conn, websocket.ClosePolicyViolation, "ticket lacks stream scope")
		return
	}

	// Push every persisted version for the ticket's FIR. The read runs under
	// the ticket holder's RLS identity, so an unassigned officer gets nothing.
	if role, rerr := a.st.userRole(ctx, t.UserID); rerr == nil {
		if versions, verr := a.st.versionsByFir(ctx, t.UserID, role, t.FirNumber); verr == nil {
			chunks := buildStreamChunks(versions)
			for _, out := range chunks {
				if err := conn.WriteMessage(websocket.TextMessage, out); err != nil {
					return
				}
			}
		} else {
			log.Printf("stream load error: %v", verr)
		}
	}
	closeOut, _ := json.Marshal(streamMessage{Type: "close", TicketID: auth.TicketID})
	_ = conn.WriteMessage(websocket.TextMessage, closeOut)

	for {
		// Re-validate the ticket TTL in Redis on EVERY chunk.
		remaining, exists, err := a.st.ticketStatus(ctx, auth.TicketID)
		if err != nil {
			closeWS(conn, websocket.CloseInternalServerErr, "redis error")
			return
		}
		if !exists || remaining <= 0 {
			// Ticket expired or consumed: close with 4008.
			closeWS(conn, 4008, "ticket expired")
			return
		}

		mt, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if mt != websocket.TextMessage {
			continue
		}
		var chunk streamMessage
		if err := json.Unmarshal(msg, &chunk); err != nil {
			continue
		}
		// Echo the decrypted-framed chunk back (relay semantics).
		out, _ := json.Marshal(streamMessage{Type: "chunk", Payload: chunk.Payload, TicketID: auth.TicketID})
		if err := conn.WriteMessage(websocket.TextMessage, out); err != nil {
			return
		}
	}
}

// closeWS writes a close frame with the given code and reason and closes.
func closeWS(conn *websocket.Conn, code int, reason string) {
	_ = conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	_ = conn.Close()
}

// buildStreamChunks serializes loaded versions into the same wire chunks the
// WebSocket stream emits, so the HTTP polling fallback (handleStreamVersions)
// returns byte-identical data for the viewer.
func buildStreamChunks(versions []streamedVersion) [][]byte {
	var out [][]byte
	for _, v := range versions {
		if len(v.ContentPayload) == 0 {
			continue
		}
		mime, _ := parsePayloadMeta(v.ContentPayload)
		meta := map[string]any{
			"version_id":  v.ID,
			"document_id": v.DocumentID,
			"version":     v.VersionNumber,
			"hash":        v.Sha256Hash,
			"node":        v.TreeNode,
			"mime_type":   mime,
			"author":      v.Badge,
			"time":        v.CreatedAt.UTC().Format(time.RFC3339),
		}
		b, _ := json.Marshal(streamMessage{
			Type:    "chunk",
			Payload: string(v.ContentPayload),
			Meta:    meta,
		})
		out = append(out, b)
	}
	return out
}

// handleStreamVersions is the HTTP polling fallback for clients whose
// WebView/browser cannot open a raw WebSocket (e.g. WebView2+localhost). It
// returns the exact same chunk payloads the stream would push, as a simple
// GET JSON list, guarded by the same session + view-scope ticket + FIR checks
// as /api/stream and /api/files/.../content.
func (a *app) handleStreamVersions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
		log.Printf("stream-versions role lookup error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	versions, err := a.st.versionsByFir(ctx, t.UserID, role, t.FirNumber)
	if err != nil {
		log.Printf("stream-versions load error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	chunks := buildStreamChunks(versions)
	out := make([]streamMessage, 0, len(chunks))
	for _, c := range chunks {
		var m streamMessage
		if json.Unmarshal(c, &m) == nil {
			out = append(out, m)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ticket_id": ticketID,
		"fir_number": t.FirNumber,
		"chunks":    out,
	})
}

// ---------------------------------------------------------------------------
// 4. /api/append
// ---------------------------------------------------------------------------

type appendRequest struct {
	SessionID     string `json:"session_id"`
	TicketID      string `json:"ticket_id"` // single-use append-scope ticket
	DocumentID    string `json:"document_id"`
	VersionNumber int    `json:"version_number"`
	TreePath      string `json:"tree_path"`
	ParentHash    string `json:"parent_sha256_hash"`
	Signature     string `json:"signature"`
	Content       string `json:"content"` // base64 payload
}

type appendResponse struct {
	Version  *DocumentVersion `json:"version"`
	Consumed bool             `json:"consumed"`
}

// handleAppend consumes a single-use append-scope ticket and writes a
// hash-chained document_versions row. The ticket is only consumed (DEL) after
// a successful version write.
func (a *app) handleAppend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req appendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !a.validSession(req.SessionID) {
		http.Error(w, "invalid session", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()

	// Fetch the ticket from Redis.
	t, err := a.st.getTicket(ctx, req.TicketID)
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

	payload, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		http.Error(w, "invalid content encoding", http.StatusBadRequest)
		return
	}

	// Build the hash-chained version identifier. The stored sha256_hash binds
	// the content digest, the parent hash, the authoring badge and the creation
	// timestamp:
	//   sha256_hash = SHA256(content_hash || parent_hash || badge || created_at)
	// GET /api/versions and the frontend audit view re-derive exactly this and
	// flag any row that does not match, making the chain tamper-evident.
	now := time.Now().UTC()
	contentHash := hashChain(payload)
	parentHash := req.ParentHash
	badge := t.BadgeNumber
	createdAt := now.Format(time.RFC3339)
	sha := hashChain([]byte(contentHash + parentHash + badge + createdAt))

	// Store DB write BEFORE consuming the single-use ticket so a failed write
	// leaves the ticket intact for retry.
	v := &DocumentVersion{
		DocumentID:       req.DocumentID,
		VersionNumber:    req.VersionNumber,
		TreePath:         req.TreePath,
		Sha256Hash:       sha,
		ParentSha256Hash: req.ParentHash,
		Signature:        req.Signature,
		ContentPayload:   payload,
		CreatedBy:        t.UserID,
		CreatedAt:        createdAt,
	}
	role, rerr := a.st.userRole(ctx, t.UserID)
	if rerr != nil {
		log.Printf("append role lookup error: %v", rerr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := a.st.insertDocumentVersion(ctx, t.UserID, role, v); err != nil {
		log.Printf("append write error: %v", err)
		http.Error(w, "version write failed", http.StatusInternalServerError)
		return
	}

	// Consume the single-use append ticket now that the write succeeded.
	consumed, err := a.st.consumeTicket(ctx, req.TicketID)
	if err != nil {
		log.Printf("consume ticket error: %v", err)
	}

	writeJSON(w, http.StatusOK, appendResponse{Version: v, Consumed: consumed})
}

// ---------------------------------------------------------------------------
// 5. /api/breach
// ---------------------------------------------------------------------------

type breachRequest struct {
	SessionID string `json:"session_id"`
	TicketID  string `json:"ticket_id"`
	UserID    string `json:"user_id"`
	FirNumber string `json:"fir_number"`
	Reason    string `json:"reason"`
}

// handleBreach revokes a ticket (DEL from Redis), appends a hash-chained
// audit_logs row, and revokes the active session.
func (a *app) handleBreach(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req breachRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := r.Context()

	// 1. DEL the Redis ticket (revoke).
	revoked, err := a.st.revokeTicket(ctx, req.TicketID)
	if err != nil {
		log.Printf("breach revoke error: %v", err)
	}

	// 2. Append a hash-chained audit_logs row. The frontend reports the badge
	// number as user_id; resolve it to the users.id UUID so RLS and the
	// actor_id FK agree with the rest of the system.
	uid := req.UserID
	if uid != "" {
		if resolved, rerr := a.st.userIDByBadge(ctx, req.UserID); rerr == nil {
			uid = resolved
		}
	}
	meta := map[string]any{"reason": req.Reason, "revoked": revoked}
	actorRole, _ := a.st.userRole(ctx, uid)
	if err := a.st.insertAuditLog(ctx, uid, actorRole, "BREACH", req.FirNumber, meta); err != nil {
		log.Printf("breach audit error: %v", err)
	}

	// 3. Revoke the session.
	if req.SessionID != "" {
		a.revokeSession(req.SessionID)
	}

	writeJSON(w, http.StatusOK, map[string]any{"revoked": revoked, "session_revoked": true})
}
