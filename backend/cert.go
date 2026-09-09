package main

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

// ---------------------------------------------------------------------------
// Chain-of-custody certificate.
//
// The certificate is the machine-readable part of the export package: a single
// JSON document that (1) states the download request + both witness decisions,
// (2) lists every document version with its stored sha256 + content hash,
// integrity verdict and original signature, and (3) is itself signed by the
// server's ML-DSA-65 key.
//
// Signing & verification: the JSON manifest is the canonical byte stream —
// field order is fixed by Go's struct marshaling. The signature covers exactly
// `json.Marshal(cert)` with server_signature empty. A verifier unmarshals the
// manifest, zeroes server_signature, re-marshals with the same struct and
// checks the signature against the server public key from GET /api/handshake.
// ===========================================================================

// certificateApproval is one signed witness decision embedded in the
// certificate. DecidedAt is the exact RFC3339 string that was signed, so a
// verifier can recompute the per-approval digest.
type certificateApproval struct {
	ApprovedByBadge string `json:"approved_by_badge"`
	ApproverName    string `json:"approver_name,omitempty"`
	Decision        string `json:"decision"`
	DecidedAt       string `json:"decided_at"`
	Signature       string `json:"signature"` // server ML-DSA-65 over request_id|decision|decided_at
}

// certificateVersion is one document version in the export. ContentHash is the
// SHA-256 of the raw content payload bundled in the ZIP. Signature is the
// version's own chain signature recorded at append time.
type certificateVersion struct {
	Version     int    `json:"version_number"`
	TreePath    string `json:"tree_path"`
	Kind        string `json:"kind"`
	Sha256Hash  string `json:"sha256_hash"`
	ParentHash  string `json:"parent_sha256_hash,omitempty"`
	MergedFrom  string `json:"merged_from_hash,omitempty"`
	Badge       string `json:"badge_number"`
	AuthorName  string `json:"author_name,omitempty"`
	CreatedAt   string `json:"created_at"`
	ContentType string `json:"content_type"`
	Filename    string `json:"filename,omitempty"`
	ContentHash string `json:"content_hash"`
	ChainValid  string `json:"chain_valid"`
	Signature   string `json:"signature"`
}

// certificateDocument identifies the case/document the export covers.
type certificateDocument struct {
	DocumentID     string `json:"document_id"`
	FirNumber      string `json:"fir_number"`
	Title          string `json:"title"`
	Classification string `json:"classification_level"`
}

// chainOfCustodyCertificate is the signed manifest. ServerSignature is the
// ML-DSA-65 signature over json.Marshal of this document WITH it empty.
type chainOfCustodyCertificate struct {
	SchemaVersion      string                `json:"schema_version"`
	CertificateID      string                `json:"certificate_id"`
	GeneratedAt        string                `json:"generated_at"`
	DownloadRequestID  string                `json:"download_request_id"`
	RequiredApprovals  int                   `json:"required_approvals"`
	RequestedByBadge   string                `json:"requested_by_badge"`
	RequestedByName    string                `json:"requested_by_name,omitempty"`
	Reason             string                `json:"reason"`
	RequestedAt        string                `json:"requested_at"`
	ExpiresAt          string                `json:"expires_at"`
	DownloadedAt       string                `json:"downloaded_at,omitempty"`
	Document           certificateDocument   `json:"document"`
	Approvals          []certificateApproval `json:"approvals"`
	Chain              []certificateVersion  `json:"chain"`
	ChainValid         string                `json:"chain_valid"`
	ServerPublicKeyB64 string                `json:"server_ml_dsa_public_key"`
	ServerSignature    string                `json:"server_signature"`
}

// certificateSchemaVersion is bumped when the manifest shape changes.
const certificateSchemaVersion = "1"

// buildCertificate assembles the manifest, signs the canonical bytes with the
// server ML-DSA-65 key and returns the finished certificate plus the final
// marshaled manifest bytes (the exact document stored in the ZIP).
func buildCertificate(cry *serverCrypto, req *DownloadRequest, ref requestRef, chain []downloadChainRow, approvals []DownloadApproval, downloadedAt time.Time) (*chainOfCustodyCertificate, []byte, error) {
	certID, err := newTicketID()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()

	pub := ""
	if cry != nil {
		pub = base64.StdEncoding.EncodeToString(cry.mldsaPublicKey())
	}
	chainValid, _ := verifyChainedVersions(chain)

	approvalsOut := make([]certificateApproval, 0, len(approvals))
	for _, ap := range approvals {
		approvalsOut = append(approvalsOut, certificateApproval{
			ApprovedByBadge: ap.ApprovedByBadge,
			ApproverName:    ap.ApproverName,
			Decision:        ap.Decision,
			DecidedAt:       ap.DecidedAt.UTC().Format(time.RFC3339),
			Signature:       ap.Signature,
		})
	}

	chainOut := make([]certificateVersion, 0, len(chain))
	for i := range chain {
		r := &chain[i]
		contentHash := hashChain(r.Content)
		createdAt := r.CreatedAt.UTC().Format(time.RFC3339)
		verdict := verifyChain(r.Sha256Hash, contentHash, r.ParentHash, r.Badge, createdAt)
		kind := classifyVersionKind(r.TreePath, r.MergedFrom)
		_, filename := parsePayloadMeta(r.Content)
		if filename == "" {
			filename = downloadVersionFileName(r)
		}
		chainOut = append(chainOut, certificateVersion{
			Version:     r.Version,
			TreePath:    r.TreePath,
			Kind:        kind,
			Sha256Hash:  r.Sha256Hash,
			ParentHash:  r.ParentHash,
			MergedFrom:  r.MergedFrom,
			Badge:       r.Badge,
			AuthorName:  r.AuthorName,
			CreatedAt:   createdAt,
			ContentType: r.ContentType,
			Filename:    filename,
			ContentHash: contentHash,
			ChainValid:  verdict,
			Signature:   r.Signature,
		})
	}

	cert := &chainOfCustodyCertificate{
		SchemaVersion:     certificateSchemaVersion,
		CertificateID:     certID,
		GeneratedAt:       now.UTC().Format(time.RFC3339),
		DownloadRequestID: req.ID,
		RequiredApprovals: req.RequiredApprovals,
		RequestedByBadge:  req.RequestedByBadge,
		RequestedByName:   req.RequesterName,
		Reason:            req.Reason,
		RequestedAt:       req.RequestedAt.UTC().Format(time.RFC3339),
		ExpiresAt:         req.ExpiresAt.UTC().Format(time.RFC3339),
		DownloadedAt:      downloadedAt.UTC().Format(time.RFC3339),
		Document: certificateDocument{
			DocumentID:     req.DocumentID,
			FirNumber:      ref.FIR,
			Title:          ref.Title,
			Classification: ref.Classification,
		},
		Approvals:          approvalsOut,
		Chain:              chainOut,
		ChainValid:         chainValid,
		ServerPublicKeyB64: pub,
		ServerSignature:    "",
	}

	// Canonical bytes to sign: the manifest with server_signature empty.
	toSign, err := json.Marshal(cert)
	if err != nil {
		return nil, nil, err
	}
	if cry != nil {
		sig, err := cry.sign(toSign)
		if err != nil {
			return nil, nil, err
		}
		cert.ServerSignature = base64.StdEncoding.EncodeToString(sig)
	}
	final, err := json.Marshal(cert)
	if err != nil {
		return nil, nil, err
	}
	return cert, final, nil
}
