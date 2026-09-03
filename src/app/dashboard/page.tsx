"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { getSession } from "@/lib/tauri";
import { requestTicket } from "@/lib/api";
import TopNav from "@/components/TopNav";
import Card from "@/components/ui/Card";
import PrimaryButton from "@/components/ui/PrimaryButton";
import StatusBadge from "@/components/ui/StatusBadge";

interface AssignedCase {
  fir_number: string;
  title: string;
}

export default function DashboardPage() {
  const router = useRouter();

  const [sessionId, setSessionId] = useState<string | null>(null);
  const [badge, setBadge] = useState("");
  const [role, setRole] = useState("");
  const [cases, setCases] = useState<AssignedCase[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [requestingTicket, setRequestingTicket] = useState<string | null>(null);

  useEffect(() => {
    let mounted = true;
    (async () => {
      try {
        const session = await getSession();
        if (!mounted) return;
        if (!session) {
          router.replace("/");
          return;
        }
        setSessionId(session.ticket_id);
        setBadge(sessionStorage.getItem("qdesk_badge") || "UNKNOWN");
        setRole(sessionStorage.getItem("qdesk_role") || "OFFICER");

        setCases([
          {
            fir_number: "FIR-2026-0089",
            title: "FIR-2026-0089 — Active Investigation",
          },
        ]);
      } catch {
        if (mounted) setError("Failed to load dashboard data");
      } finally {
        if (mounted) setLoading(false);
      }
    })();
    return () => {
      mounted = false;
    };
  }, [router]);

  const handleOpenCase = async (firNumber: string) => {
    if (!sessionId) return;
    setRequestingTicket(firNumber);
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
      setRequestingTicket(null);
    }
  };

  if (loading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-slate-950">
        <div className="flex flex-col items-center gap-4">
          <div className="h-8 w-8 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />
          <p className="text-sm text-slate-400">Loading dashboard...</p>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen flex-col bg-slate-950">
      <TopNav badge={badge} role={role} />

      {/* Content */}
      <main className="mx-auto w-full max-w-6xl flex-1 px-6 py-6">
        <div className="mb-6">
          <h1 className="text-xl font-bold text-white">Command Dashboard</h1>
          <p className="mt-0.5 text-sm text-slate-400">
            Overview of your assigned cases and active sessions.
          </p>
        </div>

        {error && (
          <div className="mb-5 rounded border border-red-700/60 bg-red-950/40 px-4 py-3 text-sm text-red-300">
            {error}
          </div>
        )}

        {/* KPI stat row */}
        <div className="mb-8 grid grid-cols-1 gap-4 sm:grid-cols-3">
          <Card className="p-5">
            <p className="microlabel">Assigned Cases</p>
            <div className="mt-2 flex items-baseline gap-2">
              <span className="text-3xl font-bold tabular-nums text-white">{cases.length}</span>
              <span className="text-xs text-slate-500">total</span>
            </div>
          </Card>
          <Card className="p-5">
            <p className="microlabel">Active Tickets</p>
            <div className="mt-2 flex items-baseline gap-2">
              <span className="text-3xl font-bold tabular-nums text-blue-400">0</span>
              <span className="text-xs text-slate-500">open</span>
            </div>
          </Card>
          <Card className="p-5">
            <p className="microlabel">Recent Breaches</p>
            <div className="mt-2 flex items-baseline gap-2">
              <span className={`text-3xl font-bold tabular-nums ${0 > 0 ? "text-red-400" : "text-emerald-400"}`}>0</span>
              <span className="text-xs text-slate-500">{0 > 0 ? "critical" : "none"}</span>
            </div>
          </Card>
        </div>

        {/* Cases table */}
        <Card>
          <div className="section-head">
            <span className="section-title">Assigned FIR Cases</span>
            <StatusBadge tone="success">{cases.length} Assigned</StatusBadge>
          </div>

          {cases.length === 0 ? (
            <p className="p-8 text-center text-sm text-slate-500">
              No cases assigned to your badge.
            </p>
          ) : (
            <div>
              <div className="tbl-head grid-cols-[1fr_1.6fr_auto]">
                <span>FIR Number</span>
                <span>Description</span>
                <span>Action</span>
              </div>
              {cases.map((c) => (
                <div key={c.fir_number} className="tbl-row grid-cols-[1fr_1.6fr_auto]">
                  <span className="font-mono text-sm text-blue-300">{c.fir_number}</span>
                  <span className="text-sm text-slate-300">{c.title}</span>
                  <div className="flex items-center justify-end gap-3">
                    <StatusBadge tone="success">Active</StatusBadge>
                    <PrimaryButton
                      onClick={() => handleOpenCase(c.fir_number)}
                      disabled={requestingTicket === c.fir_number}
                      className="text-sm"
                    >
                      {requestingTicket === c.fir_number ? (
                        <span className="flex items-center gap-2">
                          <span className="h-3 w-3 animate-spin rounded-full border-2 border-white border-t-transparent" />
                          Requesting...
                        </span>
                      ) : (
                        "Open Case"
                      )}
                    </PrimaryButton>
                  </div>
                </div>
              ))}
            </div>
          )}
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
