"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { getSession } from "@/lib/tauri";

export interface OfficerSession {
  loading: boolean;
  badge: string;
  role: string;
  sessionId: string | null;
}

export function useOfficerSession(): OfficerSession {
  const router = useRouter();
  const [state, setState] = useState<OfficerSession>({
    loading: true,
    badge: "",
    role: "",
    sessionId: null,
  });

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
        setState({
          loading: false,
          badge: sessionStorage.getItem("qdesk_badge") || "UNKNOWN",
          role: sessionStorage.getItem("qdesk_role") || "OFFICER",
          sessionId: session.ticket_id,
        });
      } catch {
        if (mounted)
          setState({
            loading: false,
            badge: "",
            role: "",
            sessionId: null,
          });
      }
    })();
    return () => {
      mounted = false;
    };
  }, [router]);

  return state;
}
