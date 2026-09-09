"use client";

import { useEffect, useState, Suspense, useCallback } from "react";
import { useRouter } from "next/navigation";
import TopNav from "@/components/TopNav";
import Card from "@/components/ui/Card";
import StatusBadge from "@/components/ui/StatusBadge";
import PrimaryButton from "@/components/ui/PrimaryButton";
import SecondaryButton from "@/components/ui/SecondaryButton";
import { useOfficerSession } from "@/lib/useOfficerSession";
import {
  fetchDownloads,
  decideDownload,
  executeDownload,
} from "@/lib/api";
import type {
  DownloadRequest,
  DownloadStatus,
  DownloadsListResponse,
} from "@/lib/types";

type ActionState =
  | "idle"
  | "deciding"
  | "executing"
  | "error";

const STATUS_TONE: Record<DownloadStatus, "default" | "success" | "warning" | "danger"> = {
  pending: "warning",
  approved: "success",
  rejected: "danger",
  expired: "default",
  downloaded: "default",
};

function statusLabel(s: DownloadStatus): string {
  switch (s) {
    case "pending":
      return "Awaiting Approval";
    case "approved":
      return "Approved — Ready";
    case "rejected":
      return "Rejected";
    case "expired":
      return "Expired";
    case "downloaded":
      return "Downloaded";
  }
}

// isExecutable mirrors the backend's one-shot rule: only fully-approved
// requests can be executed. (Auto-expiry only ever kills 'pending' requests; an
// approved request stays downloadable until the requester uses the one shot.)
function isExecutable(r: DownloadRequest): boolean {
  return r.status === "approved";
}

export default function DownloadsPage() {
  return (
    <Suspense fallback={null}>
      <DownloadsInner />
    </Suspense>
  );
}

function DownloadsInner() {
  const router = useRouter();
  const { loading, badge, role, sessionId } = useOfficerSession();

  const [data, setData] = useState<DownloadsListResponse | null>(null);
  const [loadingData, setLoadingData] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [action, setAction] = useState<ActionState>("idle");
  const [busyId, setBusyId] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!sessionId || !badge) return;
    setLoadingData(true);
    setError(null);
    try {
      setData(await fetchDownloads(sessionId, badge));
    } catch (err: any) {
      setError(err?.message || "Failed to load download requests");
    } finally {
      setLoadingData(false);
    }
  }, [sessionId, badge]);

  useEffect(() => {
    if (sessionId && badge) load();
  }, [sessionId, badge, load]);

  // Approver inbox: the spec has no push channel, so the page polls the pending
  // query every 10s so a freshly submitted request appears for a decision and a
  // colleague's approval updates the counters.
  useEffect(() => {
    if (!data?.can_approve) return;
    const id = setInterval(load, 10000);
    return () => clearInterval(id);
  }, [data?.can_approve, load]);

  useEffect(() => {
    if (action === "error" && error) {
      const t = setTimeout(() => {
        setError(null);
        setAction("idle");
      }, 6000);
      return () => clearTimeout(t);
    }
  }, [action, error]);

  if (loading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-slate-950">
        <div className="h-8 w-8 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />
      </div>
    );
  }

  const decide = async (r: DownloadRequest, decision: "approved" | "rejected") => {
    if (!sessionId || !badge || !r) return;
    setBusyId(r.id);
    setAction("deciding");
    setError(null);
    try {
      await decideDownload(sessionId, badge, r.id, decision);
      await load();
    } catch (err: any) {
      setAction("error");
      setError(err?.message || `Could not ${decision} this request`);
    } finally {
      setBusyId(null);
    }
  };

  const download = async (r: DownloadRequest) => {
    if (!sessionId || !badge || !r) return;
    setBusyId(r.id);
    setAction("executing");
    setError(null);
    try {
      const resp = await executeDownload(sessionId, badge, r.id);
      // Follow the short-lived signed URL immediately. The backend marks the
      // request 'downloaded' before it returns, so this is the one shot.
      const link = document.createElement("a");
      link.href = resp.url;
      link.download = resp.filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
      await load();
    } catch (err: any) {
      setAction("error");
      setError(err?.message || "Download failed");
      await load();
    } finally {
      setBusyId(null);
    }
  };

  const history = data?.requests || [];
  const pending = data?.pending || [];

  return (
    <div className="flex min-h-screen flex-col bg-slate-950">
      <TopNav badge={badge} role={role} />
      <main className="mx-auto w-full max-w-7xl flex-1 px-6 py-6">
        <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
          <div>
            <h1 className="text-xl font-bold text-white">Downloads</h1>
            <p className="mt-0.5 text-sm text-slate-400">
              Dual-approved chain-of-custody export of evidence packages.
            </p>
          </div>
          <StatusBadge tone={history.some((r) => r.status === "approved") ? "success" : "default"}>
            {history.length} request{history.length === 1 ? "" : "s"}
          </StatusBadge>
        </div>

        {error && (
          <div className="mb-5 rounded border border-red-700/60 bg-red-950/40 px-4 py-3 text-sm text-red-300">
            {error}
          </div>
        )}

        {data?.can_approve && (
          <Card className="mb-6">
            <div className="section-head">
              <span className="section-title">Approval Inbox</span>
              <span className="text-[11px] uppercase tracking-wider text-slate-500">
                Polled every 10s
              </span>
            </div>
            {pending.length === 0 ? (
              <p className="p-8 text-center text-sm text-slate-500">
                No download requests are waiting on your decision.
              </p>
            ) : (
              <div className="space-y-3 p-5">
                {pending.map((r) => (
                  <div
                    key={r.id}
                    className="rounded border border-slate-800 bg-slate-950/60 p-4"
                  >
                    <div className="flex flex-wrap items-start justify-between gap-3">
                      <div className="min-w-0">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="font-mono text-sm text-blue-300">
                            {r.fir_number || r.document_id.slice(0, 12)}
                          </span>
                          <span className="truncate text-sm text-slate-200">
                            {r.title}
                          </span>
                        </div>
                        <p className="mt-1 text-xs leading-relaxed text-slate-400">
                          <span className="font-semibold text-slate-300">
                            {r.requester_name} ({r.requested_by_badge})
                          </span>{" "}
                          requested on {new Date(r.requested_at).toLocaleString()}
                          <br />
                          <span className="text-slate-500">
                            Reason: {r.reason}
                          </span>
                        </p>
                      </div>
                      <div className="flex shrink-0 items-center gap-2">
                        <StatusBadge tone="warning">
                          {r.approved_count}/{r.required_approvals} approved
                        </StatusBadge>
                      </div>
                    </div>
                    <div className="mt-3 flex items-center justify-between gap-3">
                      <p className="text-[11px] text-amber-200/70">
                        Approvals expire{" "}
                        {new Date(r.expires_at).toLocaleString()} — a request
                        expires if it is not fully approved in time.
                      </p>
                      <div className="flex gap-2">
                        <SecondaryButton
                          onClick={() => decide(r, "approved")}
                          disabled={busyId !== null}
                          className="text-xs"
                        >
                          Approve
                        </SecondaryButton>
                        <button
                          onClick={() => decide(r, "rejected")}
                          disabled={busyId !== null}
                          className="rounded border border-red-700/60 px-3 py-1.5 text-xs font-semibold text-red-300 transition-colors hover:bg-red-950/40"
                        >
                          Reject
                        </button>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </Card>
        )}

        {loadingData && history.length === 0 && (
          <div className="mb-5 flex justify-center py-6">
            <div className="h-8 w-8 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />
          </div>
        )}

        <Card>
          <div className="section-head">
            <span className="section-title">My Download Requests</span>
            <span className="text-[11px] uppercase tracking-wider text-slate-500">
              {history.length} row{history.length === 1 ? "" : "s"}
            </span>
          </div>
          {history.length === 0 ? (
            <p className="p-8 text-center text-sm text-slate-500">
              You have not requested any chain-of-custody downloads yet. Open a
              case and use{" "}
              <span className="font-semibold text-blue-300">
                Request Download / Print
              </span>{" "}
              in the evidence viewer.
            </p>
          ) : (
            <div className="tbl-head grid-cols-[1.1fr_1.6fr_0.9fr_0.9fr_1.4fr_1.3fr_0.9fr]">
              <span>FIR</span>
              <span>Document</span>
              <span>Approvals</span>
              <span>Status</span>
              <span>Requested / Expires</span>
              <span>Downloaded</span>
              <span>Action</span>
            </div>
          )}
          {history.map((r) => (
            <div
              key={r.id}
              className="tbl-row grid-cols-[1.1fr_1.6fr_0.9fr_0.9fr_1.4fr_1.3fr_0.9fr]"
            >
              <span className="font-mono text-sm text-blue-300">
                {r.fir_number || "—"}
              </span>
              <span
                className="truncate text-sm text-slate-200"
                title={r.reason}
              >
                {r.title || r.document_id}
              </span>
              <span className="tabular-nums text-slate-300">
                {r.approved_count}/{r.required_approvals}
              </span>
              <span>
                <StatusBadge tone={STATUS_TONE[r.status]}>
                  {statusLabel(r.status)}
                </StatusBadge>
              </span>
              <span className="text-xs tabular-nums text-slate-400">
                {new Date(r.requested_at).toLocaleString()}
                <br />
                <span className="text-slate-600">
                  exp {new Date(r.expires_at).toLocaleString()}
                </span>
              </span>
              <span className="text-xs text-slate-400">
                {r.downloaded_at
                  ? new Date(r.downloaded_at).toLocaleString()
                  : "—"}
              </span>
              <span>
                {r.status === "downloaded" ? (
                  <span className="text-[11px] text-slate-600">
                    one-shot used; new request required
                  </span>
                ) : isExecutable(r) ? (
                  <PrimaryButton
                    onClick={() => download(r)}
                    disabled={busyId !== null}
                    className="text-xs"
                  >
                    {busyId === r.id ? "Packaging…" : "Download now"}
                  </PrimaryButton>
                ) : (
                  <span className="text-[11px] text-slate-600">
                    {r.status === "rejected"
                      ? "rejected — contact a supervisor"
                      : r.status === "expired"
                        ? "expired — submit again"
                        : "awaiting approvals"}
                  </span>
                )}
              </span>
            </div>
          ))}
        </Card>
      </main>

      <footer className="border-t border-slate-800 px-6 py-3 text-center">
        <p className="text-[11px] font-medium uppercase tracking-widest text-slate-600">
          Quantum-Secure Evidence Workstation &middot; Dual-Approval Export
        </p>
      </footer>
    </div>
  );
}