"use client";

import { useEffect, useState } from "react";
import TopNav from "@/components/TopNav";
import Card from "@/components/ui/Card";
import StatusBadge from "@/components/ui/StatusBadge";
import { useOfficerSession } from "@/lib/useOfficerSession";

interface TicketRow {
  fir: string;
  expiresAt: string;
  live: boolean;
}

export default function TicketsPage() {
  const { loading, badge, role } = useOfficerSession();
  const [activeTickets, setActiveTickets] = useState<TicketRow[]>([]);

  useEffect(() => {
    const expires = sessionStorage.getItem("qdesk_ticket_expires");
    if (!expires) return;
    const exp = new Date(expires);
    setActiveTickets([
      {
        fir: sessionStorage.getItem("qdesk_last_fir") || "FIR-2026-0089",
        expiresAt: exp.toLocaleString(),
        live: exp.getTime() > Date.now(),
      },
    ]);
  }, []);

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
          <h1 className="text-xl font-bold text-white">Access Tickets</h1>
          <p className="mt-0.5 text-sm text-slate-400">
            Signed, time-limited tickets issued for evidence access.
          </p>
        </div>

        <Card>
          <div className="section-head">
            <span className="section-title">Issued Tickets</span>
            <StatusBadge tone={activeTickets.length ? "success" : "default"}>
              {activeTickets.length} Active
            </StatusBadge>
          </div>
          <div className="tbl-head grid-cols-[1fr_1.8fr_auto]">
            <span>FIR Number</span>
            <span>Expires At</span>
            <span>Status</span>
          </div>
          {activeTickets.length === 0 ? (
            <p className="p-8 text-center text-sm text-slate-500">
              No active evidence tickets on this workstation.
            </p>
          ) : (
            activeTickets.map((t, i) => (
              <div key={i} className="tbl-row grid-cols-[1fr_1.8fr_auto]">
                <span className="font-mono text-sm text-blue-300">{t.fir}</span>
                <span className="text-sm tabular-nums text-slate-300">
                  {t.expiresAt}
                </span>
                <StatusBadge tone={t.live ? "success" : "warning"}>
                  {t.live ? "Active" : "Expired"}
                </StatusBadge>
              </div>
            ))
          )}
        </Card>

        <Card className="mt-6 p-5">
          <p className="microlabel">Minting Details</p>
          <p className="mt-2 text-sm leading-relaxed text-slate-400">
            Tickets are ML-DSA-65 signed over a canonical payload and checked
            against a live store on every stream chunk. Expired or consumed
            tickets are rejected with an immediate session close code.
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
