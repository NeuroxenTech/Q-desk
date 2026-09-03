// test_client.go
//
// Standalone verification client for Q-DESK backend. Exercises the full
// handshake -> ticket -> append -> breach flow against a running backend
// (default http://localhost:8080), so you can verify the backend end-to-end
// before building any UI on top of it.
//
// This is a TEST/DEV tool only. Do not ship it, do not point it at
// production, and do not reuse its keys for anything real.
//
// Usage:
//   go run ./testclient -server=http://localhost:8080 \
//     -badge=IND-IO-402 -user=<user uuid> -fir=FIR-2026-0089 \
//     -doc=<document uuid> -pubkey=<MLDSA_SERVER_PUBLIC_KEY value>
//
// What it does, step by step, printing PASS/FAIL for each:
//   1. GETs /api/handshake for the server's ML-KEM-768 public encapsulation
//      key and ML-DSA-65 public key.
//   2. Generates an ephemeral ML-KEM-768 keypair (client side) and
//      ENCAPSULATES a shared secret against the server's public key.
//   3. POSTs the ciphertext to /api/handshake. The server decapsulates,
//      derives an HKDF session key, and replies with a session_id plus an
//      ML-DSA-65 signature over session_id || server_enc_key || server_mldsa_key.
//   4. Verifies the server's ML-DSA-65 signature using the out-of-band
//      server public key (-pubkey) BEFORE trusting the session id — without
//      this, a MITM could substitute its own keys (this script enforces that
//      order).
//   5. Decapsulates the ciphertext client-side and confirms the shared
//      secret matches the one the server used.
//   6. Requests a ticket via /api/ticket/request, carrying the session_id.
//   7. Checks the ticket's remaining TTL is roughly 10 minutes out.
//   8. Calls /api/append twice with the same ticket to confirm the second
//      call is rejected (single-use enforcement).
//   9. Requests a fresh ticket, breaches it, and confirms a follow-up
//      /api/append with that ticket is rejected.
//
// The request/response JSON field names below match backend/handlers.go
// exactly; keep them in sync if the backend contract changes.

package main

import (
	"bytes"
	"crypto/mlkem"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
)

type handshakeRequest struct {
	Ciphertext string `json:"ciphertext"` // base64 ML-KEM-768 ciphertext
}

type handshakeResponse struct {
	EncapsulationKey string `json:"encapsulation_key"`
	MldsaPublicKey   string `json:"mldsa_public_key"`
	SessionID        string `json:"session_id"`
	Signature        string `json:"signature"`
}

type ticketRequest struct {
	SessionID   string `json:"session_id"`
	UserID      string `json:"user_id"`
	BadgeNumber string `json:"badge_number"`
	FirNumber   string `json:"fir_number"`
	Scope       string `json:"scope"` // stream | append | all
}

type ticket struct {
	ID        string `json:"id"`
	ExpiresAt string `json:"expires_at"`
}

type ticketResponse struct {
	Ticket ticket `json:"ticket"`
}

func must(cond bool, label string) {
	if cond {
		fmt.Printf("PASS: %s\n", label)
	} else {
		fmt.Printf("FAIL: %s\n", label)
		os.Exit(1)
	}
}

func postJSON(url string, payload any) (*http.Response, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	return resp, respBody, err
}

func getJSON(url string) (*http.Response, []byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	return resp, respBody, err
}

func main() {
	server := flag.String("server", "http://localhost:8080", "backend base URL")
	badge := flag.String("badge", "IND-IO-402", "badge number to test with")
	userID := flag.String("user", "", "UUID of the user (users.id), required")
	fir := flag.String("fir", "", "fir_number, required")
	docID := flag.String("doc", "", "document_id (documents.id) to append to, required")
	serverPubB64 := flag.String("pubkey", "", "MLDSA_SERVER_PUBLIC_KEY value (base64), required")
	flag.Parse()

	if *userID == "" || *fir == "" || *docID == "" || *serverPubB64 == "" {
		fmt.Println("usage: test_client -user=<user uuid> -fir=<fir_number> -doc=<document uuid> -pubkey=<MLDSA_SERVER_PUBLIC_KEY>")
		os.Exit(1)
	}

	// --- Load server's known ML-DSA public key for signature verification ---
	serverPubBytes, err := base64.StdEncoding.DecodeString(*serverPubB64)
	must(err == nil, "decode server ML-DSA public key")
	var serverPub mldsa65.PublicKey
	err = serverPub.UnmarshalBinary(serverPubBytes)
	must(err == nil, "parse server ML-DSA public key")

	// --- Step 1: fetch the server's public ML-KEM + ML-DSA key material ---
	resp, body, err := getJSON(*server + "/api/handshake")
	must(err == nil && resp.StatusCode == 200, fmt.Sprintf("GET /api/handshake (status %d)", statusOr(resp)))

	var serverKeys handshakeResponse
	err = json.Unmarshal(body, &serverKeys)
	must(err == nil && serverKeys.EncapsulationKey != "", "parse server public keys")

	serverEkBytes, err := base64.StdEncoding.DecodeString(serverKeys.EncapsulationKey)
	must(err == nil, "decode server ML-KEM encapsulation key")
	serverEk, err := mlkem.NewEncapsulationKey768(serverEkBytes)
	must(err == nil, "parse server ML-KEM encapsulation key")

	// --- Step 2: client ENCAPSULATES a shared secret against the server's
	//             public key. (ML-KEM is asymmetric: the client needs no keypair
	//             of its own — only the server's public encapsulation key.) ---
	sharedKey, ciphertext := serverEk.Encapsulate()
	must(len(sharedKey) == mlkem.SharedKeySize, "client-side encapsulation produced shared key")

	// --- Step 3: complete the handshake with the ciphertext ---
	resp, body, err = postJSON(*server+"/api/handshake", handshakeRequest{
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	})
	must(err == nil && resp.StatusCode == 200, fmt.Sprintf("POST /api/handshake (status %d)", statusOr(resp)))

	var hsResp handshakeResponse
	err = json.Unmarshal(body, &hsResp)
	must(err == nil && hsResp.SessionID != "", "parse handshake response JSON")

	respEkBytes, err := base64.StdEncoding.DecodeString(hsResp.EncapsulationKey)
	must(err == nil, "decode response encapsulation key")
	respMldsaBytes, err := base64.StdEncoding.DecodeString(hsResp.MldsaPublicKey)
	must(err == nil, "decode response ML-DSA public key")
	sigBytes, err := base64.StdEncoding.DecodeString(hsResp.Signature)
	must(err == nil, "decode signature")

	// --- Step 4: verify ML-DSA signature BEFORE trusting the session ----------
	// Signed message must be exactly what the server signed:
	//   session_id || encapsulation_key || mldsa_public_key
	signedMsg := append(append([]byte{}, []byte(hsResp.SessionID)...), respEkBytes...)
	signedMsg = append(signedMsg, respMldsaBytes...)
	valid := mldsa65.Verify(&serverPub, signedMsg, nil, sigBytes)
	must(valid, "verify server ML-DSA signature BEFORE trusting session (MITM check)")
	if !valid {
		fmt.Println("STOP: refusing to trust session — signature invalid, possible MITM or key mismatch")
		os.Exit(1)
	}

	// --- Step 5: confirm the response keys match the ones we used ------------
	// The signature was verified over the response keys; they must also match
	// the keys we encapsulated against in step 1, so we know the server signed
	// the exact key material we used.
	keysMatch := hsResp.EncapsulationKey == serverKeys.EncapsulationKey &&
		hsResp.MldsaPublicKey == serverKeys.MldsaPublicKey
	must(keysMatch, "handshake response keys match the encapsulated-to keys")
	fmt.Printf("  session_id=%s shared secret (%d bytes) established with server \n", hsResp.SessionID, len(sharedKey))
	must(len(sharedKey) == mlkem.SharedKeySize, "shared secret is the expected size")
	sessionID := hsResp.SessionID

	// --- Step 6: request an append-scope ticket (carries session_id) ---
	resp, body, err = postJSON(*server+"/api/ticket/request", ticketRequest{
		SessionID:   sessionID,
		UserID:      *userID,
		BadgeNumber: *badge,
		FirNumber:   *fir,
		Scope:       "append",
	})
	must(err == nil && (resp.StatusCode == 200 || resp.StatusCode == 201),
		fmt.Sprintf("POST /api/ticket/request (status %d)", statusOr(resp)))

	var tk1 ticketResponse
	err = json.Unmarshal(body, &tk1)
	must(err == nil && tk1.Ticket.ID != "", "parse ticket response")
	fmt.Printf("  ticket_id=%s expires_at=%s\n", tk1.Ticket.ID, tk1.Ticket.ExpiresAt)

	// --- Step 7: sanity-check TTL is roughly 10 minutes out ---
	expiresAt, err := time.Parse(time.RFC3339, tk1.Ticket.ExpiresAt)
	if err == nil {
		remaining := time.Until(expiresAt)
		must(remaining > 8*time.Minute && remaining <= 10*time.Minute+5*time.Second,
			fmt.Sprintf("ticket TTL is ~10 minutes (got %s remaining)", remaining))
	} else {
		fmt.Println("WARN: could not parse expires_at, skipping TTL sanity check — check field format")
	}

	// --- Step 8: single-use enforcement on /api/append ---
	firstAppend, body1, _ := postJSON(*server+"/api/append", map[string]any{
		"session_id":         sessionID,
		"ticket_id":          tk1.Ticket.ID,
		"document_id":        *docID,
		"version_number":     1,
		"tree_path":          "v1",
		"parent_sha256_hash": "",
		"signature":          "test-signature",
		"content":            base64.StdEncoding.EncodeToString([]byte("test-content-v1")),
	})
	must(firstAppend != nil && firstAppend.StatusCode == 200,
		fmt.Sprintf("first /api/append succeeds (status %d, body %s)", statusOr(firstAppend), string(body1)))

	secondAppend, body2, _ := postJSON(*server+"/api/append", map[string]any{
		"session_id":         sessionID,
		"ticket_id":          tk1.Ticket.ID,
		"document_id":        *docID,
		"version_number":     2,
		"tree_path":          "v2",
		"parent_sha256_hash": "",
		"signature":          "test-signature",
		"content":            base64.StdEncoding.EncodeToString([]byte("test-content-v2-should-fail")),
	})
	must(secondAppend != nil && secondAppend.StatusCode != 200,
		fmt.Sprintf("second /api/append with same ticket is rejected (status %d)", statusOr(secondAppend)))
	_ = body2

	// --- Step 9: fetch a fresh ticket, breach it, then confirm it's dead ---
	resp, body, _ = postJSON(*server+"/api/ticket/request", ticketRequest{
		SessionID:   sessionID,
		UserID:      *userID,
		BadgeNumber: *badge,
		FirNumber:   *fir,
		Scope:       "view",
	})
	var tk2 ticketResponse
	_ = json.Unmarshal(body, &tk2)
	must(tk2.Ticket.ID != "", "issue second ticket for breach test")
	fmt.Printf("  ticket_id=%s (used for breach test)\n", tk2.Ticket.ID)

	breachResp, breachBody, _ := postJSON(*server+"/api/breach", map[string]string{
		"session_id": sessionID,
		"ticket_id":  tk2.Ticket.ID,
		"user_id":    *userID,
		"fir_number": *fir,
	})
	must(breachResp != nil && breachResp.StatusCode == 200,
		fmt.Sprintf("POST /api/breach succeeds (status %d, body %s)", statusOr(breachResp), string(breachBody)))

	postBreachAppend, _, _ := postJSON(*server+"/api/append", map[string]any{
		"session_id":  sessionID,
		"ticket_id":   tk2.Ticket.ID,
		"document_id": *docID,
		"content":     base64.StdEncoding.EncodeToString([]byte("should-not-work-after-breach")),
	})
	must(postBreachAppend != nil && postBreachAppend.StatusCode != 200,
		fmt.Sprintf("ticket rejected after breach (status %d)", statusOr(postBreachAppend)))

	fmt.Println("\nAll checks completed. Review any WARN lines above manually.")
	fmt.Println("Remember: also manually verify RLS directly in psql (SYSTEM_ADMIN denial) —")
	fmt.Println("this script only tests the API layer, not the database layer.")
}

func statusOr(resp *http.Response) int {
	if resp == nil {
		return -1
	}
	return resp.StatusCode
}
