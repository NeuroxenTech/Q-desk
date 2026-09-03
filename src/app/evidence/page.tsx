"use client";

import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import TopNav from "@/components/TopNav";
import Card from "@/components/ui/Card";
import StatusBadge from "@/components/ui/StatusBadge";
import PrimaryButton from "@/components/ui/PrimaryButton";
import { useOfficerSession } from "@/lib/useOfficerSession";
import { requestTicket } from "@/lib/api";
import EvidenceViewer from "./viewer";

const EVIDENCE = [
  {
    fir: "FIR-2026-0089",
    title: "Primary Investigation Pack",
    hash: "9f2c…8a41",
    versions: 3,
  },
];

export default function EvidencePage() {
  return (
    <Suspense fallback={null}>
      <EvidenceInner />
    </Suspense>
  );
}

function EvidenceInner() {
  const router = useRouter();
  const params = useSearchParams();
  const { loading, badge, role, sessionId } = useOfficerSession();
  const [requesting, setRequesting] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const ticketId = params.get("ticket");

  if (ticketId) {
    return (
      <EvidenceViewer
        ticketId={ticketId}
        firNumber={params.get("fir") || "UNKNOWN"}
      />
    );
  }

  const handleOpenEvidence = async (firNumber: string) => {
    if (!sessionId) return;
    setRequesting(firNumber);
    try {
      const resp = await requestTicket(
        sessionId,
        badge,
        badge,
        firNumber,
        "all"
      );
      sessionStorage.setItem("qdesk_ticket_expires", resp.ticket.expires_at);
      router.push(
        `/evidence?ticket=${encodeURIComponent(resp.ticket.id)}&fir=${encodeURIComponent(firNumber)}`
      );
    } catch (err: any) {
      setError(err?.message || "Failed to request ticket");
      setRequesting(null);
    }
  };

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
          <h1 className="text-xl font-bold text-white">Evidence Access</h1>
          <p className="mt-0.5 text-sm text-slate-400">
            Quantum-secure document access for assigned FIR cases.
          </p>
        </div>

        {error && <p className="mb-4 text-sm text-red-400">{error}</p>}

        <Card>
          <div className="section-head">
            <span className="section-title">Evidence Documents</span>
            <StatusBadge tone="default">Hash-verified</StatusBadge>
          </div>
          <div className="tbl-head grid-cols-[1fr_1.8fr_1fr_auto]">
            <span>FIR Number</span>
            <span>Document</span>
            <span>SHA-256</span>
            <span>Action</span>
          </div>
          {EVIDENCE.length === 0 ? (
            <p className="p-8 text-center text-sm text-slate-500">
              No evidence documents accessible under your assignment.
            </p>
          ) : (
            EVIDENCE.map((e) => (
              <div
                key={e.fir + e.title}
                className="tbl-row grid-cols-[1fr_1.8fr_1fr_auto]"
              >
                <span className="font-mono text-sm text-blue-300">{e.fir}</span>
                <span className="text-sm text-slate-300">{e.title}</span>
                <span className="font-mono text-xs text-slate-500">{e.hash}</span>
                <div className="flex items-center justify-end">
                  <PrimaryButton
                    onClick={() => handleOpenEvidence(e.fir)}
                    className="text-sm"
                    disabled={requesting === e.fir}
                  >
                    {requesting === e.fir ? "Opening…" : "Open Evidence"}
                  </PrimaryButton>
                </div>
              </div>
            ))
          )}
        </Card>

        <Card className="mt-6 p-5">
          <p className="microlabel">Access Model</p>
          <p className="mt-2 text-sm leading-relaxed text-slate-400">
            Evidence content is gated behind a signed, time-limited access
            ticket. Each FIR must be assigned to your badge before a ticket can
            be issued, and every opening is recorded in the audit trail.
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