import { ButtonHTMLAttributes, ReactNode } from "react";

interface PrimaryButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  children: ReactNode;
}

export default function PrimaryButton({
  children,
  className = "",
  ...props
}: PrimaryButtonProps) {
  return (
    <button
      {...props}
      className={`inline-flex items-center justify-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white
        transition-colors
        hover:bg-blue-500
        active:bg-blue-700
        disabled:cursor-not-allowed disabled:bg-slate-700 disabled:text-slate-400
        ${className}`}
    >
      {children}
    </button>
  );
}
