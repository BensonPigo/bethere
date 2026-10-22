// "原 16:18" beside a shifted cue's new time (ADR-0028); later and earlier
// in different colours, as in the change banner.
import { originalTime } from "./notice";

export function OriginalTime({ start, offsetMin, tz }: { start: string; offsetMin: number; tz: string }) {
  const text = originalTime(start, offsetMin, tz);
  return text ? <small class={`original ${offsetMin < 0 ? "earlier" : "later"}`}>{text}</small> : null;
}
