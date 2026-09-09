package main

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Chain-of-custody download requests — store layer.
//
// Every read/write for download_requests and download_approvals runs under the
// acting official's RLS identity (withUserTx), matching the rest of the
// backend. RLS policies scoping is defined in db/migrations/007_download_requests.sql.
// ---------------------------------------------------------------------------

// DownloadRequest mirrors a row of download_requests plus the joined fields the
// Downloads UI needs (document/case reference, requester name, live approval
// tallies).
type DownloadRequest struct {
	ID                string     `json:"id"`
	DocumentID        string     `json:"document_id"`
	Reason            string     `json:"reason"`
	RequestedByBadge  string     `json:"requested_by_badge"`
	Status            string     `json:"status"`
	RequiredApprovals int        `json:"required_approvals"`
	RequestedAt       time.Time  `json:"requested_at"`
	ExpiresAt         time.Time  `json:"expires_at"`
	DownloadedAt      *time.Time `json:"downloaded_at,omitempty"`

	// Joined / computed fields for the UI and the certificate.
	FIR           string `json:"fir_number,omitempty"`
	Title         string `json:"title,omitempty"`
	RequesterName string `json:"requester_name,omitempty"`
	ApprovedCount int    `json:"approved_count"`
	RejectedCount int    `json:"rejected_count"`
}

// DownloadApproval mirrors a row of download_approvals — a signed decision by
// one SHO_SUPERVISOR+ official on a download request.
type DownloadApproval struct {
	ID                string    `json:"id"`
	DownloadRequestID string    `json:"download_request_id"`
	ApprovedByBadge   string    `json:"approved_by_badge"`
	Decision          string    `json:"decision"`
	DecidedAt         time.Time `json:"decided_at"`
	Signature         string    `json:"signature"`

	// Joined field for the UI (rejection transparency to the requester).
	ApproverName string `json:"approver_name,omitempty"`
}

// downloadChainRow is one document version selected for the chain-of-custody
// export. ContentPayload is included (the requester is always an investigating
// officer, never SYSTEM_ADMIN).
type downloadChainRow struct {
	ID          string
	Version     int
	TreePath    string
	Sha256Hash  string
	ParentHash  string
	MergedFrom  string
	Signature   string
	Content     []byte
	Badge       string
	CreatedAt   time.Time
	AuthorName  string
	ContentType string
}

// requestRef is the document reference data carried on requests and shown in
// the certificate.
type requestRef struct {
	FIR            string
	Title          string
	Classification string
}

// downloadRequestColumns returns the shared column list for request queries
// (base fields + document/users joins + live approval tallies).
func downloadRequestColumns() string {
	return `
		dr.id::text, dr.document_id::text, dr.reason, dr.requested_by_badge,
		dr.status::text, dr.required_approvals, dr.requested_at, dr.expires_at,
		dr.downloaded_at,
		COALESCE(d.fir_number, ''), COALESCE(d.title, ''),
		COALESCE(u.full_name, ''),
		COALESCE((SELECT COUNT(*) FROM download_approvals a
		          WHERE a.download_request_id = dr.id AND a.decision = 'approved'), 0),
		COALESCE((SELECT COUNT(*) FROM download_approvals a
		          WHERE a.download_request_id = dr.id AND a.decision = 'rejected'), 0)`
}

// scanDownloadRequest fills a DownloadRequest from the columns produced by
// downloadRequestColumns().
func scanDownloadRequest(row pgx.Row) (*DownloadRequest, error) {
	var r DownloadRequest
	var downloaded *time.Time
	err := row.Scan(&r.ID, &r.DocumentID, &r.Reason, &r.RequestedByBadge,
		&r.Status, &r.RequiredApprovals, &r.RequestedAt, &r.ExpiresAt, &downloaded,
		&r.FIR, &r.Title, &r.RequesterName, &r.ApprovedCount, &r.RejectedCount)
	if err != nil {
		return nil, err
	}
	r.DownloadedAt = downloaded
	return &r, nil
}

// insertDownloadRequest creates a pending download request under the actor's
// RLS identity and returns the stored row (requested_at populated by the DB).
func (s *store) insertDownloadRequest(ctx context.Context, actorID, role, documentID, badge, reason string, requiredApprovals int, expiresAt time.Time) (*DownloadRequest, error) {
	var r DownloadRequest
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO download_requests
				(document_id, requested_by_badge, reason, required_approvals, expires_at)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id::text, requested_at, status::text`,
			documentID, badge, reason, requiredApprovals, expiresAt).
			Scan(&r.ID, &r.RequestedAt, &r.Status)
	})
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// downloadRequestByID loads one request (RLS-scoped) reported in the caller's
// identity, with joined document reference and approval tallies.
func (s *store) downloadRequestByID(ctx context.Context, actorID, role, id string) (*DownloadRequest, error) {
	var r *DownloadRequest
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT`+downloadRequestColumns()+`
			FROM download_requests dr
			JOIN documents d ON d.id = dr.document_id
			JOIN users u ON u.badge_number = dr.requested_by_badge
			WHERE dr.id = $1`, id)
		loaded, err := scanDownloadRequest(row)
		if err != nil {
			return err
		}
		r = loaded
		return nil
	})
	return r, err
}

// downloadRequestsForRequester loads every request made by the given badge
// (requester's own history, newest first) with tallies.
func (s *store) downloadRequestsForRequester(ctx context.Context, actorID, role, badge string) ([]DownloadRequest, error) {
	var out []DownloadRequest
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT`+downloadRequestColumns()+`
			FROM download_requests dr
			JOIN documents d ON d.id = dr.document_id
			JOIN users u ON u.badge_number = dr.requested_by_badge
			WHERE dr.requested_by_badge = $1
			ORDER BY dr.requested_at DESC, dr.created_at DESC`, badge)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanDownloadRequest(rows)
			if err != nil {
				return err
			}
			out = append(out, *r)
		}
		return rows.Err()
	})
	return out, err
}

// pendingDownloadRequests lists 'pending' requests the given approver badge has
// not yet decided on and which are still inside their approval window. It is
// RLS-scoped: an investigating officer sees their own requests only; a
// SHO_SUPERVISOR / SYSTEM_ADMIN sees all station requests (existing supervisor
// read semantics from schema.sql).
func (s *store) pendingDownloadRequests(ctx context.Context, actorID, role, approverBadge string) ([]DownloadRequest, error) {
	var out []DownloadRequest
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT`+downloadRequestColumns()+`
			FROM download_requests dr
			JOIN documents d ON d.id = dr.document_id
			JOIN users u ON u.badge_number = dr.requested_by_badge
			WHERE dr.status = 'pending'
			  AND dr.expires_at > now()
			  AND NOT EXISTS (
				  SELECT 1 FROM download_approvals a
				  WHERE a.download_request_id = dr.id
				    AND a.approved_by_badge = $1
			  )
			ORDER BY dr.requested_at ASC, dr.created_at ASC`, approverBadge)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanDownloadRequest(rows)
			if err != nil {
				return err
			}
			out = append(out, *r)
		}
		return rows.Err()
	})
	return out, err
}

// insertDownloadApproval records a signed decision under the approver's RLS
// identity. The unique (download_request_id, approved_by_badge) constraint
// makes a second decision by the same official impossible at the DB level.
func (s *store) insertDownloadApproval(ctx context.Context, actorID, role, requestID, badge, decision string, decidedAt time.Time, signature string) error {
	return s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO download_approvals
				(download_request_id, approved_by_badge, decision, decided_at, signature)
			VALUES ($1, $2, $3, $4, $5)`,
			requestID, badge, decision, decidedAt, signature)
		return err
	})
}

// downloadApprovalByBadge returns the decision a specific official already made
// on a request, or nil when they have not decided. errors.Is(err, pgx.ErrNoRows)
// is treated as "no decision yet".
func (s *store) downloadApprovalByBadge(ctx context.Context, actorID, role, requestID, badge string) (*DownloadApproval, error) {
	var a DownloadApproval
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id::text, download_request_id::text, approved_by_badge,
			       decision::text, decided_at, signature
			FROM download_approvals
			WHERE download_request_id = $1 AND approved_by_badge = $2`,
			requestID, badge).
			Scan(&a.ID, &a.DownloadRequestID, &a.ApprovedByBadge, &a.Decision, &a.DecidedAt, &a.Signature)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// downloadApprovalsForRequest returns every decision recorded on a request
// (newest first), with the approver's full name joined for display.
func (s *store) downloadApprovalsForRequest(ctx context.Context, actorID, role, requestID string) ([]DownloadApproval, error) {
	var out []DownloadApproval
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT a.id::text, a.download_request_id::text, a.approved_by_badge,
			       a.decision::text, a.decided_at, a.signature,
			       COALESCE(u.full_name, '')
			FROM download_approvals a
			JOIN users u ON u.badge_number = a.approved_by_badge
			WHERE a.download_request_id = $1
			ORDER BY a.decided_at DESC, a.id`, requestID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a DownloadApproval
			if err := rows.Scan(&a.ID, &a.DownloadRequestID, &a.ApprovedByBadge,
				&a.Decision, &a.DecidedAt, &a.Signature, &a.ApproverName); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

// countDownloadApprovals tallies the approved/rejected decisions on a request.
func (s *store) countDownloadApprovals(ctx context.Context, actorID, role, requestID string) (approved, rejected int, err error) {
	err = s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT
				COUNT(*) FILTER (WHERE decision = 'approved'),
				COUNT(*) FILTER (WHERE decision = 'rejected')
			FROM download_approvals
			WHERE download_request_id = $1`, requestID).Scan(&approved, &rejected)
	})
	return approved, rejected, err
}

// setDownloadRequestStatus transitions a request's status. When status is
// 'downloaded', downloadedAt must be non-nil and is persisted alongside.
func (s *store) setDownloadRequestStatus(ctx context.Context, actorID, role, id, status string, downloadedAt *time.Time) error {
	return s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		if status == "downloaded" && downloadedAt != nil {
			_, err := tx.Exec(ctx, `
				UPDATE download_requests
				SET status = $2, downloaded_at = $3
				WHERE id = $1`, id, status, *downloadedAt)
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE download_requests SET status = $2 WHERE id = $1`, id, status)
		return err
	})
}

// expirePendingDownloadRequests marks every 'pending' request whose approval
// window has closed as 'expired', returning the ids that were expired so the
// caller can emit the DOWNLOAD_EXPIRED audit rows. Idempotent.
func (s *store) expirePendingDownloadRequests(ctx context.Context, actorID, role string) ([]string, error) {
	var ids []string
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			UPDATE download_requests SET status = 'expired'
			WHERE status = 'pending' AND expires_at <= now()
			RETURNING id::text`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	return ids, err
}

// documentRefByID returns the FIR, title and classification of a document
// under the actor's RLS identity. Used when building the certificate and when
// validating that a requester can access the document.
func (s *store) documentRefByID(ctx context.Context, actorID, role, id string) (*requestRef, error) {
	var r requestRef
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT fir_number, title, classification_level FROM documents WHERE id = $1`, id).
			Scan(&r.FIR, &r.Title, &r.Classification)
	})
	return &r, err
}

// downloadChainByDocument selects the complete version set of a document
// (mainline + branches + merges) with the raw content payload and signature for
// the chain-of-custody manifest. Runs under the actor's RLS identity; the
// requester is always an investigating officer / supervisor, never SYSTEM_ADMIN.
func (s *store) downloadChainByDocument(ctx context.Context, actorID, role, documentID string) ([]downloadChainRow, error) {
	var out []downloadChainRow
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT dv.id::text, dv.version_number, dv.tree_path::text, dv.sha256_hash,
			       COALESCE(dv.parent_sha256_hash, ''), COALESCE(dv.merged_from_hash, ''),
			       dv.signature, dv.content_payload, u.badge_number, dv.created_at,
			       COALESCE(u.full_name, '')
			FROM document_versions dv
			JOIN documents d ON d.id = dv.document_id
			JOIN users u ON u.id = dv.created_by
			WHERE dv.document_id = $1
			ORDER BY dv.created_at ASC, dv.tree_path`, documentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r downloadChainRow
			if err := rows.Scan(&r.ID, &r.Version, &r.TreePath, &r.Sha256Hash,
				&r.ParentHash, &r.MergedFrom, &r.Signature, &r.Content, &r.Badge,
				&r.CreatedAt, &r.AuthorName); err != nil {
				return err
			}
			r.ContentType = classifyPayload(r.Content)
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// canAccessDocument reports whether an officer (non-SYSTEM_ADMIN) may view a
// document: either they hold a case_assignments row for the document's FIR
// (investigating officer) or they are SHO_SUPERVISOR with station-wide read
// access. Mirrors the SELECT RLS policies in schema.sql; the document itself is
// joined identity-scoped so a FIR the caller cannot see yields a false even if
// the FIR string were guessed.
func (s *store) canAccessDocument(ctx context.Context, actorID, role, fir string) (bool, error) {
	var ok bool
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM case_assignments ca
				WHERE ca.user_id = $1::uuid AND ca.fir_number = $2
			) OR $3::text IN ('SYSTEM_ADMIN', 'SHO_SUPERVISOR')`,
			actorID, fir, role).Scan(&ok)
	})
	return ok, err
}

// markDownloadExecuted performs the one-shot 'approved' → 'downloaded'
// transition atomically. It reports false (with no error) when the request is
// no longer in 'approved' state — i.e. another caller has already executed it —
// so a single request can never yield more than one download package.
func (s *store) markDownloadExecuted(ctx context.Context, actorID, role, id string, at time.Time) (bool, error) {
	var changed bool
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE download_requests
			SET status = 'downloaded', downloaded_at = $2
			WHERE id = $1 AND status = 'approved'`, id, at)
		if err != nil {
			return err
		}
		changed = tag.RowsAffected() == 1
		return nil
	})
	return changed, err
}
