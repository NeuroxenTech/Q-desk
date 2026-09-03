package main

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// app holds the long-lived dependencies shared across handlers.
type app struct {
	cfg *Config
	cry *serverCrypto
	st  *store
	up  wsUpgrader

	// Evidence-file storage + scanning. stg is the Supabase Storage client;
	// malware is the scanning backend used when MALWARE_SCAN_ENABLED=true
	// (placeholder scanner otherwise).
	stg     *storageClient
	malware malwareScanner

	mu       sync.Mutex
	sessions map[string]*session // session_id -> active session
}

// session tracks a live WebSocket/append session derived from a handshake.
type session struct {
	SessionID string
	UserID    string
	FirNumber string
	Key       []byte // 32-byte HKDF session key
	CreatedAt time.Time
}

// ---------------------------------------------------------------------------
// 1. /api/handshake
// ---------------------------------------------------------------------------

type handshakeRequest struct {
	Ciphertext string `json:"ciphertext"` // base64 ML-KEM-768 ciphertext
	ClientPub  string `json:"client_pub"` // optional base64 client ML-KEM public key
}

type handshakeResponse struct {
	EncapsulationKey string `json:"encapsulation_key"`
	MldsaPublicKey   string `json:"mldsa_public_key"`
	SessionID        string `json:"session_id"`
	Signature        string `json:"signature"`
}

// handleHandshake serves the server public key (GET) or performs the KEM
// exchange (POST): decapsulates the client ciphertext with ML-KEM-768, derives
// an HKDF session key, and returns an ML-DSA-65 signed response.
func (a *app) handleHandshake(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.serveEncapsulationKey(w, r)
		return
	case http.MethodPost:
		a.performHandshake(w, r)
		return
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// serveEncapsulationKey returns the server's public ML-KEM + ML-DSA key material.
func (a *app) serveEncapsulationKey(w http.ResponseWriter, r *http.Request) {
	resp := handshakeResponse{
		EncapsulationKey: base64.StdEncoding.EncodeToString(a.cry.encapsulationKey()),
		MldsaPublicKey:   base64.StdEncoding.EncodeToString(a.cry.mldsaPublicKey()),
	}
	writeJSON(w, http.StatusOK, resp)
}

// performHandshake executes the ML-KEM decapsulation + HKDF + ML-DSA sign flow.
func (a *app) performHandshake(w http.ResponseWriter, r *http.Request) {
	var req handshakeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ct, err := base64.StdEncoding.DecodeString(req.Ciphertext)
	if err != nil {
		http.Error(w, "invalid ciphertext encoding", http.StatusBadRequest)
		return
	}
	sharedKey, err := a.cry.decapsulate(ct)
	if err != nil {
		http.Error(w, "decapsulation failed", http.StatusBadRequest)
		return
	}

	// HKDF session key derived from the shared secret and public context.
	ctxBytes := append(a.cry.encapsulationKey(), a.cry.mldsaPublicKey()...)
	sessionKey, err := a.cry.sessionKey(sharedKey, ctxBytes)
	if err != nil {
		http.Error(w, "session key derivation failed", http.StatusInternalServerError)
		return
	}

	sid := newSessionID()
	s := &session{SessionID: sid, Key: sessionKey, CreatedAt: time.Now().UTC()}
	a.mu.Lock()
	a.sessions[sid] = s
	a.mu.Unlock()

	// ML-DSA-65 signed response over the session id and server public keys.
	toSign := append([]byte(sid), a.cry.encapsulationKey()...)
	toSign = append(toSign, a.cry.mldsaPublicKey()...)
	sig, err := a.cry.sign(toSign)
	if err != nil {
		http.Error(w, "signing failed", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, handshakeResponse{
		EncapsulationKey: base64.StdEncoding.EncodeToString(a.cry.encapsulationKey()),
		MldsaPublicKey:   base64.StdEncoding.EncodeToString(a.cry.mldsaPublicKey()),
		SessionID:        sid,
		Signature:        base64.StdEncoding.EncodeToString(sig),
	})
}

// ---------------------------------------------------------------------------
// 2. /api/ticket/request
// ---------------------------------------------------------------------------

type ticketRequest struct {
	SessionID   string `json:"session_id"`
	UserID      string `json:"user_id"`
	BadgeNumber string `json:"badge_number"`
	FirNumber   string `json:"fir_number"`
	Scope       string `json:"scope"` // stream | append | all
}

type ticketResponse struct {
	Ticket *Ticket `json:"ticket"`
}

// handleTicketRequest validates RBAC + case_assignments, mints a signed
// ticket, stores it in Redis with an NX TTL, and mirrors it to tickets_issued.
func (a *app) handleTicketRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req ticketRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Re-validate the session attached to the request.
	if !a.validSession(req.SessionID) {
		http.Error(w, "invalid or expired session", http.StatusUnauthorized)
		return
	}

	// The frontend identifies itself by badge number, but the rest of the
	// system keys users and case assignments on the users.id UUID. Resolve the
	// badge to its UUID before doing anything else.
	uid, err := a.st.userIDByBadge(r.Context(), req.BadgeNumber)
	if err != nil {
		http.Error(w, "invalid badge", http.StatusUnauthorized)
		return
	}

	// Validate RBAC + case assignment in the database. The server operates with
	// SYSTEM_ADMIN identity here so it can inspect any officer's assignment.
	ok, rank, err := a.st.validateAssignment(r.Context(), uid, "SYSTEM_ADMIN", uid, req.FirNumber)
	if err != nil {
		log.Printf("assignment validation error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "no case assignment for this fir", http.StatusForbidden)
		return
	}
	if rank == "SYSTEM_ADMIN" {
		// SYSTEM_ADMIN is not permitted to open content access tickets.
		http.Error(w, "system admin may not receive content access tickets", http.StatusForbidden)
		return
	}

	tid, err := newTicketID()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	ttl := time.Duration(a.cfg.TicketTTLSeconds) * time.Second
	t := &Ticket{
		ID:          tid,
		FirNumber:   req.FirNumber,
		UserID:      uid,
		BadgeNumber: req.BadgeNumber,
		Scope:       TicketScope(req.Scope),
		IssuedAt:    now,
		ExpiresAt:   now.Add(ttl),
		Consumed:    false,
	}
	// Sign the canonical ticket payload with ML-DSA-65.
	sig, err := a.cry.sign(t.canonical())
	if err != nil {
		http.Error(w, "ticket signing failed", http.StatusInternalServerError)
		return
	}
	t.Signature = base64.StdEncoding.EncodeToString(sig)

	// SET ticket:<id> ... EX 600 NX in Redis (do not overwrite live ticket).
	set, err := a.st.setTicket(r.Context(), t, ttl)
	if err != nil {
		log.Printf("redis set ticket error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !set {
		http.Error(w, "ticket collision, retry", http.StatusConflict)
		return
	}

	// Mirror into tickets_issued.
	if err := a.st.insertTicket(r.Context(), uid, rank, t); err != nil {
		log.Printf("mirror ticket error: %v", err)
	}

	writeJSON(w, http.StatusCreated, ticketResponse{Ticket: t})
}

// ---------------------------------------------------------------------------
// shared validation helpers
// ---------------------------------------------------------------------------

// validSession reports whether a session id is currently active.
func (a *app) validSession(sid string) bool {
	if sid == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.sessions[sid]
	return ok
}

// revokeSession removes an active session (used on breach).
func (a *app) revokeSession(sid string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, sid)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func newSessionID() string {
	id, err := newTicketID()
	if err != nil {
		return base64.StdEncoding.EncodeToString([]byte(time.Now().String()))
	}
	return "sess-" + id
}
