import type {
  TicketRequestResponse,
  BreachResponse,
  VersionsResponse,
  UploadResponse,
  DownloadURLResponse,
  StreamChunk,
  StreamChunkMeta,
  VersionTreeResponse,
  BranchResponse,
  MergeResponse,
} from "./types";

const API_URL = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";
const WS_URL =
  process.env.NEXT_PUBLIC_WS_URL || "ws://localhost:8080/api/stream";

export async function requestTicket(
  sessionId: string,
  userId: string,
  badgeNumber: string,
  firNumber: string,
  scope: string = "all"
): Promise<TicketRequestResponse> {
  const res = await fetch(`${API_URL}/api/ticket/request`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      session_id: sessionId,
      user_id: userId,
      badge_number: badgeNumber,
      fir_number: firNumber,
      scope,
    }),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `Ticket request failed: ${res.status}`);
  }
  return res.json();
}

export async function reportBreach(
  sessionId: string,
  ticketId: string,
  userId: string,
  firNumber: string,
  reason: string
): Promise<BreachResponse> {
  const res = await fetch(`${API_URL}/api/breach`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      session_id: sessionId,
      ticket_id: ticketId,
      user_id: userId,
      fir_number: firNumber,
      reason,
    }),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `Breach report failed: ${res.status}`);
  }
  return res.json();
}

export function createStreamConnection(ticketId: string): WebSocket {
  const ws = new WebSocket(WS_URL);
  ws.onopen = () => {
    ws.send(JSON.stringify({ type: "auth", ticket_id: ticketId }));
  };
  return ws;
}

export interface VersionsFilters {
  firNumber?: string;
  badgeNumber?: string;
  dateFrom?: string;
  dateTo?: string;
}

// fetchVersions loads the cross-case document_versions audit view, RLS-scoped
// to the requesting officer's identity. `actorBadge` identifies the requesting
// officer (resolved server-side for RLS); `filters.badgeNumber` filters rows by
// authoring badge when provided.
export async function fetchVersions(
  sessionId: string,
  actorBadge: string,
  filters: VersionsFilters = {}
): Promise<VersionsResponse> {
  const params = new URLSearchParams({
    session_id: sessionId,
    actor_badge: actorBadge,
  });
  if (filters.firNumber) params.set("fir_number", filters.firNumber);
  if (filters.badgeNumber) params.set("badge_number", filters.badgeNumber);
  if (filters.dateFrom) params.set("date_from", filters.dateFrom);
  if (filters.dateTo) params.set("date_to", filters.dateTo);

  const res = await fetch(`${API_URL}/api/versions?${params.toString()}`, {
    method: "GET",
    headers: { "Content-Type": "application/json" },
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `Versions request failed: ${res.status}`);
  }
  return res.json();
}

// fetchVersionTree loads the full version set for ONE document, pre-structured
// as a tree by the backend (GET /api/versions/:documentId/tree), so the
// Evidence Viewer does not have to reconstruct parent/children client-side.
export async function fetchVersionTree(
  sessionId: string,
  actorBadge: string,
  documentId: string
): Promise<VersionTreeResponse> {
  const params = new URLSearchParams({
    session_id: sessionId,
    actor_badge: actorBadge,
  });
  const res = await fetch(
    `${API_URL}/api/versions/${encodeURIComponent(documentId)}/tree?${params.toString()}`,
    { method: "GET", headers: { "Content-Type": "application/json" } }
  );
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `Version tree request failed: ${res.status}`);
  }
  return res.json();
}

// branchVersion creates a new branch off an existing source version, either as
// a text annotation (annotation field) or an evidence file (file part). A
// single-use append-scope ticket for the FIR must be issued first.
export function branchVersion(
  params: {
    sessionId: string;
    ticketId: string;
    firNumber: string;
    sourceVersionId: string;
    annotation?: string;
    file?: File;
  },
  onProgress?: UploadProgressHandler
): Promise<BranchResponse> {
  const form = new FormData();
  form.set("session_id", params.sessionId);
  form.set("ticket_id", params.ticketId);
  form.set("fir_number", params.firNumber);
  form.set("source_version_id", params.sourceVersionId);
  if (params.annotation) form.set("annotation", params.annotation);
  if (params.file) form.set("file", params.file);

  return new Promise<BranchResponse>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `${API_URL}/api/versions/branch`);
    if (onProgress && xhr.upload) {
      xhr.upload.onprogress = (ev) => {
        if (ev.lengthComputable) onProgress(Math.round((ev.loaded / ev.total) * 100));
      };
    }
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve(JSON.parse(xhr.responseText));
        } catch {
          reject(new Error("Malformed branch response"));
        }
        return;
      }
      reject(new Error(xhr.responseText || `Branch failed: ${xhr.status}`));
    };
    xhr.onerror = () => reject(new Error("Branch failed — network error"));
    xhr.send(form);
  });
}

// mergeVersion merges a branch back into a chosen mainline (merge target)
// version, creating a new dual-parent merge point. Requires a single-use
// append-scope ticket for the FIR.
export async function mergeVersion(params: {
  sessionId: string;
  ticketId: string;
  firNumber: string;
  branchVersionId: string;
  mergeTargetVersionId: string;
}): Promise<MergeResponse> {
  const res = await fetch(`${API_URL}/api/versions/merge`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      session_id: params.sessionId,
      ticket_id: params.ticketId,
      fir_number: params.firNumber,
      branch_version_id: params.branchVersionId,
      merge_target_version_id: params.mergeTargetVersionId,
    }),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `Merge failed: ${res.status}`);
  }
  return res.json();
}

export interface UploadEvidenceParams {
  sessionId: string;
  ticketId: string;
  firNumber: string;
  isNewDocument: boolean;
  documentId?: string;
  title?: string;
  classificationLevel?: string;
  versionNumber: number;
  treePath: string;
  parentSha256Hash?: string;
  file: File;
}

export type UploadProgressHandler = (percent: number) => void;

// uploadEvidence POSTs a multipart evidence file to /api/upload using XHR so
// real upload progress can be reported (fetch has no upload.onprogress).
// A single-use append-scope ticket must have been issued first.
export function uploadEvidence(
  params: UploadEvidenceParams,
  onProgress?: UploadProgressHandler
): Promise<UploadResponse> {
  const form = new FormData();
  form.set("session_id", params.sessionId);
  form.set("ticket_id", params.ticketId);
  form.set("fir_number", params.firNumber);
  form.set("is_new_document", params.isNewDocument ? "true" : "false");
  if (params.documentId) form.set("document_id", params.documentId);
  if (params.title) form.set("title", params.title);
  if (params.classificationLevel) {
    form.set("classification_level", params.classificationLevel);
  }
  form.set("version_number", String(params.versionNumber));
  form.set("tree_path", params.treePath);
  if (params.parentSha256Hash) {
    form.set("parent_sha256_hash", params.parentSha256Hash);
  }
  form.set("file", params.file);

  return new Promise<UploadResponse>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `${API_URL}/api/upload`);
    if (onProgress && xhr.upload) {
      xhr.upload.onprogress = (ev) => {
        if (ev.lengthComputable) {
          onProgress(Math.round((ev.loaded / ev.total) * 100));
        }
      };
    }
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve(JSON.parse(xhr.responseText));
        } catch {
          reject(new Error("Malformed upload response"));
        }
        return;
      }
      // The backend sends specific plain-text errors; surface the message.
      reject(new Error(xhr.responseText || `Upload failed: ${xhr.status}`));
    };
    xhr.onerror = () => reject(new Error("Upload failed — network error"));
    xhr.send(form);
  });
}

// fetchDownloadUrl asks the backend for a 60-second signed URL for a stored
// evidence version. Requires a live view-scope ticket for the same FIR.
export async function fetchStreamVersions(
  sessionId: string,
  ticketId: string
): Promise<StreamChunk[] | null> {
  const params = new URLSearchParams({
    session_id: sessionId,
    ticket_id: ticketId,
  });
  const res = await fetch(
    `${API_URL}/api/stream/versions?${params.toString()}`,
    { method: "GET", headers: { "Content-Type": "application/json" } }
  );
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `Stream versions request failed: ${res.status}`);
  }
  const body = (await res.json()) as {
    chunks?: Array<{
      type?: string;
      payload?: string;
      meta?: StreamChunkMeta;
    }>;
  };
  return (
    body.chunks?.map((c) => ({
      type: c.type || "chunk",
      payload: c.payload,
      meta: c.meta,
    })) || []
  );
}

export async function fetchDownloadUrl(
  sessionId: string,
  ticketId: string,
  versionId: string
): Promise<DownloadURLResponse> {
  const params = new URLSearchParams({
    session_id: sessionId,
    ticket_id: ticketId,
  });
  const res = await fetch(
    `${API_URL}/api/files/${encodeURIComponent(versionId)}/download-url?${params.toString()}`,
    { method: "GET", headers: { "Content-Type": "application/json" } }
  );
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `Download URL request failed: ${res.status}`);
  }
  return res.json();
}

// fetchFileBytes pulls the raw evidence bytes from the backend content endpoint.
// Used to build a same-origin Blob URL for the PDF viewer so that right-click
// and print/save shortcuts inside the PDF are subject to the parent guard.
export async function fetchFileBytes(url: string): Promise<Blob> {
  const res = await fetch(url);
  if (!res.ok) {
    throw new Error(`Content fetch failed: ${res.status}`);
  }
  return res.blob();
}
