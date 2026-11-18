import { describe, expect, it } from "vitest";
import type { MasterCue, Notice, Outcome, RoleCue, RoleSnapshot } from "./api";
import { shouldWipeLocalData } from "./api";
import { MAX_GUEST_RESULTS, guestEmptyText, searchGuests } from "./jobs";
import { OVERRUN_PROMPT_MIN, delayText, isEarlier, originalTime } from "./notice";
import { nextPendingCue, stylistView } from "./roleView";
import { POLL_MS } from "./session";
import { deleteSnapshot, isStaleLive } from "./storage";
import { changeArrow, conflictNotice, currentLine, elapsedText, laterCues, offlineText, overdueHeld, overrunShown } from "./uiLogic";

const MIN = 60000;
const NOW = Date.parse("2027-06-16T03:00:00Z");

function mc(over: Partial<MasterCue>): MasterCue {
  return {
    id: 1, seq: 1, title: "T", scheduledStart: "2027-06-16T03:00:00Z", effectiveStart: "2027-06-16T03:00:00Z",
    durationMin: 10, isFixed: false, offsetMin: 0, status: "Pending", actualStart: null, actualEnd: null,
    notePublic: null, roles: [], ...over,
  };
}
function rc(over: Partial<RoleCue>): RoleCue {
  return { id: 1, seq: 1, title: "T", effectiveStart: "2027-06-16T03:00:00Z", durationMin: 10, isFixed: false, offsetMin: 0, status: "Pending", notePublic: null, roleNote: null, ...over };
}
function rs(over: Partial<RoleSnapshot>): RoleSnapshot {
  return {
    eventVersion: 1, serverTime: "2027-06-16T02:00:00Z", eventStatus: "Live",
    role: { type: "Stylist", name: "新秘", view: "HandsFree" },
    eventDay: { date: "2027-06-16", timezone: "Asia/Taipei" }, cues: [], notice: null, ...over,
  };
}

describe("US-M-2.4 negative shift: cues already overdue are listed and marked", () => {
  const from = mc({ id: 1, seq: 1, effectiveStart: new Date(NOW - 5 * MIN).toISOString() });
  const at = (min: number) => new Date(NOW + min * MIN).toISOString();
  it("a pending cue whose start is before now is overdue", () => {
    const cues = [from, mc({ id: 2, seq: 2, effectiveStart: at(-1) }), mc({ id: 3, seq: 3, effectiveStart: at(10) })];
    expect(overdueHeld(cues, from, [], -5, NOW).map((c) => c.id)).toEqual([1, 2]);
  });
  it("a start exactly at now is not overdue (the server uses Before(now))", () => {
    expect(overdueHeld([mc({ id: 2, seq: 2, effectiveStart: at(0) })], from, [], -5, NOW)).toEqual([]);
    expect(overdueHeld([mc({ id: 2, seq: 2, effectiveStart: new Date(NOW - 1).toISOString() })], from, [], -5, NOW)).toHaveLength(1);
  });
  it("a positive shift marks nothing", () => {
    expect(overdueHeld([mc({ id: 2, seq: 2, effectiveStart: at(-30) })], from, [], 5, NOW)).toEqual([]);
  });
  it("fixed, unticked, non-pending and before-the-start cues are not marked", () => {
    const old = at(-30);
    const cues = [
      mc({ id: 2, seq: 2, effectiveStart: old, isFixed: true }),
      mc({ id: 3, seq: 3, effectiveStart: old }),
      mc({ id: 4, seq: 4, effectiveStart: old, status: "Skipped" }),
      mc({ id: 5, seq: 0, effectiveStart: old }),
    ];
    expect(overdueHeld(cues, from, [3], -5, NOW)).toEqual([]);
    expect(overdueHeld(cues, from, ["3"], -5, NOW)).toEqual([]);
  });
});

describe("US-M-10.9 / US-M-2.7 / US-M-3.4 version conflict text", () => {
  it("rundown editor writes say the rundown was updated", () => {
    for (const path of ["/api/cues/update", "/api/cues/move", "/api/cues/delete", "/api/rundown/import"] as const) {
      expect(conflictNotice(path, "Draft")).toBe("流程表已被更新");
    }
  });
  it("adding or assigning a cue before the event is editing the rundown", () => {
    for (const path of ["/api/cues/insert", "/api/cues/roles"] as const) {
      expect(conflictNotice(path, "Draft")).toBe("流程表已被更新");
    }
  });
  it("console writes on the day keep the generic text", () => {
    for (const path of ["/api/cues/advance", "/api/cues/shift", "/api/cues/skip", "/api/cues/undo", "/api/cues/insert", "/api/cues/edit", "/api/cues/roles"] as const) {
      expect(conflictNotice(path, "Live")).toBe("畫面已更新，請重新確認");
    }
  });
  it("the first advance of a draft event is the console, not the editor", () => {
    expect(conflictNotice("/api/cues/advance", "Draft")).toBe("畫面已更新，請重新確認");
  });
});

describe("US-M-5.2 overrun prompt", () => {
  it("4 minutes is hidden, 5 is shown, a draft never shows", () => {
    expect(OVERRUN_PROMPT_MIN).toBe(5);
    expect(overrunShown(false, 4)).toBe(0);
    expect(overrunShown(false, 5)).toBe(5);
    expect(overrunShown(false, 12)).toBe(12);
    expect(overrunShown(true, 12)).toBe(0);
  });
  it("US-H-1.2 the hosts' threshold is the same 5", () => {
    expect(delayText(4).ok).toBe(true);
    expect(delayText(5)).toEqual({ ok: false, text: "延誤 5 分" });
  });
});

describe("US-M-1.4 polling", () => {
  it("polls every 2 seconds, within the 3 seconds of the condition", () => {
    expect(POLL_MS).toBe(2000);
  });
});

describe("US-M-1.2 elapsed and planned minutes", () => {
  it("reads 已進行 N 分 / 預計 M 分", () => {
    const active = mc({ status: "InProgress", actualStart: "2027-06-16T02:50:00Z", durationMin: 15 });
    expect(elapsedText(active, NOW)).toBe("已進行 10 分 / 預計 15 分");
    expect(elapsedText(active, NOW + 59 * 1000)).toBe("已進行 10 分 / 預計 15 分");
    expect(elapsedText(active, NOW - 20 * MIN)).toBe("已進行 0 分 / 預計 15 分");
    expect(elapsedText(mc({ durationMin: 7, actualStart: null }), NOW)).toBe("已進行 0 分 / 預計 7 分");
  });
});

describe("US-M-6.5 / US-X-6.7 skipped cues", () => {
  it("nextPendingCue ignores Skipped, Completed and InProgress", () => {
    const cues = [rc({ id: 1, seq: 1, status: "Skipped" }), rc({ id: 2, seq: 2, status: "Completed" }), rc({ id: 3, seq: 3, status: "Pending" }), rc({ id: 4, seq: 4, status: "Pending" })];
    expect(nextPendingCue(cues)?.id).toBe(3);
    expect(nextPendingCue(cues.slice(0, 2))).toBeNull();
  });
  it("all done or skipped: the view is done", () => {
    const cues = [rc({ id: 1, seq: 1, status: "Skipped" }), rc({ id: 2, seq: 2, status: "Completed" })];
    expect(stylistView(rs({ cues }), NOW, 0).kind).toBe("done");
  });
  it("a skipped cue is not in the later list", () => {
    const cues = [rc({ id: 1, seq: 1 }), rc({ id: 2, seq: 2, status: "Skipped" }), rc({ id: 3, seq: 3 })];
    expect(laterCues(cues, cues[0]).map((c) => c.id)).toEqual([3]);
  });
});

describe("US-X-6.3 the cues after the next one", () => {
  const make = (n: number) => Array.from({ length: n }, (_, i) => rc({ id: i + 1, seq: i + 1 }));
  it("0, 1, 3 and 5 pending cues give 0, 0, 2 and 3 later ones", () => {
    const sizes = [0, 1, 3, 5].map((n) => {
      const cues = make(n);
      return laterCues(cues, cues[0] ?? rc({ seq: 0 })).length;
    });
    expect(sizes).toEqual([0, 0, 2, 3]);
  });
  it("is ordered by seq whatever the order given", () => {
    const cues = [rc({ id: 3, seq: 3 }), rc({ id: 1, seq: 1 }), rc({ id: 2, seq: 2 })];
    expect(laterCues(cues, cues[1]).map((c) => c.id)).toEqual([2, 3]);
  });
});

describe("US-X-6.4 the current line", () => {
  it("names the in-progress cue, 準備中 when none", () => {
    expect(currentLine(rs({ currentTitle: "入場" }))).toBe("目前：入場");
    expect(currentLine(rs({ cues: [rc({ status: "InProgress", title: "致詞" })] }))).toBe("目前：致詞");
    expect(currentLine(rs({ cues: [rc({ status: "Pending" })] }))).toBe("目前：準備中");
    expect(currentLine(rs({}))).toBe("目前：準備中");
  });
  it("the hands-free top line says 準備中 with nothing in progress", () => {
    const v = stylistView(rs({ cues: [rc({})] }), NOW - 10 * MIN, 0);
    expect(v.kind === "countdown" && v.topLine).toBe("準備中");
  });
});

describe("US-X-8.2 offline for more than 5 minutes", () => {
  const T = 1_000_000;
  it("adds 倒數可能不準 only beyond 5 minutes", () => {
    expect(offlineText("09:00", T, T + 4 * MIN + 59000)).toBe("離線中，資料更新於 09:00");
    expect(offlineText("09:00", T, T + 5 * MIN)).toBe("離線中，資料更新於 09:00");
    expect(offlineText("09:00", T, T + 5 * MIN + 1000)).toBe("離線中，資料更新於 09:00・倒數可能不準");
  });
  it("never warns when no answer was ever received", () => {
    expect(offlineText("09:00", null, T + 99 * MIN)).toBe("離線中，資料更新於 09:00");
  });
});

describe("US-X-8.6 local snapshot kept for 2 days", () => {
  const server = "2027-06-16T00:00:00Z";
  const DAY = 24 * 60 * MIN;
  it("exactly 2 days is kept, one millisecond more is dropped", () => {
    const t = Date.parse(server);
    expect(isStaleLive({ eventStatus: "Live", serverTime: server }, t + 2 * DAY)).toBe(false);
    expect(isStaleLive({ eventStatus: "Live", serverTime: server }, t + 2 * DAY + 1)).toBe(true);
  });
});

describe("US-ST-2.5 / US-ST-2.6 schedule change direction and original time", () => {
  const shift = (deltaMin: number): Notice => ({ kind: "shift", at: "x", deltaMin, changes: [] });
  const assign: Notice = { kind: "assign", at: "x", cueId: 1, title: "A", effectiveStart: "2027-06-16T03:00:00Z" };
  it("earlier and later differ in arrow and class", () => {
    expect(changeArrow(shift(-5))).toBe("▲ ");
    expect(changeArrow(shift(5))).toBe("▼ ");
    expect(changeArrow(assign)).toBe("");
    expect(isEarlier(shift(-5))).toBe(true);
    expect(isEarlier(shift(5))).toBe(false);
  });
  it("the original time is the rundown time, absent when never shifted", () => {
    expect(originalTime("2027-06-16T03:25:00Z", 10, "Asia/Taipei")).toBe("原 11:15");
    expect(originalTime("2027-06-16T03:10:00Z", -5, "Asia/Taipei")).toBe("原 11:15");
    expect(originalTime("2027-06-16T03:25:00Z", 0, "Asia/Taipei")).toBeNull();
  });
});

describe("US-H-1.4 the hosts' dashboard does not buzz, notify or play", () => {
  const files = import.meta.glob("./Dashboard.tsx", { query: "?raw", import: "default", eager: true }) as Record<string, string>;
  it("reads the source and finds no vibration, notification, audio or button", () => {
    const src = files["./Dashboard.tsx"];
    expect(src.length).toBeGreaterThan(100);
    for (const banned of ["vibrate", "Notification", "Audio", "play(", "<button", "onClick", "navigator."]) {
      expect(src).not.toContain(banned);
    }
  });
});

describe("US-RC-1.6 guest search", () => {
  const guests = Array.from({ length: MAX_GUEST_RESULTS + 5 }, (_, i) => ({ id: i, name: `王${i}`, table: "1", arrived: false })) as never[];
  it("caps the result list at MAX_GUEST_RESULTS", () => {
    expect(MAX_GUEST_RESULTS).toBe(30);
    expect(searchGuests(guests, "王")).toHaveLength(30);
  });
  it("no match reads 找不到「…」; an empty query says nothing", () => {
    expect(searchGuests(guests, "李")).toEqual([]);
    expect(guestEmptyText(" 李 ", 0)).toBe("找不到「李」");
    expect(guestEmptyText("李", 1)).toBeNull();
    expect(guestEmptyText("  ", 0)).toBeNull();
  });
});

describe("US-M-7.3 a revoked or ended link deletes the local snapshot", () => {
  it("invalid and ended answers wipe", () => {
    expect(shouldWipeLocalData({ kind: "invalid" } as Outcome)).toBe(true);
    expect(shouldWipeLocalData({ kind: "ended" } as Outcome)).toBe(true);
  });
  it("deleting removes only that key and survives a storage that throws", () => {
    const m = new Map([["snap:a", "1"], ["snap:b", "2"]]);
    deleteSnapshot({ removeItem: (k: string) => void m.delete(k) } as unknown as Storage, "snap:a");
    expect([...m.keys()]).toEqual(["snap:b"]);
    const bad = { removeItem: () => { throw new Error("denied"); } } as unknown as Storage;
    expect(() => deleteSnapshot(bad, "snap:a")).not.toThrow();
  });
});
