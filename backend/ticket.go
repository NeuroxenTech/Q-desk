package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// TicketScope indicates what a ticket authorises its holder to do.
type TicketScope string

const (
	TicketScopeStream TicketScope = "stream"
	TicketScopeAppend TicketScope = "append"
	TicketScopeAll    TicketScope = "all"
)

// Ticket is a signed, time-limited access token minted for a specific user,
// FIR and scope. Bodies are stored both in Redis (for TTL checks) and mirrored
// into the tickets_issued table.
type Ticket struct {
	ID          string      `json:"id"`
	FirNumber   string      `json:"fir_number"`
	UserID      string      `json:"user_id"`
	BadgeNumber string      `json:"badge_number"`
	Scope       TicketScope `json:"scope"`
	IssuedAt    time.Time   `json:"issued_at"`
	ExpiresAt   time.Time   `json:"expires_at"`
	Signature   string      `json:"signature"` // ML-DSA-65 over canonical payload
	Consumed    bool        `json:"consumed"`
}

// newTicketID returns a cryptographically random ticket identifier.
func newTicketID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// canonical returns the byte payload that is signed/verified.
func (t *Ticket) canonical() []byte {
	p := fmt.Sprintf("%s|%s|%s|%s|%s|%d", t.ID, t.FirNumber, t.UserID, t.BadgeNumber, t.Scope, t.ExpiresAt.Unix())
	return []byte(p)
}

func (t *Ticket) marshal() ([]byte, error) {
	return json.Marshal(t)
}

func decodeTicket(b []byte) (*Ticket, error) {
	var t Ticket
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}
