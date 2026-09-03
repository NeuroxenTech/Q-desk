import { useState } from "react";
import ContentTypeIcon from "@/components/ui/ContentTypeIcon";

export interface VersionItem {
  hash: string;
  author: string;
  time: string;
  mime?: string;
}

interface VersionTimelineProps {
  versions: VersionItem[];
}

export default function VersionTimeline({ versions }: VersionTimelineProps) {
  const [open, setOpen] = useState(true);

  return (
    <div>
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between border-b border-slate-800 pb-3 text-sm font-semibold uppercase tracking-wider text-slate-300 transition-colors hover:text-white"
        aria-expanded={open}
      >
        <span className="flex items-center gap-2">
          <svg className="h-4 w-4 text-blue-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M12 6v6h4.5m4.5 0a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z" />
          </svg>
          Version History
        </span>
        <svg
          className={`h-4 w-4 text-slate-500 transition-transform ${open ? "" : "-rotate-90"}`}
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          strokeWidth={2}
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
        </svg>
      </button>

      {open && (
        <div className="mt-4">
          {versions.length === 0 ? (
            <div className="rounded border border-slate-800 bg-slate-900/50 p-4 text-xs text-slate-500 text-center">
              No versions loaded.
            </div>
          ) : (
            <ol className="relative ml-2 space-y-5 border-l border-slate-700 pl-5">
              {versions.map((v, i) => (
                <li key={i} className="relative">
                  <span
                    className={`absolute -left-[27px] top-0.5 h-2.5 w-2.5 rounded-full border-2 ${
                      i === 0
                        ? "border-emerald-400 bg-emerald-500/40"
                        : "border-blue-400 bg-blue-500/30"
                    }`}
                  />
                  <p className="text-[10px] font-bold uppercase tracking-wider text-blue-400">
                    Version {versions.length - i}
                  </p>
                  <div className="mt-1 flex items-center gap-2">
                    <p className="font-mono text-xs text-slate-200">{v.hash.slice(0, 16)}...</p>
                    {v.mime && <ContentTypeIcon mime={v.mime} />}
                  </div>
                  <p className="mt-0.5 text-xs text-slate-500">
                    by {v.author} &middot; {v.time}
                  </p>
                </li>
              ))}
            </ol>
          )}
        </div>
      )}
    </div>
  );
}
