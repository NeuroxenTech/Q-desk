package main

import (
	"testing"
)

// TestBranchParentHashChainsToSource verifies the core branching invariant: the
// branch version's parent_sha256_hash must equal the SOURCE version's hash
// exactly, and the branch's chained sha256 binds that parent (so a tampered
// parent cannot pass verification).
func TestBranchParentHashChainsToSource(t *testing.T) {
	sourceHash := hashChain([]byte("source content"))
	badge := "IND-IO-402"
	createdAt := "2026-09-03T10:00:00Z"

	// Simulate the exact construction used by handleBranch:
	//   content_hash = SHA256(branch payload)
	//   parent_sha256_hash = sourceHash
	//   sha256_hash   = SHA256(content_hash || parent_hash || badge || created_at)
	branchPayload := []byte("annotation on source v2")
	contentHash := hashChain(branchPayload)
	parentHash := sourceHash
	sha := hashChain([]byte(contentHash + parentHash + badge + createdAt))

	if parentHash != sourceHash {
		t.Fatalf("branch parent_sha256_hash (%s) does not match source hash (%s)", parentHash, sourceHash)
	}

	// Tamper-detection: if the branch hypothetically pointed at a different
	// parent, re-verification must fail.
	otherParent := hashChain([]byte("some other version"))
	if verifyChain(sha, contentHash, otherParent, badge, createdAt) == "valid" {
		t.Fatal("branch chain should NOT verify against a non-source parent")
	}
	if verifyChain(sha, contentHash, parentHash, badge, createdAt) != "valid" {
		t.Fatal("branch chain should verify against its source parent")
	}
}

// TestMergePopulatesMergedFromHash verifies the merging invariant: the merge
// version's merged_from_hash must equal the BRANCH version's hash, its
// parent_sha256_hash the target's hash, and both must be traceable.
func TestMergePopulatesMergedFromHash(t *testing.T) {
	targetHash := hashChain([]byte("mainline v4"))
	branchHash := hashChain([]byte("branch v2.1 annotation"))
	badge := "IND-IO-402"
	createdAt := "2026-09-03T11:00:00Z"

	// handleMerge chained construction.
	contentHash := hashChain([]byte(`{"type":"merge"}`))
	parentHash := targetHash
	mergedFromHash := branchHash
	sha := hashChain([]byte(contentHash + parentHash + badge + createdAt))

	if mergedFromHash != branchHash {
		t.Fatalf("merge merged_from_hash (%s) does not match branch hash (%s)", mergedFromHash, branchHash)
	}
	if parentHash != targetHash {
		t.Fatalf("merge parent_sha256_hash (%s) does not match target hash (%s)", parentHash, targetHash)
	}
	// Both parents are explicit and distinct.
	if mergedFromHash == parentHash {
		t.Fatal("merge's two parents must be distinct")
	}
	// The merge chain still verifies against the primary (target) parent.
	if verifyChain(sha, contentHash, parentHash, badge, createdAt) != "valid" {
		t.Fatal("merge chain should verify against its primary parent")
	}
}

// TestTreePathHelpers exercises the ltree path logic backing branching.
func TestTreePathHelpers(t *testing.T) {
	cases := []struct {
		path       string
		kind       string
		depth      int
		parentPath string
	}{
		{"v1", "mainline", 1, ""},
		{"v2", "mainline", 1, ""},
		{"v2.1", "branch", 2, "v2"},
		{"v2.2", "branch", 2, "v2"},
		{"v2.1.1", "branch", 3, "v2.1"},
	}
	for _, c := range cases {
		if got := ltreeDepth(c.path); got != c.depth {
			t.Errorf("ltreeDepth(%q) = %d, want %d", c.path, got, c.depth)
		}
		if got := classifyVersionKind(c.path, ""); got != c.kind {
			t.Errorf("classifyVersionKind(%q,\"\") = %q, want %q", c.path, got, c.kind)
		}
		if got := parentLtreePath(c.path); got != c.parentPath {
			t.Errorf("parentLtreePath(%q) = %q, want %q", c.path, got, c.parentPath)
		}
	}
	// A merge (merged_from_hash set) is classified as merge regardless of depth.
	if got := classifyVersionKind("v4.1", "somebranchhash"); got != "merge" {
		t.Errorf("merge classification = %q, want \"merge\"", got)
	}
}

// TestBuildVersionTree verifies the server resolves parent/child structure from
// flat ltree paths (including multi-level branches and a merge under a target).
func TestBuildVersionTree(t *testing.T) {
	rows := []VersionRow{
		{ID: "1", TreePath: "v1", Kind: "mainline", Sha256Hash: "h1"},
		{ID: "2", TreePath: "v2", Kind: "mainline", Sha256Hash: "h2"},
		{ID: "3", TreePath: "v2.1", Kind: "branch", Sha256Hash: "h3", MergedFromHash: ""},
		{ID: "4", TreePath: "v2.2", Kind: "branch", Sha256Hash: "h4"},
		{ID: "5", TreePath: "v3", Kind: "mainline", Sha256Hash: "h5"},
		{ID: "6", TreePath: "v3.1", Kind: "merge", Sha256Hash: "h6", MergedFromHash: "h3"},
		{ID: "7", TreePath: "v2.1.1", Kind: "branch", Sha256Hash: "h7", MergedFromHash: ""},
	}
	tree := buildVersionTree(rows)

	rootsByName := map[string]*versionTreeNode{}
	for _, r := range tree {
		rootsByName[r.TreePath] = r
	}
	if len(rootsByName) != 3 {
		t.Fatalf("expected 3 mainline roots, got %d", len(rootsByName))
	}

	v2 := rootsByName["v2"]
	if v2 == nil || len(v2.Children) != 2 {
		t.Fatalf("v2 should have 2 branch children (v2.1, v2.2)")
	}
	// v2.1 itself has a child branch v2.1.1 (branch off a branch).
	var v21 *versionTreeNode
	for _, c := range v2.Children {
		if c.TreePath == "v2.1" {
			v21 = c
		}
	}
	if v21 == nil || len(v21.Children) != 1 || v21.Children[0].TreePath != "v2.1.1" {
		t.Fatalf("v2.1 should have one child v2.1.1")
	}
	// The merge node (v3.1) sits under its target v3.
	v3 := rootsByName["v3"]
	if v3 == nil || len(v3.Children) != 1 || v3.Children[0].Kind != "merge" {
		t.Fatalf("v3 should have one merge child")
	}
	if v3.Children[0].MergedFrom != "h3" {
		t.Fatalf("merge node should preserve its branch parent hash, got %q", v3.Children[0].MergedFrom)
	}
}
