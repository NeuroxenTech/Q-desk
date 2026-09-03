package main

import (
	"log"
	"net/http"
)

// handleVersionTree serves the full version set for ONE document, pre-structured
// as a tree (GET /api/versions/{documentId}/tree). It resolves parent/child
// relationships server-side from the ltree tree_path and returns each node's
// exact tree_path + both parent hashes, so the frontend renders the structure
// directly rather than rebuilding it from a flat list. RLS-scoped like
// GET /api/versions: the requesting officer must be able to read the document.
func (a *app) handleVersionTree(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	documentID := r.PathValue("documentId")
	if documentID == "" {
		http.Error(w, "document id is required", http.StatusBadRequest)
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" || !a.validSession(sessionID) {
		http.Error(w, "invalid or expired session", http.StatusUnauthorized)
		return
	}
	actorBadge := r.URL.Query().Get("actor_badge")
	if actorBadge == "" {
		http.Error(w, "actor_badge is required", http.StatusBadRequest)
		return
	}
	uid, err := a.st.userIDByBadge(r.Context(), actorBadge)
	if err != nil {
		http.Error(w, "invalid badge", http.StatusUnauthorized)
		return
	}
	role, err := a.st.userRole(r.Context(), uid)
	if err != nil {
		log.Printf("tree role lookup error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Verify the document is accessible under the actor's identity (returns the
	// FIR, throwing no-rows → 403 for unassigned officers).
	fir, err := a.st.documentFirByID(r.Context(), uid, role, documentID)
	if err != nil {
		log.Printf("tree document lookup error: %v", err)
		http.Error(w, "document not accessible", http.StatusForbidden)
		return
	}

	rows, err := a.st.versionsTreeByDocument(r.Context(), uid, role, documentID)
	if err != nil {
		log.Printf("tree query error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"role":         role,
		"fir_number":   fir,
		"document_id":  documentID,
		"versions":     rows,
		"tree":         buildVersionTree(rows),
	})
}

// versionTreeNode is a server-resolved node in a document's version tree.
type versionTreeNode struct {
	ID         string            `json:"id"`
	Version    int               `json:"version_number"`
	TreePath   string            `json:"tree_path"`
	Kind       string            `json:"kind"`
	Sha256Hash string            `json:"sha256_hash"`
	ParentHash string            `json:"parent_sha256_hash"`
	MergedFrom string            `json:"merged_from_hash,omitempty"`
	Badge      string            `json:"badge_number"`
	CreatedAt  string            `json:"created_at"`
	ContentType string           `json:"content_type,omitempty"`
	ChainValid string            `json:"chain_valid"`
	Children   []*versionTreeNode `json:"children"`
}

// buildVersionTree resolves a flat version list into a parent/children tree
// using ltree prefix semantics: a node's direct parent is the longest version
// whose tree_path is a strict prefix. Every node keeps its tree_path and both
// parent hashes so mainline/branch/merge relationships and lineage are explicit.
func buildVersionTree(rows []VersionRow) []*versionTreeNode {
	byPath := make(map[string]*versionTreeNode, len(rows))
	roots := []*versionTreeNode{}
	for _, r := range rows {
		n := &versionTreeNode{
			ID:          r.ID,
			Version:     r.Version,
			TreePath:    r.TreePath,
			Kind:        r.Kind,
			Sha256Hash:  r.Sha256Hash,
			ParentHash:  r.ParentHash,
			MergedFrom:  r.MergedFromHash,
			Badge:       r.Badge,
			CreatedAt:   r.CreatedAt,
			ContentType: r.ContentType,
			ChainValid:  r.ChainValid,
			Children:    []*versionTreeNode{},
		}
		byPath[r.TreePath] = n
	}
	// Attach children: for each node, find its direct parent by longest strict
	// prefix of its tree_path. Nodes with no parent present (depth-1 mainline,
	// e.g. "v1"/"v2") become roots.
	seenChild := map[string]bool{}
	for _, r := range rows {
		node := byPath[r.TreePath]
		parentPath := parentLtreePath(r.TreePath)
		if parent, ok := byPath[parentPath]; ok && parent != node {
			parent.Children = append(parent.Children, node)
			seenChild[r.TreePath] = true
		}
	}
	for _, r := range rows {
		if !seenChild[r.TreePath] {
			roots = append(roots, byPath[r.TreePath])
		}
	}
	return roots
}

// parentLtreePath returns the parent path of an ltree path, or "" if it has
// depth 1 (a mainline root). "v2.1" -> "v2", "v2" -> "".
func parentLtreePath(path string) string {
	idx := -1
	for i := 0; i < len(path); i++ {
		if path[i] == '.' {
			idx = i
		}
	}
	if idx < 0 {
		return ""
	}
	return path[:idx]
}

// handleVersions serves the cross-case document_versions audit view (GET only).
// It is RLS-scoped identically to the existing case queries: the requesting
// officer acts under their own identity, so regular officers only see versions
// for FIRs they are assigned to, while SHO_SUPERVISOR / SYSTEM_ADMIN see all
// station cases (see db/migrations/schema.sql). SYSTEM_ADMIN rows omit content
// (content_hash is empty) so content_payload is never exposed to admins.
func (a *app) handleVersions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" || !a.validSession(sessionID) {
		http.Error(w, "invalid or expired session", http.StatusUnauthorized)
		return
	}

	// The frontend identifies itself by badge number; resolve it to the user's
	// UUID so RLS scoping keys on users.id like every other handler. The actor
	// identity travels in `actor_badge` so `badge_number` stays free as a
	// *filter* over the authoring badge (see versionFilters).
	actorBadge := r.URL.Query().Get("actor_badge")
	if actorBadge == "" {
		http.Error(w, "actor_badge is required", http.StatusBadRequest)
		return
	}
	uid, err := a.st.userIDByBadge(r.Context(), actorBadge)
	if err != nil {
		http.Error(w, "invalid badge", http.StatusUnauthorized)
		return
	}
	role, err := a.st.userRole(r.Context(), uid)
	if err != nil {
		log.Printf("versions role lookup error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	f := versionFilters{
		FIRNumber: r.URL.Query().Get("fir_number"),
		Badge:     r.URL.Query().Get("badge_number"),
		DateFrom:  r.URL.Query().Get("date_from"),
		DateTo:    r.URL.Query().Get("date_to"),
	}

	rows, err := a.st.versionsByFilters(r.Context(), uid, role, f)
	if err != nil {
		log.Printf("versions query error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"role":     role,
		"versions": rows,
	})
}
