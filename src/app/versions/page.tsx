"use client";

import { useEffect, useState, Suspense } from "react";
import type { MouseEvent } from "react";
import { useSearchParams } from "next/navigation";
import TopNav from "@/components/TopNav";
import Card from "@/components/ui/Card";
import StatusBadge from "@/components/ui/StatusBadge";
import PrimaryButton from "@/components/ui/PrimaryButton";
import { useOfficerSession } from "@/lib/useOfficerSession";
import { fetchVersions } from "@/lib/api";
import { verifyVersionChain } from "@/lib/crypto";
import ContentTypeIcon from "@/components/ui/ContentTypeIcon";
import type { VersionRow } from "@/lib/types";

// A little helper to shorten a 64-char hex digest for display.
function trunc(digest: string | undefined, n = 16): string {
  if (!digest) return "—";
  if (digest.length <= n * 2 + 1) return digest;
  return `${digest.slice(0, n)}…${digest.slice(-n)}`;
}

type ChainState = "valid" | "invalid" | "unverified";

interface DisplayRow extends VersionRow {
  chainState: ChainState; // verified locally against backend fields
}

export default function VersionsPage() {
  return (
    <Suspense fallback={null}>
      <VersionsInner />
    </Suspense>
  );
}

function VersionsInner() {
  const params = useSearchParams();
  const { loading, badge, role, sessionId } = useOfficerSession();

  const [rows, setRows] = useState<DisplayRow[]>([]);
  const [loadingData, setLoadingData] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Filter inputs.
  const [firNumber, setFirNumber] = useState(
    params.get("fir") || ""
  );
  const [badgeFilter, setBadgeFilter] = useState("");
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");
  // Number of broken chains detected on the current view.
  const [tamperedCount, setTamperedCount] = useState(0);

  const load = async () => {
    if (!sessionId || !badge) return;
    setLoadingData(true);
    setError(null);
    try {
      const resp = await fetchVersions(sessionId, badge, {
        firNumber: firNumber.trim() || undefined,
        badgeNumber: badgeFilter.trim() || undefined,
        dateFrom: dateFrom ? new Date(dateFrom).toISOString() : undefined,
        dateTo: dateTo ? new Date(dateTo).toISOString() : undefined,
      });

      // Independently verify every returned row's chain on the client.
      const verified = await Promise.all(
        resp.versions.map(async (v) => {
          const state = await verifyVersionChain(v);
          return { ...v, chainState: state as ChainState };
        })
      );
      setRows(verified);
      setTamperedCount(
        verified.filter((r) => r.chainState === "invalid").length
      );
    } catch (err: any) {
      setError(err?.message || "Failed to load version history");
    } finally {
      setLoadingData(false);
    }
  };

  // Auto-load on mount and whenever the session becomes available.
  useEffect(() => {
    if (sessionId && badge) load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, badge]);

  if (loading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-slate-950">
        <div className="h-8 w-8 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />
      </div>
    );
  }

  return (
    <div className="flex min-h-screen flex-col bg-slate-950">
      <TopNav badge={badge} role={role} />
      <main className="mx-auto w-full max-w-7xl flex-1 px-6 py-6">
        <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
          <div>
            <h1 className="text-xl font-bold text-white">Version History</h1>
            <p className="mt-0.5 text-sm text-slate-400">
              Cross-case, read-only audit of document versions and their hash
              chains.
            </p>
          </div>
          <StatusBadge tone={tamperedCount > 0 ? "danger" : "success"}>
            {tamperedCount > 0
              ? `${tamperedCount} Broken Chain${tamperedCount > 1 ? "s" : ""}`
              : "Chains Verified"}
          </StatusBadge>
        </div>

        {/* Filters */}
        <Card className="mb-6">
          <div className="section-head">
            <span className="section-title">Filters</span>
            <span className="text-[11px] uppercase tracking-wider text-slate-500">
              Read-only
            </span>
          </div>
          <div className="grid grid-cols-2 gap-4 p-5 md:grid-cols-5">
            <label className="block">
              <span className="field-label">FIR Number</span>
              <input
                className="input"
                value={firNumber}
                onChange={(e) => setFirNumber(e.target.value)}
                placeholder="FIR-2026-0089"
              />
            </label>
            <label className="block">
              <span className="field-label">Badge Number</span>
              <input
                className="input"
                value={badgeFilter}
                onChange={(e) => setBadgeFilter(e.target.value)}
                placeholder="IND-IO-402"
              />
            </label>
            <label className="block">
              <span className="field-label">From</span>
              <input
                type="date"
                className="input"
                value={dateFrom}
                onChange={(e) => setDateFrom(e.target.value)}
              />
            </label>
            <label className="block">
              <span className="field-label">To</span>
              <input
                type="date"
                className="input"
                value={dateTo}
                onChange={(e) => setDateTo(e.target.value)}
              />
            </label>
            <div className="flex items-end gap-2">
              <PrimaryButton
                onClick={load}
                disabled={loadingData || !sessionId}
                className="w-full"
              >
                {loadingData ? "Loading…" : "Apply"}
              </PrimaryButton>
            </div>
          </div>
        </Card>

        {error && (
          <div className="mb-5 rounded border border-red-700/60 bg-red-950/40 px-4 py-3 text-sm text-red-300">
            {error}
          </div>
        )}

        <Card>
          <div className="section-head">
            <span className="section-title">Document Versions</span>
            <span className="text-[11px] uppercase tracking-wider text-slate-500">
              {rows.length} row{rows.length === 1 ? "" : "s"}
            </span>
          </div>
          <div className="tbl-head grid-cols-[1fr_0.6fr_1.2fr_1.2fr_0.9fr_0.9fr_1.3fr_0.8fr]">
            <span>FIR</span>
            <span>Version</span>
            <span>SHA-256</span>
            <span>Parent SHA-256</span>
            <span>Type</span>
            <span>Author</span>
            <span>Created At</span>
            <span>Chain</span>
          </div>
          {rows.length === 0 ? (
            <p className="p-8 text-center text-sm text-slate-500">
              No document versions match the current filters.
            </p>
          ) : (
            rows.map((r, i) => {
              const broken = r.chainState === "invalid";
              return (
                <div
                  key={`${r.fir_number}-${r.version_number}-${i}`}
                  className={`tbl-row grid-cols-[1fr_0.6fr_1.2fr_1.2fr_0.9fr_0.9fr_1.3fr_0.8fr] ${
                    broken
                      ? "border-amber-500/40 bg-amber-500/5 hover:bg-amber-500/10"
                      : ""
                  }`}
                >
                  <span className="font-mono text-sm text-blue-300">
                    {r.fir_number}
                  </span>
                  <span className="tabular-nums text-slate-300">
                    {r.tree_path || `v${r.version_number}`}
                  </span>
                  <span
                    className={`font-mono text-xs ${
                      broken ? "text-amber-300" : "text-slate-400"
                    }`}
                    title={r.sha256_hash}
                  >
                    {trunc(r.sha256_hash)}
                  </span>
                  <ParentHashCell
                    label="parent"
                    hash={r.parent_sha256_hash}
                    secondLabel="merged_from"
                    secondHash={r.merged_from_hash}
                  />
                  <span className="flex items-center gap-2">
                    <KindBadge kind={r.kind} />
                    {r.tree_path && (
                      <span className="font-mono text-[11px] text-slate-500">
                        {r.tree_path}
                      </span>
                    )}
                    {r.content_type ? (
                      <ContentTypeIcon mime={r.content_type} />
                    ) : null}
                  </span>
                  <span className="font-mono text-xs text-slate-300">
                    {r.badge_number}
                  </span>
                  <span className="text-xs tabular-nums text-slate-400">
                    {new Date(r.created_at).toLocaleString()}
                  </span>
                  <ChainBadge state={r.chainState} />
                </div>
              );
            })
          )}
        </Card>

        {/* Tamper-warning explanation — amber/orange, deliberately distinct from
            the red breach screen to avoid confusion. */}
        {tamperedCount > 0 && (
          <div className="mt-6 rounded-md border border-amber-500/50 bg-amber-500/10 px-5 py-4">
            <p className="text-sm font-semibold text-amber-300">
              Chain Integrity Warning
            </p>
            <p className="mt-1 text-sm leading-relaxed text-amber-200/80">
              {tamperedCount} version row{tamperedCount === 1 ? "" : "s"} failed
              hash verification — the stored sha256 does not match
              SHA256(content_hash ‖ parent_hash ‖ badge ‖ created_at). This may
              indicate tampering with the evidence chain.
            </p>
          </div>
        )}
      </main>

      <footer className="border-t border-slate-800 px-6 py-3 text-center">
        <p className="text-[11px] font-medium uppercase tracking-widest text-slate-600">
          Quantum-Secure Evidence Workstation &middot; Classified Access
        </p>
      </footer>
    </div>
  );
}

// Type badge: Mainline / Branch / Merge classification carried from the
// backend's kind field. Falls back to showing nothing if the backend didn't
// provide one (e.g. legacy rows).
function KindBadge({ kind }: { kind?: string }) {
  if (kind === "merge") {
    return (
      <span className="inline-flex items-center rounded bg-emerald-500/15 px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wide text-emerald-300">
        merged
      </span>
    );
  }
  if (kind === "branch") {
    return (
      <span className="inline-flex items-center rounded bg-blue-500/15 px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wide text-blue-300">
        branch
      </span>
    );
  }
  if (kind === "mainline") {
    return (
      <span className="inline-flex items-center rounded bg-slate-500/15 px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wide text-slate-300">
        mainline
      </span>
    );
  }
  return null;
}

// Parent-hash cell: renders the single parent digest (already truncated), or —
// for merge rows — both the parent and the merged-from digest, each clickable
// to copy the full hash to the clipboard.
function ParentHashCell({
  label,
  hash,
  secondLabel,
  secondHash,
}: {
  label: string;
  hash: string;
  secondLabel?: string;
  secondHash?: string;
}) {
  return (
    <div className="space-y-0.5">
      <HashChip label={label} hash={hash} />
      {secondHash && <HashChip label={secondLabel || "merged_from"} hash={secondHash} />}
    </div>
  );
}

function HashChip({ label, hash }: { label: string; hash: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async (e: MouseEvent) => {
    e.stopPropagation();
    try {
      await navigator.clipboard.writeText(hash);
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    } catch {
      /* clipboard unavailable */
    }
  };
  return (
    <button
      onClick={copy}
      className="group inline-flex items-center gap-1 font-mono text-xs text-slate-400 transition-colors hover:text-blue-300"
      title={
        copied
          ? "Copied!"
          : `${label}: ${hash}\nClick to copy the full hash. Browsing the version history or across-merges requires the FIR filter.`
      }
    >
      {copied ? (
        <span className="text-emerald-300">copied</span>
      ) : (
        <>
          <span className="text-[10px] uppercase tracking-wide text-slate-600">
            {label}
          </span>
          {hash ? trunc(hash, 8) : "—"}
          <svg
            className="h-3 w-3 shrink-0 text-slate-600 opacity-0 transition-opacity group-hover:opacity-100"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            strokeWidth={2}
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              d="M15.75 17.25v3.375c0 .621-.504 1.125-1.125 1.125h-9.75a1.125 1.125 0 0 1-1.125-1.125V7.875c0-.621.504-1.125 1.125-1.125H6.75a9.06 9.06 0 0 1 1.5.124m7.5 10.376h3.375c.621 0 1.125-.504 1.125-1.125V11.25c0-4.46-3.243-8.161-7.5-8.876a9.06 9.06 0 0 0-1.5-.124H9.375c-.621 0-1.125.504-1.125 1.125v3.5m7.5 10.375H9.375a1.125 1.125 0 0 1-1.125-1.125v-9.25m12 6.625v-1.875a3.375 3.375 0 0 0-3.375-3.375h-1.5a1.125 1.125 0 0 1-1.125-1.125v-1.5a3.375 3.375 0 0 0-3.375-3.375H9.75"
            />
          </svg>
        </>
      )}
    </button>
  );
}

function ChainBadge({ state }: { state: ChainState }) {
  if (state === "valid") {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs font-semibold text-emerald-300">
        <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />
        Valid
      </span>
    );
  }
  if (state === "invalid") {
    // Amber/orange tamper treatment — deliberately distinct from breach red.
    return (
      <span className="inline-flex items-center gap-1.5 rounded border border-amber-500/50 bg-amber-500/10 px-2 py-0.5 text-xs font-bold uppercase tracking-wide text-amber-300">
        <svg
          className="h-3.5 w-3.5 text-amber-400"
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          strokeWidth={2}
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            d="M12 9v3.75m0 3.75h.008v.008H12v-.008ZM12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Z"
          />
        </svg>
        Tampered
      </span>
    );
  }
  return (
    <span
      className="inline-flex items-center gap-1.5 text-xs text-slate-500"
      title="Content payload withheld for this role; chain cannot be verified."
    >
      <span className="h-1.5 w-1.5 rounded-full bg-slate-500" />
      Unverified
    </span>
  );
}
