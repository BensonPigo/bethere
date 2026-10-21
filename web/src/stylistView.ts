// V2a view model (screen-design.md section 5, US-ST-1). Pure so it can be tested.
import type { RoleCue, RoleSnapshot } from "./api";
import { remainingMs } from "./clock";

export type Level = "calm" | "amber" | "red" | "overdue";

// US-ST-1.3: >15 calm, 5..15 amber, <5 red, overdue blinking red.
export function levelFor(remaining: number): Level {
  if (remaining < 0) return "overdue";
  const min = remaining / 60000;
  if (min > 15) return "calm";
  if (min >= 5) return "amber";
  return "red";
}

// Big number text: minutes above 5, M:SS below 5, "已逾時 N 分" when overdue.
export function countdownText(remaining: number): { big: string; unit: string } {
  if (remaining < 0) return { big: `已逾時 ${Math.ceil(-remaining / 60000)} 分`, unit: "" };
  const totalSec = Math.floor(remaining / 1000);
  if (remaining / 60000 < 5) {
    const m = Math.floor(totalSec / 60);
    const s = totalSec % 60;
    return { big: `${m}:${String(s).padStart(2, "0")}`, unit: "" };
  }
  return { big: String(Math.floor(remaining / 60000)), unit: "分" };
}

// US-ST-1.1: assigned, Pending, lowest seq. (A role snapshot already holds only assigned cues.)
export function nextPendingCue(cues: RoleCue[]): RoleCue | null {
  let best: RoleCue | null = null;
  for (const c of cues) {
    if (c.status === "Pending" && (best === null || c.seq < best.seq)) best = c;
  }
  return best;
}

export function inProgressCue(cues: RoleCue[]): RoleCue | null {
  return cues.find((c) => c.status === "InProgress") ?? null;
}

export type StylistView =
  | { kind: "draft" }
  | { kind: "none" } // nothing assigned
  | { kind: "countdown"; target: RoleCue; remaining: number; level: Level; topLine: string }
  | { kind: "inProgress"; cue: RoleCue }
  | { kind: "done" };

export function stylistView(snap: RoleSnapshot, localNowMs: number, skewMs: number): StylistView {
  if (snap.eventStatus === "Draft") return { kind: "draft" };
  if (snap.cues.length === 0) return { kind: "none" };
  const target = nextPendingCue(snap.cues);
  const active = inProgressCue(snap.cues);
  if (target) {
    const remaining = remainingMs(Date.parse(target.effectiveStart), localNowMs, skewMs);
    return {
      kind: "countdown",
      target,
      remaining,
      level: levelFor(remaining),
      topLine: active ? `${active.title}（進行中）` : "準備中",
    };
  }
  if (active) return { kind: "inProgress", cue: active };
  return { kind: "done" };
}
