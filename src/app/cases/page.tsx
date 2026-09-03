"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import TopNav from "@/components/TopNav";
import Card from "@/components/ui/Card";
import StatusBadge from "@/components/ui/StatusBadge";
import PrimaryButton from "@/components/ui/PrimaryButton";
import SecondaryButton from "@/components/ui/SecondaryButton";
import ContentTypeIcon from "@/components/ui/ContentTypeIcon";
import UploadEvidenceDialog from "@/components/UploadEvidenceDialog";
import { useOfficerSession } from "@/lib/useOfficerSession";
import { requestTicket, fetchVersions } from "@/lib/api";
import { canUploadEvidence } from "@/lib/content";
import type { UploadDraft, UploadResponse } from "@/lib/types";

interface CaseRow {
  fir: string;
  title: string;
  category: string;
  status: "Active" | "Closed";
}

const CASES: CaseRow[] = [
  { fir: "FIR-2026-0089", title: "Active Investigation", category: "Criminal", status: "Active" },
];

export default function CasesPage() {
  const router = useRouter();
  const { loading, badge, role, sessionId } = useOfficerSession();
  const [requesting, setRequesting] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [contentTypes, setContentTypes] = useState<Record<string, string[]>>({});
  // Per-case version-tree summary (latest path + branch/merge counts) derived
  // from the kind carried by fetchVersions.
  const [versionStats, setVersionStats] = useState<
    Record<string, { latest: string; branches: number; merged: number }>
  >({});
  const [versionError, setVersionError] = useState<string | null>(null);
  const [uploadOpen, setUploadOpen] = useState(false);

  const uploadEligible = canUploadEvidence(role);

  // Pull real version rows per case FIR to render content-type chips (the
  // cases list itself is a static demo list; the version data is live).
  const loadTypes = async (fir: string) => {
    if (!sessionId || !badge) return;
    try {
      const resp = await fetchVersions(sessionId, badge, { firNumber: fir });
      const types = Array.from(
        new Set(
          resp.versions
            .map((v) => v.content_type)
            .filter((t): t is string => !!t && t !== "text/plain")
        ).values()
      );
      setContentTypes((prev) => ({ ...prev, [fir]: types }));

      // Version-tree summary: last mainline label plus branch/merge counts,
      // from the backend-computed kind field (e.g. "v5 · 2 branches, 1 merged").
      const branches = resp.versions.filter((v) => v.kind === "branch").length;
      const merged = resp.versions.filter((v) => v.kind === "merge").length;
      const latest =
        resp.versions
          .filter((v) => v.kind === "mainline")
          .sort((a, b) => b.version_number - a.version_number)[0]?.tree_path ||
        resp.versions[0]?.tree_path ||
        "v1";
      setVersionStats((prev) => ({
        ...prev,
        [fir]: { latest, branches, merged },
      }));
      setVersionError(null);
    } catch (err: any) {
      setVersionError(
        err?.message ||
          "Could not load version summaries (branch/merge counts)."
      );
    }
  };

  useEffect(() => {
    if (!sessionId || !badge) return;
    for (const c of CASES) loadTypes(c.fir);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, badge, uploadOpen]);

  // Open a case by requesting a signed access ticket, then handing it to the
  // evidence viewer. This is the evidence-access functionality that used to
  // live on the standalone Evidence page, now folded into Cases.
  const handleOpenCase = async (fir: string) => {
    if (!sessionId) return;
    setRequesting(fir);
    setError(null);
    try {
      const resp = await requestTicket(sessionId, badge, badge, fir, "all");
      sessionStorage.setItem("qdesk_ticket_expires", resp.ticket.expires_at);
      router.push(
        `/evidence?ticket=${encodeURIComponent(resp.ticket.id)}&fir=${encodeURIComponent(fir)}`
      );
    } catch (err: any) {
      setError(err?.message || "Failed to request ticket");
      setRequesting(null);
    }
  };

  const handleUploadSuccess = (result: UploadResponse) => {
    setUploadOpen(false);
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
          <h1 className="text-xl font-bold text-white">Assigned Cases</h1>
          <p className="mt-0.5 text-sm text-slate-400">
            Criminal investigations assigned to your badge. Open a case to
            access its evidence, or upload the first evidence file for a case.
          </p>
        </div>

        {error && (
          <div className="mb-5 rounded border border-red-700/60 bg-red-950/40 px-4 py-3 text-sm text-red-300">
            {error}
          </div>
        )}
        {versionError && (
          <div className="mb-5 rounded border border-amber-700/60 bg-amber-950/40 px-4 py-3 text-sm text-amber-300">
            {versionError}
          </div>
        )}

        <Card>
          <div className="section-head">
            <span className="section-title">FIR Cases</span>
            <StatusBadge tone="success">{CASES.length} Assigned</StatusBadge>
          </div>
          <div className="tbl-head grid-cols-[1fr_1.8fr_1fr_auto]">
            <span>FIR Number</span>
            <span>Title</span>
            <span>Category</span>
            <span>Action</span>
          </div>
          {CASES.length === 0 ? (
            <p className="p-8 text-center text-sm text-slate-500">
              No cases assigned to your badge.
            </p>
          ) : (
            CASES.map((c) => (
              <div
                key={c.fir}
                className="tbl-row grid-cols-[1fr_1.8fr_1fr_auto]"
              >
                <span className="font-mono text-sm text-blue-300">{c.fir}</span>
                <span>
                  <span className="text-sm text-slate-300">{c.title}</span>
                  {versionStats[c.fir] && (
                    <span className="mt-1.5 block font-mono text-[11px] text-slate-400">
                      {versionStats[c.fir].latest}
                      {versionStats[c.fir].branches > 0 &&
                        ` · ${versionStats[c.fir].branches} branch${
                          versionStats[c.fir].branches === 1 ? "" : "es"
                        }`}
                      {versionStats[c.fir].merged > 0 &&
                        ` · ${versionStats[c.fir].merged} merged`}
                    </span>
                  )}
                  {(contentTypes[c.fir] || []).length > 0 && (
                    <span className="mt-1.5 flex flex-wrap gap-1.5">
                      {(contentTypes[c.fir] || []).map((t) => (
                        <ContentTypeIcon key={t} mime={t} />
                      ))}
                    </span>
                  )}
                </span>
                <span className="text-sm text-slate-400">{c.category}</span>
                <div className="flex items-center justify-end gap-3">
                  <StatusBadge tone={c.status === "Active" ? "success" : "default"}>
                    {c.status}
                  </StatusBadge>
                  {uploadEligible && (
                    <SecondaryButton
                      onClick={() => setUploadOpen(true)}
                      className="text-sm"
                      title="Upload a new evidence file to this case"
                    >
                      Upload Evidence
                    </SecondaryButton>
                  )}
                  <PrimaryButton
                    onClick={() => handleOpenCase(c.fir)}
                    className="text-sm"
                    disabled={requesting === c.fir}
                  >
                    {requesting === c.fir
                      ? "Opening…"
                      : c.status === "Active"
                        ? "Open Case"
                        : "View Case"}
                  </PrimaryButton>
                </div>
              </div>
            ))
          )}
        </Card>
      </main>

      <footer className="border-t border-slate-800 px-6 py-3 text-center">
        <p className="text-[11px] font-medium uppercase tracking-widest text-slate-600">
          Quantum-Secure Evidence Workstation &middot; Classified Access
        </p>
      </footer>

      <UploadEvidenceDialog
        open={uploadOpen}
        onClose={() => setUploadOpen(false)}
        sessionId={sessionId}
        badge={badge}
        firNumber={CASES[0]?.fir || "FIR-2026-0089"}
        draft={{ isNewDocument: true, versionNumber: 1, treePath: "v1" } satisfies UploadDraft}
        onSuccess={handleUploadSuccess}
      />
    </div>
  );
}