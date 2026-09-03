// Shared policy helpers for evidence content. The server is authoritative for
// every rule here (backend/upload.go); these constants/helpers exist so the UI
// can mirror the allowlist, size cap and upload visibility client-side.

import type { FilePayloadMeta } from "./types";

// UPLOAD_ALLOWED mirrors the server magic-byte allowlist in allowedUploadTypes().
export const UPLOAD_ALLOWED: {
  mime: string;
  label: string;
  accept: string;
}[] = [
  { mime: "image/jpeg", label: "Image (JPEG)", accept: ".jpg,.jpeg" },
  { mime: "image/png", label: "Image (PNG)", accept: ".png" },
  { mime: "application/pdf", label: "Document (PDF)", accept: ".pdf" },
  { mime: "video/mp4", label: "Video (MP4)", accept: ".mp4" },
  { mime: "audio/mpeg", label: "Audio (MP3)", accept: ".mp3" },
  { mime: "audio/wav", label: "Audio (WAV)", accept: ".wav" },
];

// MAX_UPLOAD_MB mirrors the server MAX_UPLOAD_SIZE_MB default (50).
export const MAX_UPLOAD_MB = 50;

// canUploadEvidence mirrors backend canUploadEvidence(): only investigating
// officer ranks ingest evidence; supervisors and admins cannot.
export function canUploadEvidence(role: string): boolean {
  switch (role) {
    case "SYSTEM_ADMIN":
    case "SHO_SUPERVISOR":
      return false;
    default:
      return true;
  }
}

export type ContentKind = "image" | "pdf" | "video" | "audio" | "text" | "unknown";

// mimeToKind maps a MIME string to a coarse rendering kind. Legacy append rows
// carry raw text payloads, so an empty/unknown type renders as text.
export function mimeToKind(mime: string | undefined): ContentKind {
  if (!mime) return "text";
  if (mime.startsWith("image/")) return "image";
  if (mime === "application/pdf") return "pdf";
  if (mime.startsWith("video/")) return "video";
  if (mime.startsWith("audio/")) return "audio";
  return "unknown";
}

export function kindLabel(kind: ContentKind): string {
  switch (kind) {
    case "image":
      return "Image";
    case "pdf":
      return "PDF Document";
    case "video":
      return "Video";
    case "audio":
      return "Audio";
    case "text":
      return "Text";
    default:
      return "File";
  }
}

export function formatBytes(n: number | undefined): string {
  if (!n) return "—";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

export function truncateDigest(digest: string | undefined, n = 16): string {
  if (!digest) return "—";
  if (digest.length <= n * 2 + 1) return digest;
  return `${digest.slice(0, n)}…${digest.slice(-n)}`;
}

// parseFilePayload detects whether a streamed chunk payload is the JSON
// metadata recorded for an uploaded evidence file (as opposed to a legacy
// raw-text append chunk). Returns null for text chunks.
export function parseFilePayload(payload: string): FilePayloadMeta | null {
  if (!payload || !payload.trim().startsWith("{")) return null;
  try {
    const meta = JSON.parse(payload) as FilePayloadMeta;
    if (meta.storage_path && meta.mime_type) return meta;
    return null;
  } catch {
    return null;
  }
}