"use client";

import TopNav from "@/components/TopNav";
import Card from "@/components/ui/Card";
import StatusBadge from "@/components/ui/StatusBadge";
import { useOfficerSession } from "@/lib/useOfficerSession";

const AUDIT_ENTRIES = [
  {
    action: "TICKET_REQUEST",
    fir: "FIR-2026-0089",
    ts: "2026-09-01 18:40 UTC",
    hash: "c4d2…11b9",
  },
  {
    action: "AUTH_HANDSHAKE",
    fir: "—",
    ts: "2026-09-01 18:35 UTC",
    hash: "7a01…e332",
  },
];

export default function AuditPage() {
  const { loading, badge, role } = useOfficerSession();

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
      <main className="mx-auto w-full max-w-6xl flex-1 px-6 py-6">
        <div className="mb-6">
          <h1 className="text-xl font-bold text-white">Audit Trail</h1>
          <p className="mt-0.5 text-sm text-slate-400">
            Hash-chained record of actions on this workstation.
          </p>
        </div>

        <Card>
          <div className="section-head">
            <span className="section-title">Recent Activity</span>
            <StatusBadge tone="default">ML-DSA chained</StatusBadge>
          </div>
          <div className="tbl-head grid-cols-[1.4fr_1fr_1.4fr_1fr]">
            <span>Action</span>
            <span>FIR</span>
            <span>Timestamp</span>
            <span>Log Hash</span>
          </div>
          {AUDIT_ENTRIES.length === 0 ? (
            <p className="p-8 text-center text-sm text-slate-500">
              No audit entries recorded.
            </p>
          ) : (
            AUDIT_ENTRIES.map((e, i) => (
              <div
                key={i}
                className="tbl-row grid-cols-[1.4fr_1fr_1.4fr_1fr]"
              >
                <span className="font-mono text-xs text-slate-300">
                  {e.action}
                </span>
                <span className="font-mono text-xs text-slate-500">{e.fir}</span>
                <span className="text-sm tabular-nums text-slate-400">
                  {e.ts}
                </span>
                <span className="font-mono text-xs text-slate-500">{e.hash}</span>
              </div>
            ))
          )}
        </Card>

        <Card className="mt-6 p-5">
          <p className="microlabel">Integrity Model</p>
          <p className="mt-2 text-sm leading-relaxed text-slate-400">
            Every audit row links to the previous row&apos;s hash, forming an
            append-only chain. Tampering with any past entry invalidates every
            subsequent hash in the chain.
          </p>
        </Card>
      </main>

      <footer className="border-t border-slate-800 px-6 py-3 text-center">
        <p className="text-[11px] font-medium uppercase tracking-widest text-slate-600">
          Quantum-Secure Evidence Workstation &middot; Classified Access
        </p>
      </footer>
    </div>
  );
}
