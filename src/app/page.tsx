"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import {
  getPlatform,
  enableScreenProtection,
  getScreenProtectionStatus,
  setSession,
} from "@/lib/tauri";
import { performHandshake } from "@/lib/crypto";
import CaptureProtectionBadge from "@/components/CaptureProtectionBadge";
import Card from "@/components/ui/Card";
import StatusBadge from "@/components/ui/StatusBadge";
import PrimaryButton from "@/components/ui/PrimaryButton";

export default function LoginPage() {
  const router = useRouter();

  const [protectionState, setProtectionState] = useState<
    "loading" | "active" | "failed"
  >("loading");
  const [platform, setPlatform] = useState("");
  const [badgeNumber, setBadgeNumber] = useState("");
  const [pin, setPin] = useState("");
  const [showPin, setShowPin] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let mounted = true;
    (async () => {
      try {
        const [plat] = await Promise.all([
          getPlatform(),
          enableScreenProtection().catch(() => {}),
        ]);
        if (!mounted) return;
        setPlatform(plat);

        const status = await getScreenProtectionStatus();
        if (!mounted) return;
        setProtectionState(status ? "active" : "failed");
      } catch {
        if (mounted) setProtectionState("failed");
      }
    })();
    return () => {
      mounted = false;
    };
  }, []);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setLoading(true);

    try {
      if (!badgeNumber.trim() || !pin.trim()) {
        throw new Error("Badge number and PIN are required");
      }

      const { sessionId, sessionKeyB64 } = await performHandshake();
      await setSession(sessionId, sessionKeyB64);

      sessionStorage.setItem("qdesk_badge", badgeNumber.trim());
      sessionStorage.setItem("qdesk_pin", pin.trim());

      router.push("/dashboard");
    } catch (err: any) {
      setError(err?.message || "Authentication failed");
    } finally {
      setLoading(false);
    }
  };

  if (protectionState === "loading") {
    return (
      <div className="flex min-h-screen items-center justify-center bg-slate-950">
        <div className="flex flex-col items-center gap-4">
          <div className="h-8 w-8 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />
          <p className="text-sm text-slate-400">
            Initializing screen capture protection...
          </p>
        </div>
      </div>
    );
  }

  if (protectionState === "failed") {
    return (
      <div className="flex min-h-screen items-center justify-center bg-slate-950">
        <Card className="max-w-md p-8 text-center">
          <div className="mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-red-500/10">
            <svg
              className="h-8 w-8 text-red-400"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              strokeWidth={2}
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126ZM12 15.75h.007v.008H12v-.008Z"
              />
            </svg>
          </div>
          <h2 className="text-xl font-bold text-red-400">
            Screen Capture Protection Unavailable
          </h2>
          <p className="mt-3 text-sm text-slate-400">
            Q-DESK requires screen capture protection ({platform}). Evidence
            access is blocked on this device.
          </p>
        </Card>
      </div>
    );
  }

  const shieldMark = (
    <svg
      className="h-10 w-10 text-primary-light"
      fill="none"
      viewBox="0 0 24 24"
      stroke="currentColor"
      strokeWidth={1.8}
    >
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        d="M9 12.75 11.25 15 15 9.75m-3-7.036A11.959 11.959 0 0 1 3.598 6 11.99 11.99 0 0 0 3 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285Z"
      />
    </svg>
  );

  return (
    <div className="flex min-h-screen flex-col bg-slate-950">
      {/* Application header bar */}
      <header className="flex items-center justify-between border-b border-slate-800 bg-slate-900/70 px-6 py-3">
        <div className="flex items-center gap-3">
          <div className="flex h-8 w-8 items-center justify-center rounded border border-blue-700/60 bg-blue-600/15">
            {shieldMark}
          </div>
          <div className="flex items-baseline gap-2">
            <span className="text-base font-bold tracking-wide text-white">Q-DESK</span>
            <span className="text-[10px] font-medium uppercase tracking-widest text-slate-500">
              Secure Evidence Workstation
            </span>
          </div>
        </div>
        <StatusBadge tone="default">Quantum-Secure</StatusBadge>
      </header>

      {/* Login content */}
      <main className="flex flex-1 items-center justify-center px-6 py-10">
        <div className="w-full max-w-md">
          <div className="mb-6">
            <h1 className="text-2xl font-bold text-white">Officer Sign-In</h1>
            <p className="mt-1 text-sm text-slate-400">
              Authenticate to access assigned evidence and case material.
            </p>
          </div>

          <Card className="overflow-hidden">
            <form onSubmit={handleSubmit} className="space-y-5 p-6">
              <div>
                <label htmlFor="badge" className="field-label">
                  Badge Number
                </label>
                <input
                  id="badge"
                  type="text"
                  value={badgeNumber}
                  onChange={(e) => setBadgeNumber(e.target.value)}
                  placeholder="e.g. IND-IO-402"
                  className="input"
                  autoComplete="off"
                  autoFocus
                />
              </div>

              <div>
                <label htmlFor="pin" className="field-label">
                  PIN
                </label>
                <div className="relative">
                  <input
                    id="pin"
                    type={showPin ? "text" : "password"}
                    value={pin}
                    onChange={(e) => setPin(e.target.value)}
                    placeholder="Enter your PIN"
                    className="input pr-12"
                    autoComplete="off"
                  />
                  <button
                    type="button"
                    onClick={() => setShowPin((v) => !v)}
                    className="absolute inset-y-0 right-0 flex items-center pr-3 text-slate-500 transition-colors hover:text-slate-300"
                    aria-label={showPin ? "Hide PIN" : "Show PIN"}
                    tabIndex={-1}
                  >
                    {showPin ? (
                      <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                        <path strokeLinecap="round" strokeLinejoin="round" d="M3.98 8.223A10.477 10.477 0 0 0 1.934 12C3.226 16.338 7.244 19.5 12 19.5c.993 0 1.953-.138 2.863-.395M6.228 6.228A10.451 10.451 0 0 1 12 4.5c4.756 0 8.773 3.162 10.065 7.498a10.522 10.522 0 0 1-4.293 5.774M6.228 6.228 3 3m3.228 3.228 3.65 3.65m7.894 7.894L21 21m-3.228-3.228-3.65-3.65m0 0a3 3 0 1 0-4.243-4.243m4.242 4.242L9.88 9.88" />
                      </svg>
                    ) : (
                      <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                        <path strokeLinecap="round" strokeLinejoin="round" d="M2.036 12.322a1.012 1.012 0 0 1 0-.639C3.423 7.51 7.36 4.5 12 4.5c4.638 0 8.573 3.007 9.963 7.178.07.207.07.431 0 .639C20.577 16.49 16.64 19.5 12 19.5c-4.638 0-8.573-3.007-9.963-7.178Z" />
                        <path strokeLinecap="round" strokeLinejoin="round" d="M15 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0Z" />
                      </svg>
                    )}
                  </button>
                </div>
              </div>

              {error && (
                <div className="rounded border border-red-700/60 bg-red-950/40 px-4 py-3 text-sm text-red-300">
                  {error}
                </div>
              )}

              <PrimaryButton
                type="submit"
                disabled={loading || !badgeNumber.trim() || !pin.trim()}
                className="w-full"
              >
                {loading ? (
                  <span className="flex items-center justify-center gap-2">
                    <span className="h-4 w-4 animate-spin rounded-full border-2 border-white border-t-transparent" />
                    Authenticating...
                  </span>
                ) : (
                  "Sign In"
                )}
              </PrimaryButton>
            </form>
          </Card>

          {/* Distinct status + trust sections */}
          <Card className="mt-4">
            <div className="section-head">
              <span className="section-title">Workstation Status</span>
              <CaptureProtectionBadge />
            </div>
            <div className="flex items-start gap-3 p-5">
              <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded border border-emerald-600/50 bg-emerald-600/10">
                <svg className="h-4 w-4 text-emerald-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75 11.25 15 15 9.75m-3-7.036A11.959 11.959 0 0 1 3.598 6 11.99 11.99 0 0 0 3 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285Z" />
                </svg>
              </div>
              <div>
                <p className="text-sm font-medium text-slate-200">
                  Protected Mode Active
                </p>
                <p className="mt-0.5 text-xs leading-relaxed text-slate-400">
                  Screen capture is blocked. Evidence cannot be recorded or
                  screenshotted on this workstation.
                </p>
              </div>
            </div>
          </Card>
        </div>
      </main>

      {/* Footer */}
      <footer className="border-t border-slate-800 px-6 py-3 text-center">
        <p className="text-[11px] font-medium uppercase tracking-widest text-slate-500">
          ML-KEM-768 Handshake &middot; ML-DSA-65 Verified
        </p>
      </footer>
    </div>
  );
}
