import { ContentKind, kindLabel, mimeToKind } from "@/lib/content";

// Renders a small content-type badge (icon + label) for an evidence file /
// version row, e.g. the short MIME chips on the Cases list and Versions tab.
export default function ContentTypeIcon({
  mime,
  className = "",
}: {
  mime: string | undefined;
  className?: string;
}) {
  const kind: ContentKind = mimeToKind(mime);
  const palette: Record<ContentKind, string> = {
    image: "border-sky-500/40 bg-sky-500/10 text-sky-300",
    pdf: "border-red-500/40 bg-red-500/10 text-red-300",
    video: "border-violet-500/40 bg-violet-500/10 text-violet-300",
    audio: "border-emerald-500/40 bg-emerald-500/10 text-emerald-300",
    text: "border-slate-600 bg-slate-800/60 text-slate-300",
    unknown: "border-slate-600 bg-slate-800/60 text-slate-300",
  };

  return (
    <span
      title={mime ?? "Text chunk"}
      className={`inline-flex cursor-default items-center gap-1.5 rounded border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${palette[kind]} ${className}`}
    >
      <KindGlyph kind={kind} />
      {kindLabel(kind)}
    </span>
  );
}

function KindGlyph({ kind }: { kind: ContentKind }) {
  const cls = "h-3 w-3";
  switch (kind) {
    case "image":
      return (
        <svg className={cls} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 15.75l5.159-5.159a2.25 2.25 0 0 1 3.182 0l5.159 5.159m-1.5-1.5 1.409-1.409a2.25 2.25 0 0 1 3.182 0l2.909 2.909m-18 3.75h16.5a1.5 1.5 0 0 0 1.5-1.5V6a1.5 1.5 0 0 0-1.5-1.5H3.75A1.5 1.5 0 0 0 2.25 6v12a1.5 1.5 0 0 0 1.5 1.5Zm10.5-11.25h.008v.008h-.008V8.25Z" />
        </svg>
      );
    case "pdf":
      return (
        <svg className={cls} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 0 0-3.375-3.375h-1.5A1.125 1.125 0 0 1 13.5 7.125v-1.5a3.375 3.375 0 0 0-3.375-3.375H8.25m2.25 9.75 2.25 2.25m0 0 2.25-2.25m-2.25 2.25V21" />
        </svg>
      );
    case "video":
      return (
        <svg className={cls} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="m15.75 10.5 4.72-4.72a.75.75 0 0 1 1.28.53v11.38a.75.75 0 0 1-1.28.53l-4.72-4.72M4.5 18.75h9a2.25 2.25 0 0 0 2.25-2.25v-9a2.25 2.25 0 0 0-2.25-2.25h-9A2.25 2.25 0 0 0 2.25 7.5v9a2.25 2.25 0 0 0 2.25 2.25Z" />
        </svg>
      );
    case "audio":
      return (
        <svg className={cls} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M9 9l10.5-3m0 6.553v3.75a2.25 2.25 0 0 1-1.632 2.163l-1.32.377a1.803 1.803 0 1 1-.99-3.467l2.31-.66a2.25 2.25 0 0 0 1.632-2.163Zm0 0V2.25L9 5.25v10.303m0 0v3.75a2.25 2.25 0 0 1-1.632 2.163l-1.32.377a1.803 1.803 0 0 1-.99-3.467l2.31-.66A2.25 2.25 0 0 0 9 15.553Z" />
        </svg>
      );
    default:
      return (
        <svg className={cls} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 9.776c.112-.017.227-.026.344-.026h15.812c.117 0 .232.009.344.026m-16.5 0a2.25 2.25 0 0 0-1.883 2.542l.857 6a2.25 2.25 0 0 0 2.227 1.932H19.05a2.25 2.25 0 0 0 2.227-1.932l.857-6a2.25 2.25 0 0 0-1.883-2.542m-16.5 0V6A2.25 2.25 0 0 1 6 3.75h3.879a1.5 1.5 0 0 1 1.06.44l2.122 2.12a1.5 1.5 0 0 0 1.06.44H18A2.25 2.25 0 0 1 20.25 9v.776" />
        </svg>
      );
  }
}