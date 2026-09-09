"use client";

import { useEffect, useState } from "react";
import { requestDownload } from "@/lib/api";
import type {
  DownloadRequest,
  DownloadRequestResponse,
} from "@/lib/types";
import PrimaryButton from "@/components/ui/PrimaryButton";
import SecondaryButton from "@/components/ui/SecondaryButton";
import StatusBadge from "@/components/ui/StatusBadge";

type Phase = "form" | "submitting" | "done" | "error";

// Error copy mirrors the project's describeError convention so the same
// backend sentinel strings read naturally to the officer.
function describeError(raw: string): string {
  const lower = raw.toLowerCase();
  if (lower.includes("no case assignment"))
    return "You have no case assignment for this document, so you cannot request its download.";
  if (lower.includes("system admin"))
    return "Platform administrators cannot request evidence downloads.";
  if (lower.includes("session"))
    return "Your session is no longer valid — please re-authenticate.";
  if (lower.includes("reason"))
    return "A reason is required for a chain-of-custody download.";
  return raw || "Download request failed. Please try again.";
}

export default function DownloadRequestDialog({
  open,
  onClose,
  sessionId,
  badge,
  firNumber,
  documentId,
  onSuccess,
}: {
  open: boolean;
  onClose: () => void;
  sessionId: string | null;
  badge: string;
  firNumber: string;
  documentId: string | null;
  onSuccess: (created: DownloadRequest) => void;
}) {
  const [phase, setPhase] = useState<Phase>("form");
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<DownloadRequest | null>(null);

  useEffect(() => {
    if (!open) return;
    setPhase("form");
    setReason("");
    setError(null);
    setResult(null);
  }, [open]);

  if (!open) return null;

  const canSubmit = reason.trim().length > 0;

  const submit = async () => {
    if (!sessionId || !documentId || !canSubmit) return;
    setPhase("submitting");
    setError(null);
    try {
      const resp: DownloadRequestResponse = await requestDownload(
        sessionId,
        badge,
        documentId,
        reason
      );
      setResult(resp.request);
      setPhase("done");
      onSuccess(resp.request);
    } catch (err: any) {
      setPhase("error");
      setError(describeError(err?.message || "Download request failed"));
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div className="w-full max-w-lg rounded-lg border border-slate-700 bg-slate-900 shadow-2xl">
        <div className="section-head rounded-t-lg">
          <span className="section-title">Request Evidence Download</span>
          <StatusBadge tone="default">{firNumber}</StatusBadge>
        </div>

        <div className="max-h-[70vh] space-y-4 overflow-auto p-5">
          {phase === "form" && (
            <div className="space-y-4">
              <div className="rounded border border-slate-800 bg-slate-950/60 p-3 text-xs leading-relaxed text-slate-400">
                <p>
                  A chain-of-custody download packages every version of this
                  document together with a signed certificate. It must be
                  approved by{" "}
                  <span className="font-semibold text-slate-200">
                    two distinct supervisors
                  </span>{" "}
                  before it becomes executable. Your request is submitted to
                  the supervisors&apos; Downloads inbox now.
                </p>
                <p className="mt-2 text-[11px] text-slate-500">
                  The requester can never approve their own request, and a
                  single rejection voids it.
                </p>
              </div>
              <label className="block">
                <span className="field-label">Reason for download</span>
                <textarea
                  className="input min-h-[110px]"
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                  placeholder="e.g. Referral of evidence package to the District Prosecutor's Office for charge certification."
                  maxLength={2000}
                />
                <span className="mt-1 block text-right text-[10px] text-slate-600">
                  {reason.length}/2000
                </span>
              </label>
            </div>
          )}

          {phase === "submitting" && (
            <div className="flex flex-col items-center gap-3 py-8 text-center">
              <div className="h-8 w-8 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />
              <p className="text-sm font-medium text-slate-200">
                Submitting download request…
              </p>
              <p className="text-xs text-slate-500">
                The request is recorded with your badge and routed to the
                supervisor inbox.
              </p>
            </div>
          )}

          {phase === "done" && result && (
            <div className="space-y-3">
              <div className="flex items-center gap-2">
                <svg className="h-5 w-5 text-emerald-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75 11.25 15 15 9.75M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z" />
                </svg>
                <p className="text-sm font-semibold text-emerald-300">
                  Request submitted — awaiting dual approval
                </p>
              </div>
              <div className="rounded border border-slate-800 bg-slate-950/60 p-3 text-xs text-slate-400">
                <p>
                  <span className="font-mono text-slate-500">request_id: </span>
                  <span className="font-mono text-emerald-300/90">
                    {result.id}
                  </span>
                </p>
                <p className="mt-1">
                  Track approval progress in the{" "}
                  <span className="font-semibold text-blue-300">Downloads</span>{" "}
                  tab. You can execute the download as soon as{" "}
                  {result.required_approvals} approvals are recorded.
                </p>
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
          {phase === "form" && (
            <>
              <SecondaryButton onClick={onClose}>Cancel</SecondaryButton>
              <PrimaryButton onClick={submit} disabled={!canSubmit || !documentId}>
                Submit Request
              </PrimaryButton>
            </>
          )}
          {(phase === "done" || phase === "error") && (
            <PrimaryButton onClick={onClose}>Close</PrimaryButton>
          )}
          {phase === "submitting" && (
            <SecondaryButton disabled>Submitting…</SecondaryButton>
          )}
        </div>
      </div>
    </div>
  );
}