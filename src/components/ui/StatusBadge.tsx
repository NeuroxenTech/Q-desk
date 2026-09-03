import { ReactNode } from "react";

type Tone = "default" | "success" | "warning" | "danger";

interface StatusBadgeProps {
  children: ReactNode;
  tone?: Tone;
  className?: string;
  icon?: ReactNode;
}

const toneStyles: Record<Tone, string> = {
  default: "border-blue-500/40 bg-blue-500/10 text-blue-300",
  success: "border-emerald-500/40 bg-emerald-500/10 text-emerald-300",
  warning: "border-amber-500/40 bg-amber-500/10 text-amber-300",
  danger: "border-red-500/40 bg-red-500/10 text-red-300",
};

const dotStyles: Record<Tone, string> = {
  default: "bg-blue-400",
  success: "bg-emerald-400",
  warning: "bg-amber-400",
  danger: "bg-red-400",
};

export default function StatusBadge({
  children,
  tone = "default",
  className = "",
  icon,
}: StatusBadgeProps) {
  return (
    <span
      className={`inline-flex items-center gap-2 rounded border px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wider ${toneStyles[tone]} ${className}`}
    >
      {icon ?? <span className={`h-1.5 w-1.5 rounded-full ${dotStyles[tone]}`} />}
      <span className="whitespace-nowrap">{children}</span>
    </span>
  );
}
