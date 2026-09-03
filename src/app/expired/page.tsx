"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";
import PrimaryButton from "@/components/ui/PrimaryButton";

export default function ExpiredPage() {
  const router = useRouter();

  useEffect(() => {
    sessionStorage.removeItem("qdesk_ticket_expires");
  }, []);

  return (
    <div className="flex min-h-screen items-center justify-center bg-slate-950">
      <div className="max-w-md p-8 text-center">
        <div className="relative mx-auto mb-8 flex h-24 w-24 items-center justify-center">
          <span className="absolute inset-0 animate-ping rounded-full bg-red-500/20" />
          <span className="absolute -inset-3 rounded-full border border-red-500/30" />
          <span className="absolute -inset-6 rounded-full border border-red-500/20" />
          <div className="relative flex h-20 w-20 items-center justify-center rounded-full border border-red-500/40 bg-red-500/10">
            <svg
              className="h-10 w-10 text-red-400"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              strokeWidth={2}
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                d="M16.5 10.5V6.75a4.5 4.5 0 1 0-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 0 0 2.25-2.25v-6.75a2.25 2.25 0 0 0-2.25-2.25H6.75a2.25 2.25 0 0 0-2.25 2.25v6.75a2.25 2.25 0 0 0 2.25 2.25Z"
              />
            </svg>
          </div>
        </div>
        <h2 className="text-2xl font-extrabold uppercase tracking-tight text-red-400">
          Session Expired
        </h2>
        <p className="mt-3 text-sm text-slate-400">
          Your evidence access ticket has expired. The evidence content is no
          longer accessible. Please re-authenticate with your badge and PIN to
          continue.
        </p>
        <PrimaryButton onClick={() => router.push("/")} className="mt-6">
          Go to Login
        </PrimaryButton>
      </div>
    </div>
  );
}
