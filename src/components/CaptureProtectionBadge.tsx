"use client";

import { useEffect, useState } from "react";
import { getScreenProtectionStatus } from "@/lib/tauri";
import StatusBadge from "@/components/ui/StatusBadge";

const shieldIcon = (
  <svg
    className="h-3 w-3"
    fill="none"
    viewBox="0 0 24 24"
    stroke="currentColor"
    strokeWidth={2}
  >
    <path
      strokeLinecap="round"
      strokeLinejoin="round"
      d="M9 12.75 11.25 15 15 9.75m-3-7.036A11.959 11.959 0 0 1 3.598 6 11.99 11.99 0 0 0 3 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285Z"
    />
  </svg>
);

export default function CaptureProtectionBadge({
  className = "",
}: {
  className?: string;
}) {
  const [active, setActive] = useState<boolean | null>(null);
  const [platform, setPlatform] = useState("");

  useEffect(() => {
    let mounted = true;
    const poll = async () => {
      try {
        const [status, plat] = await Promise.all([
          getScreenProtectionStatus(),
          import("@/lib/tauri").then((m) => m.getPlatform()),
        ]);
        if (mounted) {
          setActive(status);
          setPlatform(plat);
        }
      } catch {
        if (mounted) setActive(false);
      }
    };
    poll();
    const interval = setInterval(poll, 5000);
    return () => {
      mounted = false;
      clearInterval(interval);
    };
  }, []);

  if (active === null) return null;

  return (
    <StatusBadge
      tone={active ? "success" : "danger"}
      icon={shieldIcon}
      className={className}
    >
      Screen Capture {active ? "Blocked" : "Unavailable"} &middot; {platform}
    </StatusBadge>
  );
}
