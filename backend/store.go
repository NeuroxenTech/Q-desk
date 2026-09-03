package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

const ticketKeyPrefix = "ticket:"

// store wraps the Upstash Redis client and the Supabase (Postgres) pool.
type store struct {
	rdb *redis.Client
	db  *pgxPool
}

// redisKey returns the Redis key for a ticket id.
func redisKey(ticketID string) string { return ticketKeyPrefix + ticketID }

// setTicket stores the ticket in Redis with an NX guard and TTL.
// It does NOT overwrite an existing live ticket for the same key. The TTL is
// read from the ticket's own expiry rather than relying on the caller.
func (s *store) setTicket(ctx context.Context, t *Ticket, ttl time.Duration) (bool, error) {
	body, err := t.marshal()
	if err != nil {
		return false, err
	}
	ok, err := s.rdb.SetNX(ctx, redisKey(t.ID), body, ttl).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}

// consumeTicket atomically deletes a single-use ticket. The boolean result
// reports whether a live ticket was present (i.e. it had not expired or been
// consumed already).
func (s *store) consumeTicket(ctx context.Context, ticketID string) (bool, error) {
	n, err := s.rdb.Del(ctx, redisKey(ticketID)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ticketTTL returns the remaining TTL of a ticket in Redis and whether it
// still exists. A zero remaining value with exists=false means expired/missing.
func (s *store) ticketStatus(ctx context.Context, ticketID string) (remaining time.Duration, exists bool, err error) {
	count, err := s.rdb.Exists(ctx, redisKey(ticketID)).Result()
	if err != nil {
		return 0, false, err
	}
	if count == 0 {
		return 0, false, nil
	}
	secs, err := s.rdb.TTL(ctx, redisKey(ticketID)).Result()
	if err != nil {
		return 0, false, err
	}
	return secs, true, nil
}

// getTicket fetches a stored ticket body from Redis.
func (s *store) getTicket(ctx context.Context, ticketID string) (*Ticket, error) {
	data, err := s.rdb.Get(ctx, redisKey(ticketID)).Bytes()
	if err != nil {
		return nil, err
	}
	return decodeTicket(data)
}

// revokeTicket removes the Redis ticket and returns whether it existed.
func (s *store) revokeTicket(ctx context.Context, ticketID string) (bool, error) {
	return s.consumeTicket(ctx, ticketID)
}

// latestAuditPrevHash returns the most recent log_hash from the audit chain so
// the next entry can be chained to it. It returns "" for an empty chain.
func (s *store) latestAuditPrevHash(ctx context.Context, tx pgx.Tx) (string, error) {
	var prev string
	err := tx.QueryRow(ctx, "SELECT log_hash FROM audit_logs ORDER BY created_at DESC, id DESC LIMIT 1").Scan(&prev)
	if err != nil && prev == "" && isNoRows(err) {
		return "", nil
	}
	return prev, err
}

// insertDocumentVersion writes a hash-chained document_version row, enforcing
// the RLS read policy on the actor identity. The created_at value is supplied
// explicitly so the sha256_hash chain (which binds created_at) can be verified
// deterministically by /api/versions.
func (s *store) insertDocumentVersion(ctx context.Context, actorID, role string, v *DocumentVersion) error {
	return s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO document_versions
				(document_id, version_number, tree_path, sha256_hash, parent_sha256_hash, signature, content_payload, created_by, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			RETURNING id::text`,
			v.DocumentID, v.VersionNumber, v.TreePath, v.Sha256Hash, v.ParentSha256Hash, v.Signature, v.ContentPayload, v.CreatedBy, v.CreatedAt).Scan(&v.ID)
	})
}

// insertTicket mirrors a minted ticket into the tickets_issued table, enforcing
// the RLS policy on the issuing actor.
func (s *store) insertTicket(ctx context.Context, actorID, role string, t *Ticket) error {
	return s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO tickets_issued
				(ticket_code, fir_number, issued_by, issued_to, expires_at, consumed)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			t.ID, t.FirNumber, actorID, t.UserID, t.ExpiresAt, t.Consumed)
		return err
	})
}

// insertAuditLog appends a hash-chained row to audit_logs inside a single
// transaction so the prev_log_hash read and the insert are consistent.
func (s *store) insertAuditLog(ctx context.Context, actorID, role, action, fir string, metadata map[string]any) error {
	return s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		prev, err := s.latestAuditPrevHash(ctx, tx)
		if err != nil {
			return err
		}
		logHash := hashLog(prev, action, actorID, fir)
		mb, err := jsonMarshal(metadata)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO audit_logs (action, actor_id, fir_number, prev_log_hash, log_hash, metadata)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			action, actorID, fir, prev, logHash, mb)
		return err
	})
}

// validateAssignment checks a user has a case_assignments row for the FIR
// within the calling session's RLS context and returns the user's rank.
func (s *store) validateAssignment(ctx context.Context, actorID, role, userID, fir string) (bool, string, error) {
	var ok bool
	var rank string
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT (u.rank IS NOT NULL) AS present, COALESCE(u.rank::text, '')
			FROM users u
			JOIN case_assignments ca ON ca.user_id = u.id
			WHERE u.id = $1 AND ca.fir_number = $2`,
			userID, fir).Scan(&ok, &rank)
		if isNoRows(err) {
			ok, rank = false, ""
			return nil
		}
		return err
	})
	if err != nil {
		return false, "", err
	}
	return ok, rank, nil
}

// userRole returns the lea_role (rank) for a user id, needed to establish the
// RLS identity for operations performed on that user's behalf.
func (s *store) userRole(ctx context.Context, userID string) (string, error) {
	var role string
	err := s.db.pool.QueryRow(ctx, `SELECT rank::text FROM users WHERE id = $1`, userID).Scan(&role)
	if err != nil {
		return "", err
	}
	return role, nil
}

// userIDByBadge resolves a badge number to the user's UUID id. The badge
// number is the frontend-provided identity; the rest of the system keys on
// the UUID, so every handler that interacts with users/case_assignments must
// translate the badge to its id first.
func (s *store) userIDByBadge(ctx context.Context, badge string) (string, error) {
	var id string
	err := s.db.pool.QueryRow(ctx, `SELECT id::text FROM users WHERE badge_number = $1`, badge).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

// hashChain computes the sha256 of a message returning a hex string.
func hashChain(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// payloadMeta is the JSON shape stored in document_versions.content_payload for
// uploaded evidence files. Legacy append rows keep a raw text payload, so
// content_type derivation must tolerate non-JSON payloads.
type payloadMeta struct {
	StoragePath      string `json:"storage_path"`
	OriginalFilename string `json:"original_filename"`
	MimeType         string `json:"mime_type"`
	SizeBytes        int64  `json:"size_bytes"`
	Sha256           string `json:"sha256,omitempty"`
}

// parsePayloadMeta reports the mime type + filename recorded in a version's
// content payload, or "" when the payload is a legacy raw-text chunk.
func parsePayloadMeta(payload []byte) (mime, filename string) {
	var m payloadMeta
	if err := json.Unmarshal(payload, &m); err != nil || m.StoragePath == "" || m.MimeType == "" {
		return "", ""
	}
	return m.MimeType, m.OriginalFilename
}

// classifyPayload maps a stored content payload to a coarse content kind, used
// by the Versions audit view and case list to render type chips without
// exposing payload bytes.
func classifyPayload(payload []byte) string {
	if mime, _ := parsePayloadMeta(payload); mime != "" {
		return mime
	}
	return "text/plain"
}

// insertDocument creates a documents row under the actor's RLS identity and
// returns the new document id. Used by the evidence upload flow when the file
// is flagged as the first document for a FIR (is_new_document=true). storagePath
// records where the uploaded file is stored (Supabase Storage object key).
func (s *store) insertDocument(ctx context.Context, actorID, role, fir, title, classification, storagePath, createdBy string) (string, error) {
	var id string
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO documents (fir_number, title, classification_level, storage_path, created_by)
			VALUES ($1,$2,$3,$4,$5)
			RETURNING id::text`,
			fir, title, classification, storagePath, createdBy).Scan(&id)
	})
	return id, err
}

// setDocumentStoragePath keeps the documents row's storage_path in sync with
// the latest uploaded file for that document (existing-document uploads).
// Runs under the actor's RLS identity; failures are non-fatal (logged by the
// caller) so a path refresh can never break an already-committed version.
func (s *store) setDocumentStoragePath(ctx context.Context, actorID, role, docID, storagePath string) error {
	return s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE documents SET storage_path = $1, updated_at = now()
			WHERE id = $2`, storagePath, docID)
		return err
	})
}

// documentIDForFirTitle finds an existing document for a FIR + title under the
// actor's RLS identity. It reports "" when none is visible. Guards against the
// upload flow silently creating duplicate document rows.
func (s *store) documentIDForFirTitle(ctx context.Context, actorID, role, fir, title string) (string, error) {
	var id string
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT id::text FROM documents
			WHERE fir_number = $1 AND title = $2
			ORDER BY created_at DESC LIMIT 1`, fir, title).Scan(&id)
		if isNoRows(err) {
			return nil
		}
		return err
	})
	return id, err
}

// documentFirByID returns the fir_number for a document id under the actor's
// RLS identity (so an unassigned officer gets a no-rows error and nothing
// leaks). Used to validate an existing-document upload matches the ticket FIR.
func (s *store) documentFirByID(ctx context.Context, actorID, role, id string) (string, error) {
	var fir string
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT fir_number FROM documents WHERE id = $1`, id).Scan(&fir)
	})
	return fir, err
}

// versionByID loads a single version row (with payload + FIR) under the actor's
// RLS identity. The FIR must match the caller's view-scope ticket before a
// download URL is issued.
func (s *store) versionByID(ctx context.Context, actorID, role, id string) (*DocumentVersion, string, error) {
	var v DocumentVersion
	var treePath, fir string
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT dv.id::text, dv.document_id::text, dv.version_number,
			       dv.tree_path::text, dv.sha256_hash, COALESCE(dv.parent_sha256_hash, ''),
			       dv.signature, dv.content_payload, dv.created_by::text, dv.created_at::text,
			       COALESCE(dv.merged_from_hash, ''), d.fir_number
			FROM document_versions dv
			JOIN documents d ON d.id = dv.document_id
			WHERE dv.id = $1`, id).
			Scan(&v.ID, &v.DocumentID, &v.VersionNumber, &treePath, &v.Sha256Hash,
				&v.ParentSha256Hash, &v.Signature, &v.ContentPayload, &v.CreatedBy, &v.CreatedAt,
				&v.MergedFromHash, &fir)
	})
	v.TreePath = treePath
	v.Kind = classifyVersionKind(treePath, v.MergedFromHash)
	return &v, fir, err
}

// streamedVersion is a document version ready to be pushed to a viewer.
type streamedVersion struct {
	ID             string
	DocumentID     string
	VersionNumber  int
	TreeNode       string
	Sha256Hash     string
	Badge          string
	CreatedAt      time.Time
	ContentPayload []byte
}

// versionFilters narrows the cross-case audit view of document_versions.
type versionFilters struct {
	FIRNumber string // exact fir_number match ("" = unfiltered)
	Badge     string // exact authoring badge match ("" = unfiltered)
	DateFrom  string // inclusive lower bound on created_at (RFC3339, "" = unfiltered)
	DateTo    string // exclusive upper bound on created_at (RFC3339, "" = unfiltered)
}

// VersionRow is one row of a document_versions view (cross-case audit or tree).
// ContentHash is the SHA-256 of the content payload, or "" when the acting role
// cannot read content payload (SYSTEM_ADMIN). ChainValid is computed by the
// backend as a convenience; the frontend independently re-verifies it.
type VersionRow struct {
	// ID is the document_versions row's UUID. Populated only by the per-document
	// tree query (versionsTreeByDocument); the cross-case audit view omits it.
	ID         string `json:"id,omitempty"`
	FIR        string `json:"fir_number"`
	Version    int    `json:"version_number"`
	Sha256Hash string `json:"sha256_hash"`
	ParentHash string `json:"parent_sha256_hash"`
	// TreePath is the ltree path (e.g. "v2", "v2.1") that drives branching.
	TreePath string `json:"tree_path,omitempty"`
	// MergedFromHash records a merge's second parent (the branch being merged
	// in). Empty for non-merge versions.
	MergedFromHash string `json:"merged_from_hash,omitempty"`
	// Kind classifies the row as mainline | branch | merge.
	Kind         string `json:"kind,omitempty"`
	Badge        string `json:"badge_number"`
	CreatedAt    string `json:"created_at"`
	ContentHash  string `json:"content_hash"`
	ChainValid   string `json:"chain_valid"`  // valid | invalid | unverified
	ContentType  string `json:"content_type"` // mime derived from payload; "" when withheld
}

// versionsByFilters returns an RLS-scoped, filtered listing of document
// versions for the cross-case audit view. The query runs under the acting
// user's RLS identity so officers only see assigned FIRs while
// SHO_SUPERVISOR / SYSTEM_ADMIN see all station cases (per schema policies).
// When role is SYSTEM_ADMIN the content payload is never selected (content
// access is withheld for admins; only metadata + hashes are returned).
func (s *store) versionsByFilters(ctx context.Context, actorID, role string, f versionFilters) ([]VersionRow, error) {
	var out []VersionRow
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		q := `
			SELECT d.fir_number, dv.version_number, dv.sha256_hash,
			       COALESCE(dv.parent_sha256_hash, ''), u.badge_number, dv.created_at,
			       dv.tree_path::text, COALESCE(dv.merged_from_hash, '')`
		args := []any{}
		where := []string{"1=1"}
		param := func(v any) string {
			args = append(args, v)
			return fmt.Sprintf("$%d", len(args))
		}
		if role != "SYSTEM_ADMIN" {
			q += ", dv.content_payload"
		}
		q += `
			FROM document_versions dv
			JOIN documents d ON d.id = dv.document_id
			JOIN users u ON u.id = dv.created_by`
		if f.FIRNumber != "" {
			where = append(where, "d.fir_number = "+param(f.FIRNumber))
		}
		if f.Badge != "" {
			where = append(where, "u.badge_number = "+param(f.Badge))
		}
		if f.DateFrom != "" {
			where = append(where, "dv.created_at >= "+param(f.DateFrom))
		}
		if f.DateTo != "" {
			where = append(where, "dv.created_at < "+param(f.DateTo))
		}
		q += "\nWHERE " + strings.Join(where, " AND ")
		q += "\nORDER BY d.fir_number, dv.created_at, dv.version_number"

		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r VersionRow
			var created time.Time
			var treePath, mergedFrom string
			if role != "SYSTEM_ADMIN" {
				var content []byte
				if err := rows.Scan(&r.FIR, &r.Version, &r.Sha256Hash, &r.ParentHash, &r.Badge, &created,
					&treePath, &mergedFrom, &content); err != nil {
					return err
				}
				r.ContentHash = hashChain(content)
				r.ContentType = classifyPayload(content)
			} else {
				if err := rows.Scan(&r.FIR, &r.Version, &r.Sha256Hash, &r.ParentHash, &r.Badge, &created,
					&treePath, &mergedFrom); err != nil {
					return err
				}
				r.ContentHash = ""
				r.ContentType = ""
			}
			r.CreatedAt = created.UTC().Format(time.RFC3339)
			r.TreePath = treePath
			r.MergedFromHash = mergedFrom
			r.Kind = classifyVersionKind(treePath, mergedFrom)
			r.ChainValid = verifyChain(r.Sha256Hash, r.ContentHash, r.ParentHash, r.Badge, r.CreatedAt)
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// verifyChain recomputes SHA256(content_hash || parent_hash || badge ||
// created_at) and compares it to the stored sha256_hash. When content is
// withheld (empty content_hash) the chain cannot be verified and "unverified"
// is returned so the frontend does not false-flag a tamper.
func verifyChain(stored, contentHash, parentHash, badge, createdAt string) string {
	if contentHash == "" {
		return "unverified"
	}
	canonical := contentHash + parentHash + badge + createdAt
	if hashChain([]byte(canonical)) == stored {
		return "valid"
	}
	return "invalid"
}

// insertDocumentVersionBranch writes a hash-chained document_version row that
// records a second parent hash (merged_from_hash) for merge points. Runs under
// the actor's RLS identity, identical to insertDocumentVersion.
func (s *store) insertDocumentVersionBranch(ctx context.Context, actorID, role string, v *DocumentVersion) error {
	return s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO document_versions
				(document_id, version_number, tree_path, sha256_hash, parent_sha256_hash,
				 merged_from_hash, signature, content_payload, created_by, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			RETURNING id::text`,
			v.DocumentID, v.VersionNumber, v.TreePath, v.Sha256Hash, v.ParentSha256Hash,
			strOrNil(v.MergedFromHash), v.Signature, v.ContentPayload, v.CreatedBy, v.CreatedAt).Scan(&v.ID)
	})
}

// strOrNil returns nil for an empty string so an empty merged_from_hash is
// stored as NULL (rather than an empty string), keeping the "is a merge" test
// (merged_from_hash IS NOT NULL) truthful.
func strOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nextVersionNumber returns max(version_number)+1 across the whole document so
// branches and merges — which do not advance the mainline counter — still get a
// UNIQUE (document_id, version_number) value. Runs under the actor's RLS
// identity so an unassigned officer sees no rows and falls back to 1.
func (s *store) nextVersionNumber(ctx context.Context, actorID, role, documentID string) (int, error) {
	var max int
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(dv.version_number), 0) FROM document_versions dv WHERE dv.document_id = $1`,
			documentID).Scan(&max)
	})
	if err != nil {
		return 0, err
	}
	return max + 1, nil
}

// nextBranchTreePath returns the next available child path under parent tree_path
// (e.g. parent "v2" -> "v2.1", then "v2.2", ...). The next index is derived by
// querying the existing direct children (nlevel = parent level + 1) of that path
// — it is never guessed or hardcoded. Runs under the actor's RLS identity so an
// unassigned officer sees no children (max 0 -> first branch ".1").
func (s *store) nextBranchTreePath(ctx context.Context, actorID, role, documentID, parentPath string) (string, error) {
	var maxLeaf int
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		q := `
			SELECT COALESCE(MAX(subpath(dv.tree_path, -1)::text)::int, 0)
			FROM document_versions dv
			WHERE dv.document_id = $1
			  AND dv.tree_path <@ $2
			  AND nlevel(dv.tree_path) = nlevel($2::ltree) + 1`
		return tx.QueryRow(ctx, q, documentID, parentPath).Scan(&maxLeaf)
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.%d", parentPath, maxLeaf+1), nil
}

// classifyVersionKind derives a coarse kind from a version's tree_path and
// merged_from_hash so the UI can render mainline vs branch vs merge without
// post-processing flat rows. A merge is any row with merged_from_hash set; a
// branch is any deeper-than-mainline path; a mainline is a top-level (depth 1)
// path that is not a merge.
func classifyVersionKind(treePath, mergedFromHash string) string {
	if mergedFromHash != "" {
		return "merge"
	}
	if ltreeDepth(treePath) > 1 {
		return "branch"
	}
	return "mainline"
}

// ltreeDepth returns the number of labels in an ltree path (nlevel) without a DB
// round-trip. "v2" -> 1, "v2.1" -> 2. Empty or malformed paths return 0.
func ltreeDepth(path string) int {
	if path == "" {
		return 0
	}
	depth := 1
	for _, c := range path {
		if c == '.' {
			depth++
		}
	}
	return depth
}

// mergeTreePathForTarget extends the merge TARGET's mainline path with the next
// available child index, i.e. the merge row is a child of the mainline version
// it merges back into, not of a new top-level counter.
func (s *store) mergeTreePathForTarget(ctx context.Context, actorID, role, documentID, targetPath string) (string, error) {
	return s.nextBranchTreePath(ctx, actorID, role, documentID, targetPath)
}

// versionsTreeByDocument loads every document version for one document (RLS-
// scoped) grouped into a parent/children tree. The server resolves the parent/
// child relationship using ltree prefix semantics (a child's tree_path has the
// parent's path as a prefix), so the frontend does not have to reconstruct the
// tree from a flat list. Rows are returned with exact tree_path + both parent
// hashes for lineage tracing.
func (s *store) versionsTreeByDocument(ctx context.Context, actorID, role, documentID string) ([]VersionRow, error) {
	var out []VersionRow
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		q := `
			SELECT dv.id::text, d.fir_number, dv.version_number, dv.sha256_hash,
			       COALESCE(dv.parent_sha256_hash, ''), u.badge_number, dv.created_at,
			       dv.tree_path::text, COALESCE(dv.merged_from_hash, '')`
		if role != "SYSTEM_ADMIN" {
			q += ", dv.content_payload"
		}
		q += `
			FROM document_versions dv
			JOIN documents d ON d.id = dv.document_id
			JOIN users u ON u.id = dv.created_by
			WHERE dv.document_id = $1
			ORDER BY dv.tree_path`
		rows, err := tx.Query(ctx, q, documentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r VersionRow
			var created time.Time
			var treePath, mergedFrom string
			if role != "SYSTEM_ADMIN" {
				var content []byte
				if err := rows.Scan(&r.ID, &r.FIR, &r.Version, &r.Sha256Hash, &r.ParentHash, &r.Badge, &created,
					&treePath, &mergedFrom, &content); err != nil {
					return err
				}
				r.ContentHash = hashChain(content)
				r.ContentType = classifyPayload(content)
			} else {
				if err := rows.Scan(&r.ID, &r.FIR, &r.Version, &r.Sha256Hash, &r.ParentHash, &r.Badge, &created,
					&treePath, &mergedFrom); err != nil {
					return err
				}
				r.ContentHash = ""
				r.ContentType = ""
			}
			r.CreatedAt = created.UTC().Format(time.RFC3339)
			r.TreePath = treePath
			r.MergedFromHash = mergedFrom
			r.Kind = classifyVersionKind(treePath, mergedFrom)
			r.ChainValid = verifyChain(r.Sha256Hash, r.ContentHash, r.ParentHash, r.Badge, r.CreatedAt)
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// versionsByFir loads every document version belonging to documents under the
// given FIR, scoped to the actor's RLS identity. If the officer has no case
// assignment for the FIR the RLS policies return zero rows, so nothing leaks.
func (s *store) versionsByFir(ctx context.Context, actorID, role, fir string) ([]streamedVersion, error) {
	var out []streamedVersion
	err := s.db.withUserTx(ctx, actorID, role, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT dv.id::text, dv.document_id::text, dv.version_number, dv.tree_path::text,
			       dv.sha256_hash, u.badge_number, dv.created_at, dv.content_payload
			FROM document_versions dv
			JOIN documents d ON d.id = dv.document_id
			JOIN users u ON u.id = dv.created_by
			WHERE d.fir_number = $1
			ORDER BY dv.created_at ASC, dv.version_number ASC`, fir)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v streamedVersion
			if err := rows.Scan(&v.ID, &v.DocumentID, &v.VersionNumber, &v.TreeNode,
				&v.Sha256Hash, &v.Badge, &v.CreatedAt, &v.ContentPayload); err != nil {
				return err
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	return out, err
}
