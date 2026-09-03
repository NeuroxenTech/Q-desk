"use client";

import { useEffect, useRef, useState } from "react";
import { requestTicket, branchVersion, UploadProgressHandler } from "@/lib/api";
import { UPLOAD_ALLOWED, MAX_UPLOAD_MB, truncateDigest } from "@/lib/content";
import type {
  BranchResponse,
  Ticket,
  VersionTreeNode,
} from "@/lib/types";
import PrimaryButton from "@/components/ui/PrimaryButton";
import SecondaryButton from "@/components/ui/SecondaryButton";
import StatusBadge from "@/components/ui/StatusBadge";

type Phase =
  | "ticket"
  | "compose"
  | "submitting"
  | "done"
  | "error";

type BranchKind = "annotation" | "file";

// Error mapping mirrors the upload dialog's describeError so the user gets the
// same friendly copy (see backend sentinel errors).
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
  if (lower.includes("annotation") && lower.includes("required"))
    return "Enter an annotation or choose a file to branch.";
  return raw || "Branch failed. Please try again.";
}

export default function BranchDialog({
  open,
  onClose,
  sessionId,
  badge,
  firNumber,
  source,
  onSuccess,
}: {
  open: boolean;
  onClose: () => void;
  sessionId: string | null;
  badge: string;
  firNumber: string;
  source: VersionTreeNode | null;
  onSuccess: (result: BranchResponse) => void;
}) {
  const [phase, setPhase] = useState<Phase>("ticket");
  const [kind, setKind] = useState<BranchKind>("annotation");
  const [annotation, setAnnotation] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [ticket, setTicket] = useState<Ticket | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [clientError, setClientError] = useState<string | null>(null);
  const [progress, setProgress] = useState(0);
  const [result, setResult] = useState<BranchResponse | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (!open) return;
    setPhase("ticket");
    setError(null);
    setClientError(null);
    setFile(null);
    setAnnotation("");
    setResult(null);
    setProgress(0);

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
        setPhase("compose");
      })
      .catch((err: any) => {
        if (cancelled) return;
        setPhase("error");
        setError(describeError(err?.message || "Ticket request failed"));
      });
    return () => {
      cancelled = true;
    };
  }, [open, sessionId, badge, firNumber]);

  if (!open || !source) return null;

  const pickFile = (f: File | null) => {
    setClientError(null);
    if (!f) {
      setFile(null);
      return;
    }
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

  const canSubmit =
    kind === "annotation"
      ? annotation.trim().length > 0
      : file !== null;

  const submit = async () => {
    if (!ticket || !sessionId || !canSubmit) return;
    setProgress(0);
    setPhase("submitting");
    const onProgress: UploadProgressHandler = (p) => setProgress(p);
    try {
      const resp = await branchVersion(
        {
          sessionId,
          ticketId: ticket.id,
          firNumber,
          sourceVersionId: source.id,
          annotation: kind === "annotation" ? annotation : undefined,
          file: kind === "file" ? file || undefined : undefined,
        },
        kind === "file" ? onProgress : undefined
      );
      setResult(resp);
      setPhase("done");
      onSuccess(resp);
    } catch (err: any) {
      setPhase("error");
      setError(describeError(err?.message || "Branch failed"));
    }
  };

  const acceptAttr = UPLOAD_ALLOWED.map((a) => a.accept).join(",");

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div className="w-full max-w-lg rounded-lg border border-slate-700 bg-slate-900 shadow-2xl">
        <div className="section-head rounded-t-lg">
          <span className="section-title">Branch for Annotation</span>
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

          {phase === "compose" && (
            <div className="space-y-4">
              <div className="rounded border border-slate-800 bg-slate-950/60 p-3 text-xs">
                <p className="text-slate-500">
                  Branching from{" "}
                  <span className="font-bold text-blue-300">
                    {source.tree_path}
                  </span>
                </p>
                <p className="mt-1 font-mono text-[11px] text-slate-400">
                  {truncateDigest(source.sha256_hash, 12)}
                </p>
              </div>

              <div className="flex gap-2">
                <button
                  onClick={() => setKind("annotation")}
                  className={`flex-1 rounded border px-3 py-2 text-xs font-semibold transition-colors ${
                    kind === "annotation"
                      ? "border-blue-500/60 bg-blue-500/10 text-blue-200"
                      : "border-slate-700 text-slate-400 hover:bg-slate-800/50"
                  }`}
                >
                  Text Annotation
                </button>
                <button
                  onClick={() => setKind("file")}
                  className={`flex-1 rounded border px-3 py-2 text-xs font-semibold transition-colors ${
                    kind === "file"
                      ? "border-blue-500/60 bg-blue-500/10 text-blue-200"
                      : "border-slate-700 text-slate-400 hover:bg-slate-800/50"
                  }`}
                >
                  File Upload
                </button>
              </div>

              {kind === "annotation" ? (
                <label className="block">
                  <span className="field-label">Annotation</span>
                  <textarea
                    className="input min-h-[120px]"
                    value={annotation}
                    onChange={(e) => setAnnotation(e.target.value)}
                    placeholder="e.g. Forensic comparison notes: fibres match trace sample A-114 (parallel analysis, not superseding the original)."
                  />
                </label>
              ) : (
                <label className="block">
                  <span className="field-label">Evidence File</span>
                  <input
                    ref={fileInputRef}
                    type="file"
                    accept={acceptAttr}
                    onChange={(e) => pickFile(e.target.files?.[0] ?? null)}
                    className="block w-full cursor-pointer rounded border border-slate-700 bg-slate-950 text-xs text-slate-300 file:mr-3 file:cursor-pointer file:rounded file:border-0 file:bg-blue-600 file:px-3 file:py-2 file:text-xs file:font-semibold file:text-white hover:file:bg-blue-500"
                  />
                  {file && (
                    <div className="mt-2 flex items-center justify-between rounded border border-slate-800 bg-slate-950/60 px-3 py-2 text-xs">
                      <span className="truncate font-mono text-blue-300">
                        {file.name}
                      </span>
                      <span className="ml-3 shrink-0 tabular-nums text-slate-400">
                        {(file.size / 1024).toFixed(1)} KB
                      </span>
                    </div>
                  )}
                  <p className="mt-2 text-[11px] leading-relaxed text-slate-500">
                    Accepted: JPEG, PNG, PDF, MP4, MP3, WAV &middot; max{" "}
                    {MAX_UPLOAD_MB} MB. Files are magic-byte validated
                    server-side and stored hash-chained in the private bucket.
                  </p>
                </label>
              )}

              {clientError && (
                <p className="rounded border border-red-700/60 bg-red-950/40 px-3 py-2 text-sm text-red-300">
                  {clientError}
                </p>
              )}
            </div>
          )}

          {phase === "submitting" && (
            <div className="space-y-3 py-4">
              <div className="flex items-center justify-between text-sm">
                <span className="text-slate-300">
                  {kind === "file" && file
                    ? `Uploading ${file.name}`
                    : "Creating branch…"}
                </span>
                {kind === "file" && (
                  <span className="tabular-nums text-slate-400">{progress}%</span>
                )}
              </div>
              {kind === "file" && (
                <div className="h-2 w-full overflow-hidden rounded-full bg-slate-800">
                  <div
                    className="h-full rounded-full bg-blue-500 transition-all"
                    style={{ width: `${progress}%` }}
                  />
                </div>
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
                  Branch created — {result.version.tree_path}
                </p>
              </div>
              <div className="rounded border border-slate-800 bg-slate-950/60 p-3 font-mono text-xs text-slate-400">
                <span className="text-slate-500">parent_sha256_hash: </span>
                <span className="text-emerald-300/90">
                  {truncateDigest(result.parent_sha256_hash, 12)}
                </span>
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
          {phase === "compose" && (
            <>
              <SecondaryButton onClick={onClose}>Cancel</SecondaryButton>
              <PrimaryButton onClick={submit} disabled={!canSubmit}>
                Create Branch
              </PrimaryButton>
            </>
          )}
          {(phase === "done" || phase === "error") && (
            <PrimaryButton onClick={onClose}>Close</PrimaryButton>
          )}
          {phase === "submitting" && (
            <SecondaryButton disabled>Working…</SecondaryButton>
          )}
          {phase === "ticket" && (
            <SecondaryButton disabled>Requesting…</SecondaryButton>
          )}
        </div>
      </div>
    </div>
  );
}
