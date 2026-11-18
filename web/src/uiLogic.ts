// Pure decisions the screens make, kept apart so vitest can test them.
import type { MasterCue, Notice, RoleCue, RoleSnapshot, WriteRequest } from "./api";
import { OVERRUN_PROMPT_MIN } from "./notice";
import { inProgressCue } from "./roleView";

// US-M-2.4: a negative shift leaves out the pending, non-fixed, ticked cues
// from `from` on that are already overdue (domain PlanShift: effective start
// Before(now), so a start exactly at now is not overdue). The preview lists
// them as held.
export function overdueHeld(
  cues: MasterCue[],
  from: MasterCue,
  excluded: (string | number)[],
  deltaMin: number,
  nowMs: number,
): MasterCue[] {
  if (deltaMin >= 0) return [];
  const skip = new Set(excluded.map(String));
  return cues
    .filter(
      (c) =>
        c.seq >= from.seq && c.status === "Pending" && !c.isFixed && !skip.has(String(c.id)) && Date.parse(c.effectiveStart) < nowMs,
    )
    .sort((a, b) => a.seq - b.seq);
}

// The rundown editor's writes (US-M-10.9); every other write is the day's console.
const EDITOR_PATHS: WriteRequest["path"][] = ["/api/cues/update", "/api/cues/move", "/api/cues/delete", "/api/rundown/import"];

// What a 409 says: the editor's writes (US-M-10.9) name the rundown; the
// console's (US-M-2.7, US-M-3.4) ask to look again.
export function conflictNotice(path: WriteRequest["path"]): string {
  return EDITOR_PATHS.includes(path) ? "流程表已被更新，已載入最新內容" : "畫面已更新，請重新確認";
}

// US-M-5.2: the overrun to offer, 0 when nothing is shown.
export function overrunShown(draft: boolean, overrunMin: number): number {
  return !draft && overrunMin >= OVERRUN_PROMPT_MIN ? overrunMin : 0;
}

// US-M-1.2
export function elapsedText(active: MasterCue, correctedNow: number): string {
  const started = active.actualStart ? Date.parse(active.actualStart) : NaN;
  const elapsed = Number.isFinite(started) ? Math.max(0, Math.floor((correctedNow - started) / 60000)) : 0;
  return `已進行 ${elapsed} 分 / 預計 ${active.durationMin} 分`;
}

// US-X-6.3: up to three pending cues after the one the card counts to.
export function laterCues(cues: RoleCue[], target: RoleCue): RoleCue[] {
  return cues
    .filter((c) => c.status === "Pending" && c.seq > target.seq)
    .sort((a, b) => a.seq - b.seq)
    .slice(0, 3);
}

// US-X-6.4: the cue in progress, whoever it is assigned to; 準備中 without one.
export function currentLine(snap: RoleSnapshot): string {
  return `目前：${snap.currentTitle ?? inProgressCue(snap.cues)?.title ?? "準備中"}`;
}

export const OFFLINE_WARN_MS = 5 * 60 * 1000;

// US-X-8.2: the offline banner; past 5 minutes without an answer, the
// countdown may be off.
export function offlineText(updatedAt: string, lastOk: number | null, nowMs: number): string {
  const warn = lastOk !== null && nowMs - lastOk > OFFLINE_WARN_MS;
  return `離線中，資料更新於 ${updatedAt}${warn ? "・倒數可能不準" : ""}`;
}

// US-ST-2.5: an earlier shift points up, a later one down.
export function changeArrow(n: Notice): string {
  return n.kind === "shift" ? (n.deltaMin < 0 ? "▲ " : "▼ ") : "";
}
