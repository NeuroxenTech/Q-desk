"use client";

import { useEffect, useState } from "react";
import { requestTicket, mergeVersion } from "@/lib/api";
import { truncateDigest } from "@/lib/content";
import type {
  MergeResponse,
  Ticket,
  VersionTreeNode,
} from "@/lib/types";
import PrimaryButton from "@/components/ui/PrimaryButton";
import SecondaryButton from "@/components/ui/SecondaryButton";
import StatusBadge from "@/components/ui/StatusBadge";

type Phase = "ticket" | "choose" | "submitting" | "done" | "error";

function describeError(raw: string): string {
  const lower = raw.toLowerCase();
  if (lower.includes("ticket expired") || lower.includes("ticket not found"))
    return "Your access ticket expired — close and reopen this dialog to mint a fresh one.";
  if (lower.includes("session"))
    return "Your session is no longer valid — please re-authenticate.";
  if (lower.includes("same document"))
    return "The branch and merge target must belong to the same document.";
  return raw || "Merge failed. Please try again.";
}

export default function MergeDialog({
  open,
  onClose,
  sessionId,
  badge,
  firNumber,
  branch,
  mainlineCandidates,
  defaultTargetId,
  onSuccess,
}: {
  open: boolean;
  onClose: () => void;
  sessionId: string | null;
  badge: string;
  firNumber: string;
  branch: VersionTreeNode | null;
  mainlineCandidates: VersionTreeNode[];
  defaultTargetId?: string;
  onSuccess: (result: MergeResponse) => void;
}) {
  const [phase, setPhase] = useState<Phase>("ticket");
  const [ticket, setTicket] = useState<Ticket | null>(null);
  const [targetId, setTargetId] = useState<string>("");
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<MergeResponse | null>(null);

  useEffect(() => {
    if (!open) return;
    setPhase("ticket");
    setError(null);
    setResult(null);
    setTargetId(defaultTargetId || "");

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
        setPhase("choose");
      })
      .catch((err: any) => {
        if (cancelled) return;
        setPhase("error");
        setError(describeError(err?.message || "Ticket request failed"));
      });
    return () => {
      cancelled = true;
    };
  }, [open, sessionId, badge, firNumber, defaultTargetId]);

  if (!open || !branch) return null;

  const target = mainlineCandidates.find((c) => c.id === targetId);

  const submit = async () => {
    if (!ticket || !sessionId || !target) return;
    setPhase("submitting");
    try {
      const resp = await mergeVersion({
        sessionId,
        ticketId: ticket.id,
        firNumber,
        branchVersionId: branch.id,
        mergeTargetVersionId: target.id,
      });
      setResult(resp);
      setPhase("done");
      onSuccess(resp);
    } catch (err: any) {
      setPhase("error");
      setError(describeError(err?.message || "Merge failed"));
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div className="w-full max-w-lg rounded-lg border border-slate-700 bg-slate-900 shadow-2xl">
        <div className="section-head rounded-t-lg">
          <span className="section-title">Merge into Mainline</span>
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

          {phase === "choose" && (
            <div className="space-y-4">
              <div className="rounded border border-slate-800 bg-slate-950/60 p-3 text-xs">
                <p className="text-slate-500">
                  Merging branch{" "}
                  <span className="font-bold text-blue-300">{branch.tree_path}</span>
                </p>
                <p className="mt-1 font-mono text-[11px] text-slate-400">
                  {truncateDigest(branch.sha256_hash, 12)}
                </p>
              </div>

              <label className="block">
                <span className="field-label">Merge into mainline version</span>
                <select
                  className="input"
                  value={targetId}
                  onChange={(e) => setTargetId(e.target.value)}
                >
                  {mainlineCandidates.length === 0 && (
                    <option value="">No mainline versions available</option>
                  )}
                  {mainlineCandidates.map((c) => (
                    <option key={c.id} value={c.id} className="bg-slate-900">
                      {c.tree_path} &middot; {truncateDigest(c.sha256_hash, 8)}
                    </option>
                  ))}
                </select>
                {defaultTargetId && (
                  <p className="mt-1 text-[11px] text-slate-500">
                    Defaults to the branch&apos;s original source; you can pick a
                    later mainline version if the mainline has moved on.
                  </p>
                )}
              </label>

              {target && (
                <p className="text-xs leading-relaxed text-slate-400">
                  A new <span className="font-semibold text-emerald-300">merge point</span>{" "}
                  will be created under <span className="font-mono">{target.tree_path}</span>,
                  chained to both this branch and the target mainline.
                </p>
              )}
            </div>
          )}

          {phase === "submitting" && (
            <div className="flex flex-col items-center gap-3 py-8 text-center">
              <div className="h-8 w-8 animate-spin rounded-full border-2 border-emerald-500 border-t-transparent" />
              <p className="text-sm font-medium text-slate-200">Merging branch…</p>
            </div>
          )}

          {phase === "done" && result && (
            <div className="space-y-3">
              <div className="flex items-center gap-2">
                <svg className="h-5 w-5 text-emerald-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75 11.25 15 15 9.75M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z" />
                </svg>
                <p className="text-sm font-semibold text-emerald-300">
                  Merged — {result.version.tree_path}
                </p>
              </div>
              <div className="rounded border border-slate-800 bg-slate-950/60 p-3 font-mono text-xs text-slate-400">
                <span className="text-slate-500">merged_from_hash: </span>
                <span className="text-emerald-300/90">
                  {truncateDigest(result.merged_from_hash, 12)}
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
          {phase === "choose" && (
            <>
              <SecondaryButton onClick={onClose}>Cancel</SecondaryButton>
              <PrimaryButton onClick={submit} disabled={!target}>
                Merge into {target?.tree_path || "…"}
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
