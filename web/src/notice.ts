// Change notices (screen-design.md 4.4, ADR-0021): shown once per device.
import type { ShiftNotice } from "./api";
import { formatHM } from "./format";

const SUFFIX = ":notice";

// freshNotice returns n when this device has not shown it yet and records it
// as shown. Notices are told apart by their time; storage failures show it.
export function freshNotice(n: ShiftNotice | null, store: Storage, snapKey: string): ShiftNotice | null {
  if (!n) return null;
  try {
    if (store.getItem(snapKey + SUFFIX) === n.at) return null;
    store.setItem(snapKey + SUFFIX, n.at);
  } catch {
    /* storage unavailable: better shown twice than never */
  }
  return n;
}

export function signed(min: number): string {
  return `${min > 0 ? "+" : min < 0 ? "−" : ""}${Math.abs(min)}`;
}

// "時程已更新 +10 分（原 11:15 → 現 11:25）", naming the earliest moved cue.
export function noticeText(n: ShiftNotice, timeZone: string): string {
  const first = [...n.changes].sort((a, b) => Date.parse(a.toStart) - Date.parse(b.toStart))[0];
  const detail = first ? `（原 ${formatHM(first.fromStart, timeZone)} → 現 ${formatHM(first.toStart, timeZone)}）` : "";
  return `時程已更新 ${signed(n.deltaMin)} 分${detail}`;
}

// OVERRUN_PROMPT_MIN mirrors domain.OverrunPromptMin (ADR-0022).
export const OVERRUN_PROMPT_MIN = 5;

// The hosts see "一切順利" until the delay reaches the threshold (US-H-1.2).
export function delayText(delayMin: number): { ok: boolean; text: string } {
  return delayMin >= OVERRUN_PROMPT_MIN ? { ok: false, text: `延誤 ${delayMin} 分` } : { ok: true, text: "一切順利" };
}

// The prefilled shift for an overrun: up to the next multiple of 5 (US-M-5.3).
export function suggestedShift(overrunMin: number): number {
  return overrunMin <= 0 ? 0 : Math.ceil(overrunMin / 5) * 5;
}

// "6/6" from "2027-06-06".
export function monthDay(date: string): string {
  const [, m, d] = date.split("-").map(Number);
  return m && d ? `${m}/${d}` : date;
}

// The rundown's time of a shifted cue, shown beside its new time as
// "原 16:18" so the big time is never read as "plus N more" (ADR-0028).
// The original is the effective start minus the offset; null when the cue
// was never shifted.
export function originalTime(effectiveStart: string, offsetMin: number, timeZone: string): string | null {
  if (!offsetMin) return null;
  const ms = Date.parse(effectiveStart);
  if (!Number.isFinite(ms)) return null;
  return `原 ${formatHM(ms - offsetMin * 60000, timeZone)}`;
}
