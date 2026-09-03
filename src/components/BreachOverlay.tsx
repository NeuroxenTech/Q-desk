"use client";

import { useEffect, useState } from "react";
import { clearSession } from "@/lib/tauri";
import { useRouter } from "next/navigation";

function HazardStripe() {
  return (
    <div
      aria-hidden
      className="pointer-events-none absolute inset-0 opacity-[0.04]"
      style={{
        backgroundImage:
          "repeating-linear-gradient(135deg, #fff 0 18px, transparent 18px 36px)",
      }}
    />
  );
}

export default function BreachOverlay({
  onComplete,
}: {
  onComplete?: () => void;
}) {
  const router = useRouter();
  const [cleared, setCleared] = useState(false);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        await clearSession();
      } catch {
        /* best-effort */
      }
      if (!cancelled) setCleared(true);
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <div
      className="fixed inset-0 z-[9999] flex flex-col items-center justify-center select-none"
      style={{
        background:
          "radial-gradient(circle at 50% 40%, #991b1b 0%, #7f1d1d 45%, #450a0a 100%)",
      }}
    >
      <HazardStripe />
      <div className="relative flex flex-col items-center gap-6 px-6 text-center text-white">
        <div className="flex h-20 w-20 items-center justify-center rounded-full border-2 border-white/40 bg-white/10">
          <svg
            className="h-10 w-10 text-white"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            strokeWidth={2.2}
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126ZM12 15.75h.007v.008H12v-.008Z"
            />
          </svg>
        </div>

        <div className="space-y-2">
          <h1 className="text-5xl font-black uppercase tracking-tight">
            Breach Detected
          </h1>
          <p className="mx-auto max-w-md text-lg text-red-100/90">
            An unauthorized recording device was detected. Your session has
            been terminated and all access tokens have been revoked
            immediately.
          </p>
        </div>

        <p className="text-sm text-red-200/70">
          This incident has been logged in the audit trail.
        </p>

        <div className="mt-2 rounded-lg border border-white/30 bg-black/40 px-6 py-3 text-sm font-semibold uppercase tracking-[0.2em] text-red-100">
          All Actions Are Being Logged
        </div>
      </div>
    </div>
  );
}
