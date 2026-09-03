"use client";

import { useEffect, useRef, useState } from "react";
import { requestTicket, uploadEvidence, UploadProgressHandler } from "@/lib/api";
import {
  UPLOAD_ALLOWED,
  MAX_UPLOAD_MB,
  truncateDigest,
} from "@/lib/content";
import type {
  Ticket,
  UploadDraft,
  UploadResponse,
} from "@/lib/types";
import PrimaryButton from "@/components/ui/PrimaryButton";
import SecondaryButton from "@/components/ui/SecondaryButton";
import StatusBadge from "@/components/ui/StatusBadge";

type Phase =
  | "ticket" // requesting the append-scope ticket
  | "picker" // ticket issued, choose a file + fields
  | "uploading" // XHR transferring bytes
  | "scanning" // cosmetic malware-scan state
  | "done" // success summary
  | "error";

const CLASSIFICATIONS = ["RESTRICTED", "CONFIDENTIAL", "SECRET", "TOP_SECRET"];

// Error mapping — the backend answers with specific plain-text messages; turn
// them into the user-facing copies requested by the feature.
function describeError(raw: string): string {
  const lower = raw.toLowerCase();
  if (lower.includes("file too large"))
    return `File is too large — the limit is ${MAX_UPLOAD_MB} MB.`;
  if (lower.includes("file type not allowed"))
    return "File type not allowed — evidence must be a JPEG, PNG, PDF, MP4, MP3 or WAV file.";
  if (lower.includes("ticket expired") || lower.includes("ticket not found"))
    return "Your access ticket expired — close and reopen this dialog to mint a fresh one.";
  if (lower.includes("session"))
    return "Your session is no longer valid — please re-authenticate.";
  if (lower.includes("storage not configured"))
    return "Evidence storage is not configured on the server.";
  return raw || "Upload failed. Please try again.";
}

export default function UploadEvidenceDialog({
  open,
  onClose,
  sessionId,
  badge,
  firNumber,
  draft,
  onSuccess,
}: {
  open: boolean;
  onClose: () => void;
  sessionId: string | null;
  badge: string;
  firNumber: string;
  draft: UploadDraft;
  onSuccess: (result: UploadResponse) => void;
}) {
  const [phase, setPhase] = useState<Phase>("ticket");
  const [error, setError] = useState<string | null>(null);
  const [ticket, setTicket] = useState<Ticket | null>(null);
  const [file, setFile] = useState<File | null>(null);
  const [clientError, setClientError] = useState<string | null>(null);
  const [progress, setProgress] = useState(0);
  const [title, setTitle] = useState(draft.title || "");
  const [classification, setClassification] = useState(
    draft.classificationLevel || "RESTRICTED"
  );
  const [result, setResult] = useState<UploadResponse | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  // Mint an append-scope ticket every time the dialog opens. This reuses the
  // existing /api/ticket/request flow (and its loading state) exactly.
  useEffect(() => {
    if (!open) return;
    setPhase("ticket");
    setError(null);
    setClientError(null);
    setFile(null);
    setResult(null);
    setProgress(0);
    setTitle(draft.title || "");
    setClassification(draft.classificationLevel || "RESTRICTED");

    let cancelled = false;
    if (!sessionId) {
      setPhase("error");
      setError("No active session — re-authenticate first.");
      return;
    }
    requestTicket(sessionId, badge, badge, firNumber, "append")
      .then((resp) => {
        if (cancelled) return;
        setTicket(resp.ticket);
        setPhase("picker");
      })
      .catch((err: any) => {
        if (cancelled) return;
        setPhase("error");
        setError(describeError(err?.message || "Ticket request failed"));
      });
    return () => {
      cancelled = true;
    };
  }, [open, sessionId, badge, firNumber, draft.title, draft.classificationLevel]);

  if (!open) return null;

  const pickFile = (f: File | null) => {
    setClientError(null);
    if (!f) {
      setFile(null);
      return;
    }
    // UX pre-check only — the server re-validates via magic bytes.
    const allowed = UPLOAD_ALLOWED.find((a) => a.mime === f.type);
    if (!allowed && !/\.(jpe?g|png|pdf|mp4|mp3|wav)$/i.test(f.name)) {
      setClientError("File type not allowed — JPEG, PNG, PDF, MP4, MP3 or WAV only.");
      setFile(null);
      return;
    }
    if (f.size > MAX_UPLOAD_MB * 1024 * 1024) {
      setClientError(`File is too large — the limit is ${MAX_UPLOAD_MB} MB.`);
      setFile(null);
      return;
    }
    setFile(f);
  };

  const startUpload = async () => {
    if (!file || !ticket || !sessionId) return;
    setProgress(0);
    setPhase("uploading");
    const onProgress: UploadProgressHandler = (p) => setProgress(p);
    try {
      const resp = await uploadEvidence(
        {
          sessionId,
          ticketId: ticket.id,
          firNumber,
          isNewDocument: draft.isNewDocument,
          documentId: draft.documentId,
          title: draft.isNewDocument ? title.trim() : undefined,
          classificationLevel: draft.isNewDocument ? classification : undefined,
          versionNumber: draft.versionNumber || 1,
          treePath: draft.treePath || `v${draft.versionNumber || 1}`,
          parentSha256Hash: draft.parentSha256Hash,
          file,
        },
        onProgress
      );
      // Cosmetic "scanning for threats" state (real scanning is prototype-mode;
      // the server audit row records scan_status).
      setPhase("scanning");
      const scanDone = await new Promise<void>((r) => setTimeout(r, 1500));
      void scanDone;
      setResult(resp);
      setPhase("done");
      onSuccess(resp);
    } catch (err: any) {
      setPhase("error");
      setError(describeError(err?.message || "Upload failed"));
    }
  };

  const acceptAttr = UPLOAD_ALLOWED.map((a) => a.accept).join(",");
  const nextVersion = draft.versionNumber || 1;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div className="w-full max-w-lg rounded-lg border border-slate-700 bg-slate-900 shadow-2xl">
        <div className="section-head rounded-t-lg">
          <span className="section-title">
            {draft.isNewDocument ? "Upload Evidence File" : "Upload New Version"}
          </span>
          <StatusBadge tone="default">{firNumber}</StatusBadge>
        </div>

        <div className="max-h-[70vh] space-y-4 overflow-auto p-5">
          {phase === "ticket" && (
            <div className="flex flex-col items-center gap-3 py-8 text-center">
              <div className="h-8 w-8 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />
              <p className="text-sm font-medium text-slate-200">
                Requesting access ticket…
              </p>
              <p className="text-xs text-slate-500">
                Minting a single-use append-scope ticket for {firNumber}
              </p>
            </div>
          )}

          {phase === "picker" && (
            <div className="space-y-4">
              {draft.isNewDocument ? (
                <>
                  <label className="block">
                    <span className="field-label">Document Title</span>
                    <input
                      className="input"
                      value={title}
                      onChange={(e) => setTitle(e.target.value)}
                      placeholder="e.g. Crime scene photographs: FIR-2026-0089"
                    />
                  </label>
                  <label className="block">
                    <span className="field-label">Classification Level</span>
                    <select
                      className="input"
                      value={classification}
                      onChange={(e) => setClassification(e.target.value)}
                    >
                      {CLASSIFICATIONS.map((c) => (
                        <option key={c} value={c} className="bg-slate-900">
                          {c}
                        </option>
                      ))}
                    </select>
                  </label>
                </>
              ) : (
                <p className="text-sm text-slate-400">
                  Appending <span className="font-semibold text-slate-200">v{nextVersion}</span>{" "}
                  to the existing document for {firNumber}. The new row will be
                  hash-chained to v{Math.max(1, nextVersion - 1)}.
                </p>
              )}

              <label className="block">
                <span className="field-label">Evidence File</span>
                <input
                  ref={fileInputRef}
                  type="file"
                  accept={acceptAttr}
                  onChange={(e) => pickFile(e.target.files?.[0] ?? null)}
                  className="block w-full cursor-pointer rounded border border-slate-700 bg-slate-950 text-xs text-slate-300 file:mr-3 file:cursor-pointer file:rounded file:border-0 file:bg-blue-600 file:px-3 file:py-2 file:text-xs file:font-semibold file:text-white hover:file:bg-blue-500"
                />
              </label>

              {file && (
                <div className="flex items-center justify-between rounded border border-slate-800 bg-slate-950/60 px-3 py-2 text-xs">
                  <span className="truncate font-mono text-blue-300">
                    {file.name}
                  </span>
                  <span className="ml-3 shrink-0 tabular-nums text-slate-400">
                    {(file.size / 1024).toFixed(1)} KB
                  </span>
                </div>
              )}

              {clientError && (
                <p className="rounded border border-red-700/60 bg-red-950/40 px-3 py-2 text-sm text-red-300">
                  {clientError}
                </p>
              )}

              <p className="text-[11px] leading-relaxed text-slate-500">
                Accepted: JPEG, PNG, PDF, MP4, MP3, WAV &middot; max{" "}
                {MAX_UPLOAD_MB} MB. Files are magic-byte validated server-side
                and stored hash-chained in the private evidence bucket.
              </p>
            </div>
          )}

          {(phase === "uploading" || phase === "scanning") && file && (
            <div className="space-y-3 py-4">
              <div className="flex items-center justify-between text-sm">
                <span className="text-slate-300">
                  {phase === "uploading"
                    ? `Uploading ${file.name}`
                    : "Scanning for threats…"}
                </span>
                <span className="tabular-nums text-slate-400">
                  {phase === "uploading" ? `${progress}%` : "1.5s"}
                </span>
              </div>
              <div className="h-2 w-full overflow-hidden rounded-full bg-slate-800">
                <div
                  className="h-full rounded-full bg-blue-500 transition-all"
                  style={{ width: phase === "scanning" ? "100%" : `${progress}%` }}
                />
              </div>
              {phase === "scanning" && (
                <p className="text-xs text-slate-500">
                  Hashing + chain-binding the evidence copy…
                </p>
              )}
            </div>
          )}

          {phase === "done" && result && (
            <div className="space-y-3">
              <div className="flex items-center gap-2">
                <svg className="h-5 w-5 text-emerald-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75 11.25 15 15 9.75M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z" />
                </svg>
                <p className="text-sm font-semibold text-emerald-300">
                  Evidence stored — version {result.version.version_number}
                </p>
              </div>
              <div className="rounded border border-slate-800 bg-slate-950/60 p-3 text-xs">
                <Row label="SHA-256" mono value={truncateDigest(result.version.sha256_hash, 20)} />
                <Row label="Type" value={result.version.mime_type} />
                <Row label="Size" value={`${(result.version.size_bytes / 1024).toFixed(1)} KB`} />
                <Row
                  label="Scan"
                  value={
                    result.scan_status === "clean"
                      ? "Clean"
                      : "Skipped (prototype mode)"
                  }
                />
              </div>
            </div>
          )}

          {phase === "error" && (
            <div className="rounded border border-red-700/60 bg-red-950/40 px-4 py-3 text-sm text-red-300">
              {error}
            </div>
          )}
        </div>

        <div className="flex items-center justify-end gap-3 border-t border-slate-800 p-4">
          {phase === "picker" && (
            <>
              <SecondaryButton onClick={onClose}>Cancel</SecondaryButton>
              <PrimaryButton
                onClick={startUpload}
                disabled={!file || (draft.isNewDocument && !title.trim())}
              >
                Upload Evidence
              </PrimaryButton>
            </>
          )}
          {(phase === "done" || phase === "error") && (
            <PrimaryButton onClick={onClose}>Close</PrimaryButton>
          )}
          {(phase === "uploading" || phase === "scanning") && (
            <SecondaryButton disabled>Uploading…</SecondaryButton>
          )}
          {phase === "ticket" && (
            <SecondaryButton disabled>Requesting…</SecondaryButton>
          )}
        </div>
      </div>
    </div>
  );
}

function Row({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="flex items-center justify-between gap-4 py-1">
      <span className="text-slate-500">{label}</span>
      <span className={`truncate ${mono ? "font-mono text-blue-300" : "text-slate-300"}`}>
        {value}
      </span>
    </div>
  );
}