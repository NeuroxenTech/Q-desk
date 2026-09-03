export interface HandshakeGetResponse {
  encapsulation_key: string;
  mldsa_public_key: string;
}

export interface HandshakePostResponse {
  encapsulation_key: string;
  mldsa_public_key: string;
  session_id: string;
  signature: string;
}

export interface Ticket {
  id: string;
  fir_number: string;
  user_id: string;
  badge_number: string;
  scope: "stream" | "append" | "all";
  issued_at: string;
  expires_at: string;
  signature: string;
  consumed: boolean;
}

export interface TicketRequestResponse {
  ticket: Ticket;
}

export interface DocumentVersion {
  id?: string;
  document_id: string;
  version_number: number;
  tree_path: string;
  sha256_hash: string;
  parent_sha256_hash?: string;
  merged_from_hash?: string;
  kind?: "mainline" | "branch" | "merge";
  signature: string;
  content_payload?: string;
  created_by: string;
  created_at?: string;
}

export interface StreamMessage {
  type: "chunk" | "close" | "auth";
  ticket_id?: string;
  payload?: string;
  meta?: StreamChunkMeta;
}

export interface BreachResponse {
  revoked: boolean;
  session_revoked: boolean;
}

export interface SessionInfo {
  ticket_id: string;
  session_key_b64: string;
}

// One row of a document_versions view (cross-case GET /api/versions or the
// per-document tree GET /api/versions/:documentId/tree).
export interface VersionRow {
  // Only present on the per-document tree endpoint (the DB row UUID, needed to
  // fetch that specific version's content).
  id?: string;
  fir_number: string;
  version_number: number;
  sha256_hash: string;
  parent_sha256_hash: string;
  // ltree path that drives branching ("v2", "v2.1", ...).
  tree_path?: string;
  // second parent of a merge (the branch being merged in); empty otherwise.
  merged_from_hash?: string;
  // "mainline" | "branch" | "merge" — computed server-side.
  kind?: string;
  badge_number: string;
  created_at: string;
  content_hash: string;
  // "valid" | "invalid" | "unverified" — precomputed by the backend, but the
  // frontend independently re-verifies each row before trusting it.
  chain_valid: string;
  // MIME type derived from the content payload ("" when withheld for admins).
  content_type?: string;
}

export interface VersionsResponse {
  role: string;
  versions: VersionRow[];
}

// A node of the server-resolved per-document version tree.
export interface VersionTreeNode {
  id: string;
  version_number: number;
  tree_path: string;
  kind: "mainline" | "branch" | "merge";
  sha256_hash: string;
  parent_sha256_hash: string;
  merged_from_hash?: string;
  badge_number: string;
  created_at: string;
  content_type?: string;
  chain_valid: string;
  children: VersionTreeNode[];
}

export interface VersionTreeResponse {
  role: string;
  fir_number: string;
  document_id: string;
  versions: VersionRow[];
  tree: VersionTreeNode[];
}

// POST /api/versions/branch response
export interface BranchResponse {
  version: DocumentVersion;
  consumed: boolean;
  scan_status: string;
  parent_sha256_hash: string;
}

// POST /api/versions/merge response
export interface MergeResponse {
  version: DocumentVersion;
  consumed: boolean;
  merged_from_hash: string;
}

// Streamed evidence chunk — the backend enriches each chunk with the version
// and document ids so the viewer can render files and add follow-up versions.
export interface StreamChunkMeta {
  version_id?: string;
  document_id?: string;
  version?: number;
  hash?: string;
  node?: string;
  mime_type?: string;
  author?: string;
  time?: string;
}

export interface StreamChunk {
  type: string;
  payload?: string;
  meta?: StreamChunkMeta;
}

// The JSON metadata the backend stores in document_versions.content_payload
// for an uploaded evidence file.
export interface FilePayloadMeta {
  storage_path?: string;
  original_filename?: string;
  mime_type?: string;
  size_bytes?: number;
  sha256?: string;
}

// POST /api/upload response.
export interface UploadedVersion {
  document_id: string;
  version_id?: string;
  version_number: number;
  tree_path?: string;
  sha256_hash: string;
  content_hash?: string;
  signature?: string;
  mime_type: string;
  filename: string;
  size_bytes: number;
  created_at?: string;
  classification?: string;
}

export interface UploadResponse {
  version: UploadedVersion;
  consumed: boolean;
  scan_status: string;
}

// GET /api/files/{version_id}/download-url response.
export interface DownloadURLResponse {
  url: string;
  mime_type: string;
  filename: string;
  size_bytes: number;
  version_number: number;
  fir_number: string;
  document_id: string;
  expires_seconds: number;
}

// First-document upload metadata used by the Cases list entry point.
export interface UploadDraft {
  isNewDocument: boolean;
  documentId?: string;
  title?: string;
  classificationLevel?: string;
  // Chain inputs the caller can pre-fill (evidence viewer uses the streamed
  // versions to supply these; the Cases list leave them for the dialog).
  versionNumber?: number;
  treePath?: string;
  parentSha256Hash?: string;
}
