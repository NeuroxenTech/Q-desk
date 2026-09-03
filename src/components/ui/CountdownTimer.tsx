interface CountdownTimerProps {
  seconds: number;
}

function format(sec: number) {
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return `${m}:${s.toString().padStart(2, "0")}`;
}

export default function CountdownTimer({ seconds }: CountdownTimerProps) {
  const urgent = seconds > 0 && seconds < 60;

  return (
    <div
      className={`select-none rounded-md border px-4 py-1 ${
        urgent
          ? "border-amber-500/50 bg-amber-500/10"
          : "border-slate-700 bg-slate-900/70"
      }`}
      aria-label={`Session time remaining: ${format(seconds)}`}
    >
      <span className="block text-[9px] font-semibold uppercase tracking-[0.2em] text-slate-500">
        Session Ticket
      </span>
      <span
        className={`block text-2xl font-bold tabular-nums leading-tight ${
          urgent ? "text-amber-400" : "text-emerald-400"
        }`}
      >
        {format(seconds)}
      </span>
    </div>
  );
}
