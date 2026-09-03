package main

// DocumentVersion mirrors a row of the document_versions table for hash-chained
// version writes.
type DocumentVersion struct {
	ID               string `json:"id,omitempty"`
	DocumentID       string `json:"document_id"`
	VersionNumber    int    `json:"version_number"`
	TreePath         string `json:"tree_path"`
	Sha256Hash       string `json:"sha256_hash"`
	ParentSha256Hash string `json:"parent_sha256_hash,omitempty"`
	// MergedFromHash records the SECOND parent of a merge version (the branch
	// being brought back in). parent_sha256_hash holds the primary/merge-target
	// parent, so a merge's full lineage is cryptographically traceable.
	MergedFromHash string `json:"merged_from_hash,omitempty"`
	Signature      string `json:"signature"`
	ContentPayload []byte `json:"content_payload,omitempty"`
	CreatedBy      string `json:"created_by"`
	CreatedAt      string `json:"created_at,omitempty"`

	// Kind classifies a version for the UI tree: "mainline" | "branch" | "merge".
	// Computed from tree_path depth, not stored. Omitted when empty (legacy rows).
	Kind string `json:"kind,omitempty"`
}
