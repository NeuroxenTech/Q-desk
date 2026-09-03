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

export default function SettingsPage() {
  const { loading, badge, role } = useOfficerSession();
  const [tickets, setTickets] = useState<TicketRow[]>([]);

  useEffect(() => {
    const expires = sessionStorage.getItem("qdesk_ticket_expires");
    if (!expires) {
      setTickets([]);
      return;
    }
    const exp = new Date(expires);
    setTickets([
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

  const rows = [
    { label: "Workstation", value: "Q-DESK Secure Client" },
    { label: "Screen Capture Protection", value: "Active", live: true },
    { label: "Session Identity", value: role },
    { label: "ML-KEM", value: "768" },
    { label: "Signature Scheme", value: "ML-DSA-65" },
  ];

  return (
    <div className="flex min-h-screen flex-col bg-slate-950">
      <TopNav badge={badge} role={role} />
      <main className="mx-auto w-full max-w-4xl flex-1 px-6 py-6">
        <div className="mb-6">
          <h1 className="text-xl font-bold text-white">Workstation Settings</h1>
          <p className="mt-0.5 text-sm text-slate-400">
            Security posture, session configuration, and ticket history.
          </p>
        </div>

        <Card>
          <div className="section-head">
            <span className="section-title">Security Posture</span>
            <StatusBadge tone="success">Protected</StatusBadge>
          </div>
          <div>
            {rows.map((r) => (
              <div
                key={r.label}
                className="grid grid-cols-[1fr_auto] items-center gap-4 border-b border-slate-800/70 px-5 py-3.5 text-sm last:border-0"
              >
                <span className="text-slate-400">{r.label}</span>
                <span className="text-right">
                  {r.live ? (
                    <StatusBadge tone="success">{r.value}</StatusBadge>
                  ) : (
                    <span className="font-medium text-slate-200">{r.value}</span>
                  )}
                </span>
              </div>
            ))}
          </div>
        </Card>

        {/* My Ticket History — relocated from the standalone Tickets view. */}
        <Card className="mt-6">
          <div className="section-head">
            <span className="section-title">My Ticket History</span>
            <StatusBadge tone={tickets.length ? "success" : "default"}>
              {tickets.length} Recent
            </StatusBadge>
          </div>
          <div className="tbl-head grid-cols-[1fr_1.8fr_auto]">
            <span>FIR Number</span>
            <span>Expires At</span>
            <span>Status</span>
          </div>
          {tickets.length === 0 ? (
            <p className="p-6 text-center text-sm text-slate-500">
              No evidence tickets issued on this workstation.
            </p>
          ) : (
            tickets.map((t, i) => (
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

        <Card className="mt-6">
          <div className="section-head">
            <span className="section-title">Ticket Minting</span>
          </div>
          <p className="p-5 text-sm leading-relaxed text-slate-400">
            Tickets are ML-DSA-65 signed over a canonical payload and checked
            against a live store on every stream chunk. Expired or consumed
            tickets are rejected with an immediate session close code.
          </p>
        </Card>

        <Card className="mt-6 p-5">
          <p className="microlabel">End Session</p>
          <p className="mt-2 text-sm leading-relaxed text-slate-400">
            Ending your session revokes local tokens and returns you to the
            sign-in screen. Use the logout button in the top-right of any page.
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
