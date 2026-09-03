import { ButtonHTMLAttributes, ReactNode } from "react";

interface SecondaryButtonProps
  extends ButtonHTMLAttributes<HTMLButtonElement> {
  children: ReactNode;
  danger?: boolean;
}

export default function SecondaryButton({
  children,
  className = "",
  danger = false,
  ...props
}: SecondaryButtonProps) {
  return (
    <button
      {...props}
      className={`inline-flex items-center justify-center gap-2 rounded-md border bg-transparent px-4 py-2 text-sm font-medium transition-colors ${
        danger
          ? "border-red-700/60 text-red-400 hover:border-red-600 hover:bg-red-950/50"
          : "border-slate-700 text-slate-300 hover:border-slate-600 hover:bg-slate-800/60"
      } disabled:cursor-not-allowed disabled:opacity-50 ${className}`}
    >
      {children}
    </button>
  );
}
