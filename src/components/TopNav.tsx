"use client";

import { useRouter, usePathname } from "next/navigation";
import { clearSession } from "@/lib/tauri";
import CaptureProtectionBadge from "@/components/CaptureProtectionBadge";

const NAV_ITEMS = [
  { label: "Dashboard", route: "/dashboard", icon: "grid" },
  { label: "Cases", route: "/cases", icon: "folder" },
  { label: "Versions", route: "/versions", icon: "clock" },
  { label: "Audit", route: "/audit", icon: "shield" },
  { label: "Settings", route: "/settings", icon: "gear" },
];

const ICON_PATHS: Record<string, string> = {
  grid: "M3.75 6A2.25 2.25 0 0 1 6 3.75h2.25A2.25 2.25 0 0 1 10.5 6v2.25a2.25 2.25 0 0 1-2.25 2.25H6a2.25 2.25 0 0 1-2.25-2.25V6ZM3.75 15.75A2.25 2.25 0 0 1 6 13.5h2.25a2.25 2.25 0 0 1 2.25 2.25V18a2.25 2.25 0 0 1-2.25 2.25H6A2.25 2.25 0 0 1 3.75 18v-2.25ZM13.5 6a2.25 2.25 0 0 1 2.25-2.25H18A2.25 2.25 0 0 1 20.25 6v2.25A2.25 2.25 0 0 1 18 10.5h-2.25a2.25 2.25 0 0 1-2.25-2.25V6ZM13.5 15.75a2.25 2.25 0 0 1 2.25-2.25H18a2.25 2.25 0 0 1 2.25 2.25V18A2.25 2.25 0 0 1 18 20.25h-2.25A2.25 2.25 0 0 1 13.5 18v-2.25Z",
  folder:
    "M2.25 12.75V12A2.25 2.25 0 0 1 4.5 9.75h15A2.25 2.25 0 0 1 21.75 12v.75m-8.69-6.44-2.12-2.12a1.5 1.5 0 0 0-1.061-.44H4.5A2.25 2.25 0 0 0 2.25 6v12a2.25 2.25 0 0 0 2.25 2.25h15A2.25 2.25 0 0 0 21.75 18V9a2.25 2.25 0 0 0-2.25-2.25h-5.379a1.5 1.5 0 0 1-1.06-.44Z",
  shield:
    "M9 12.75 11.25 15 15 9.75m-3-7.036A11.959 11.959 0 0 1 3.598 6 11.99 11.99 0 0 0 3 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285Z",
  ticket:
    "M16.5 6v.75m0 3v.75m0 3v.75m0 3V18m-12-4.5v-3m2.25 3h-3m3.75-9H4.5a1.5 1.5 0 0 0-1.5 1.5v9a1.5 1.5 0 0 0 1.5 1.5h15a1.5 1.5 0 0 0 1.5-1.5v-9a1.5 1.5 0 0 0-1.5-1.5h-3.75m-9 0V6a3 3 0 0 1 6 0v3m5.25 0H6.75",
  clock:
    "M12 6v6h4.5m4.5 0a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z",
  gear: "M11.42 15.17 17.25 21A2.652 2.652 0 0 0 21 17.25l-5.877-5.877M11.42 15.17l2.496-3.03c.317-.384.74-.626 1.208-.766M11.42 15.17l-4.655 5.653a2.548 2.548 0 1 1-3.586-3.586l6.837-5.63m5.108-.233c.55-.164 1.163-.188 1.743-.14a4.5 4.5 0 0 0 4.486-6.336l-3.276 3.277a3.004 3.004 0 0 1-2.25-2.25l3.276-3.276a4.5 4.5 0 0 0-6.336 4.486c.091 1.076-.071 2.264-.904 2.95l-.102.085m-1.745 1.437L5.909 7.5H4.5L2.25 3.75l1.5-1.5L7.5 4.5v1.409l4.26 4.26m-1.745 1.437 1.745-1.437m6.615 8.206L15.75 15.75M4.867 19.125h.008v.008h-.008v-.008Z",
};

export default function TopNav({
  badge,
  role,
}: {
  badge: string;
  role: string;
}) {
  const router = useRouter();
  const pathname = usePathname();

  const handleEndSession = async () => {
    try {
      await clearSession();
    } catch {
      /* best-effort */
    }
    sessionStorage.clear();
    router.push("/");
  };

  const isActive = (route: string) =>
    pathname === route || pathname.startsWith(route + "/");

  return (
    <header className="sticky top-0 z-20 border-b border-slate-800 bg-slate-900/90 backdrop-blur">
      <div className="flex h-14 items-center justify-between px-6">
        <div className="flex items-center gap-3">
          <button
            onClick={() => router.push("/dashboard")}
            className="flex items-center gap-3"
          >
            <div className="flex h-8 w-8 items-center justify-center rounded border border-blue-700/60 bg-blue-600/15">
              <svg
                className="h-4 w-4 text-blue-400"
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
            </div>
            <span className="text-base font-bold tracking-wide text-white">
              Q-DESK
            </span>
          </button>
        </div>

        <nav className="hidden items-center gap-1 md:flex">
          {NAV_ITEMS.map((item) => (
            <button
              key={item.label}
              onClick={() => router.push(item.route)}
              className={`flex items-center gap-2 rounded px-3 py-1.5 text-sm font-medium transition-colors ${
                isActive(item.route)
                  ? "bg-blue-600/15 text-blue-300"
                  : "text-slate-400 hover:bg-slate-800 hover:text-slate-200"
              }`}
            >
              <svg
                className="h-4 w-4"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                strokeWidth={1.8}
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  d={ICON_PATHS[item.icon]}
                />
              </svg>
              {item.label}
            </button>
          ))}
        </nav>

        <div className="flex items-center gap-4">
          <CaptureProtectionBadge className="hidden lg:inline-flex" />
          <div className="flex items-center gap-3 border-l border-slate-800 pl-4">
            <div>
              <p className="text-sm font-medium leading-tight text-white">
                {badge}
              </p>
              <p className="text-xs text-slate-500">{role}</p>
            </div>
            <button
              onClick={handleEndSession}
              className="h-8 w-8 rounded border border-slate-700 text-slate-400 transition-colors hover:border-red-700 hover:text-red-400"
              aria-label="End session / Logout"
            >
              <svg
                className="mx-auto h-4 w-4"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                strokeWidth={1.8}
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  d="M15.75 9V5.25A2.25 2.25 0 0 0 13.5 3h-6a2.25 2.25 0 0 0-2.25 2.25v13.5A2.25 2.25 0 0 0 7.5 21h6a2.25 2.25 0 0 0 2.25-2.25V15m3 0 3-3m0 0-3-3m3 3H9"
                />
              </svg>
            </button>
          </div>
        </div>
      </div>
    </header>
  );
}
