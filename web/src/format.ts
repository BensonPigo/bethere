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

// parseClock reads a 24-hour time typed by hand: "09:20", "9:20", "0920"
// or "920", with a half- or full-width colon, as "HH:MM"; null otherwise.
// The browser's own time field is not used: in a Traditional Chinese
// Chrome it is 12-hour with the 上午/下午 part easily out of sight, so
// "0920" became 21:20 (M2c field test, US-M-10.C2).
export function parseClock(text: string): string | null {
  const s = text.trim().replace("：", ":");
  const m = /^(\d{1,2}):(\d{2})$/.exec(s) ?? /^(\d{1,2})(\d{2})$/.exec(s);
  if (!m) return null;
  const h = Number(m[1]);
  const mi = Number(m[2]);
  if (h > 23 || mi > 59) return null;
  return `${String(h).padStart(2, "0")}:${m[2]}`;
}

// zonedToISO turns "HH:MM" on `date` (YYYY-MM-DD) in a time zone into a
// UTC RFC 3339 instant, e.g. for a cue added on the event day. Null when
// the input is not a time.
export function zonedToISO(date: string, hhmm: string, timeZone: string = DEFAULT_TZ): string | null {
  const m = /^(\d{1,2}):(\d{2})$/.exec(hhmm.trim());
  const [y, mo, d] = date.split("-").map(Number);
  if (!m || !y || !mo || !d) return null;
  const h = Number(m[1]);
  const mi = Number(m[2]);
  if (h > 23 || mi > 59) return null;
  const guess = Date.UTC(y, mo - 1, d, h, mi);
  // The zone's offset at that moment: what the wall clock there shows, minus UTC.
  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone, hourCycle: "h23", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit",
  }).formatToParts(guess);
  const get = (t: string) => Number(parts.find((p) => p.type === t)?.value);
  const shown = Date.UTC(get("year"), get("month") - 1, get("day"), get("hour"), get("minute"));
  return new Date(guess - (shown - guess)).toISOString();
}

// "3 分鐘前" for the last use of a link (US-M-1.6).
export function agoText(iso: string | null, nowMs: number): string {
  if (!iso) return "沒有使用紀錄";
  const min = Math.max(0, Math.floor((nowMs - Date.parse(iso)) / 60000));
  if (!Number.isFinite(min)) return "沒有使用紀錄";
  return min < 1 ? "剛剛" : min < 60 ? `${min} 分鐘前` : `${Math.floor(min / 60)} 小時前`;
}
