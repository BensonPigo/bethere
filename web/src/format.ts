// 24-hour HH:MM in the event's time zone (screen-design.md section 2).

export const DEFAULT_TZ = "Asia/Taipei";

export function formatHM(iso: string | number, timeZone: string = DEFAULT_TZ): string {
  const ms = typeof iso === "number" ? iso : Date.parse(iso);
  if (!Number.isFinite(ms)) return "--:--";
  return new Intl.DateTimeFormat("en-GB", {
    timeZone,
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).format(ms);
}

// YYYY-MM-DD of an instant in a time zone.
export function dateInZone(ms: number, timeZone: string): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone, year: "numeric", month: "2-digit", day: "2-digit" }).format(ms);
}
