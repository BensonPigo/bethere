import { describe, expect, it } from "vitest";
import { classifyNotModified, classifyResponse, shouldWipeLocalData, writeBody } from "./api";
import type { Guest, HostSnapshot, MasterCue, MasterSnapshot, RoleCue, RoleSnapshot } from "./api";
import { printRows } from "./print";
import { arrivedCount, callRunnerTitle, searchGuests, tableText } from "./jobs";
import { addSkewSample, currentSkew, median, remainingMs } from "./clock";
import { isLineBrowser, tokenFromHash } from "./env";
import { dayStarted } from "./controls";
import { readTheme, saveTheme } from "./theme";
import { agoText, dateInZone, formatHM, parseClock, zonedToISO } from "./format";
import { holdsStaleGuests, isStaleLive, loadSnapshot, pruneStale, saveSnapshot, snapKey } from "./storage";
import { countdownText, levelFor, nextPendingCue, stylistView } from "./roleView";
import { delayText, freshNotice, isEarlier, monthDay, noticedCues, noticeText, originalTime, signed, suggestedShift } from "./notice";
import type { AssignNotice, ShiftNotice } from "./api";

const MIN = 60000;
const JSON_CT = "application/json; charset=utf-8";

function cue(over: Partial<RoleCue>): RoleCue {
  return {
    id: 1, seq: 1, title: "T", effectiveStart: "2027-06-06T03:00:00Z", durationMin: 10,
    isFixed: false, offsetMin: 0, status: "Pending", notePublic: null, roleNote: null, ...over,
  };
}
function snap(over: Partial<RoleSnapshot>): RoleSnapshot {
  return {
    eventVersion: 1, serverTime: "2027-06-06T02:00:00Z", eventStatus: "Live",
    role: { type: "Stylist", name: "新秘", view: "HandsFree" },
    eventDay: { date: "2027-06-06", timezone: "Asia/Taipei" }, cues: [], notice: null, ...over,
  };
}
const okBody = { result: "ok", snapshot: snap({}) };

describe("clock skew (sync-and-offline 4)", () => {
  it("median of odd and even sample counts", () => {
    expect(median([5, 1, 3])).toBe(3);
    expect(median([1, 2, 3, 10])).toBe(2.5);
    expect(median([])).toBe(0);
  });
  it("skew = serverTime - local; the median ignores one slow outlier", () => {
    let s: number[] = [];
    for (const d of [1000, 1010, 990, 5000, 1005]) s = addSkewSample(s, 10_000 + d, 10_000);
    expect(currentSkew(s)).toBe(1005);
  });
  it("keeps only the most recent samples", () => {
    let s: number[] = [];
    for (let i = 0; i < 20; i++) s = addSkewSample(s, i, 0);
    expect(s.length).toBeLessThanOrEqual(9);
    expect(s[s.length - 1]).toBe(19);
  });
  it("countdown = effectiveStart - (now + skew)", () => {
    expect(remainingMs(100_000, 40_000, 10_000)).toBe(50_000);
    expect(remainingMs(100_000, 120_000, 0)).toBe(-20_000);
  });
});

describe("US-ST-1.3 colour thresholds and text", () => {
  it("levels", () => {
    expect(levelFor(15 * MIN + 1)).toBe("calm");
    expect(levelFor(15 * MIN)).toBe("amber");
    expect(levelFor(5 * MIN)).toBe("amber");
    expect(levelFor(5 * MIN - 1)).toBe("red");
    expect(levelFor(0)).toBe("red");
    expect(levelFor(-1)).toBe("overdue");
  });
  it("minutes above 5, M:SS below 5, overdue text", () => {
    expect(countdownText(18 * MIN + 30000)).toEqual({ big: "18", unit: "分" });
    expect(countdownText(4 * MIN + 5000)).toEqual({ big: "4:05", unit: "" });
    expect(countdownText(0).big).toBe("0:00");
    expect(countdownText(-2 * MIN - 1).big).toBe("已逾時 3 分");
  });
});

describe("US-ST-1.1 next assigned pending cue", () => {
  it("picks the lowest seq among Pending, ignoring order and other statuses", () => {
    const cues = [
      cue({ id: 1, seq: 4 }), cue({ id: 2, seq: 2, status: "Completed" }),
      cue({ id: 3, seq: 3 }), cue({ id: 4, seq: 1, status: "InProgress" }),
    ];
    expect(nextPendingCue(cues)?.id).toBe(3);
  });
  it("none when nothing is pending", () => {
    expect(nextPendingCue([cue({ status: "Completed" })])).toBeNull();
  });
});

describe("stylist view", () => {
  const start = Date.parse("2027-06-06T03:00:00Z");
  const now = start - 10 * MIN;
  it("US-ST-1.8 a draft counts down on the event day", () => {
    expect(stylistView(snap({ eventStatus: "Draft", cues: [cue({})] }), now, 0).kind).toBe("countdown");
  });
  it("US-ST-1.9 the event day is judged with the corrected clock in the event zone", () => {
    // 2027-06-05 23:30 Taipei on a device whose clock is 1 h slow: already the 6th.
    const lateOn5th = Date.parse("2027-06-05T15:30:00Z");
    expect(stylistView(snap({ eventStatus: "Draft", cues: [cue({})] }), lateOn5th, 0).kind).toBe("preDay");
    expect(stylistView(snap({ eventStatus: "Draft", cues: [cue({})] }), lateOn5th, 60 * MIN).kind).toBe("countdown");
  });
  it("US-X-2.1 before the event day: the list, ordered by seq", () => {
    const v = stylistView(
      snap({ eventStatus: "Draft", cues: [cue({ id: 2, seq: 2, title: "B" }), cue({ id: 1, seq: 1, title: "A" })] }),
      Date.parse("2027-06-01T02:00:00Z"), 0);
    expect(v.kind).toBe("preDay");
    expect(v.kind === "preDay" && v.cues.map((c) => c.title)).toEqual(["A", "B"]);
  });
  it("no assigned cues", () => {
    expect(stylistView(snap({}), now, 0).kind).toBe("none");
  });
  it("US-ST-1.1 countdown with skew, 準備中 top line", () => {
    const v = stylistView(snap({ cues: [cue({})] }), now - 2 * MIN, 2 * MIN);
    expect(v).toMatchObject({ kind: "countdown", remaining: 10 * MIN, level: "amber", topLine: "準備中" });
  });
  it("top line names the assigned in-progress cue", () => {
    const v = stylistView(
      snap({ cues: [cue({ id: 9, seq: 1, title: "一進造型", status: "InProgress" }), cue({ id: 2, seq: 2 })] }), now, 0);
    expect(v).toMatchObject({ kind: "countdown", topLine: "一進造型（進行中）" });
  });
  it("US-ST-1.4 in progress only, then done", () => {
    expect(stylistView(snap({ cues: [cue({ title: "造型", status: "InProgress" })] }), now, 0))
      .toMatchObject({ kind: "inProgress" });
    expect(stylistView(snap({ cues: [cue({ status: "Completed" })] }), now, 0).kind).toBe("done");
  });
});

describe("local data wipe rules (access-control 3.3, api section 7)", () => {
  const wipes = (status: number, ct: string | null, body: unknown) =>
    shouldWipeLocalData(classifyResponse(status, ct, body));

  it("200 + JSON + invalid/ended wipes", () => {
    expect(wipes(200, JSON_CT, { result: "invalid" })).toBe(true);
    expect(wipes(200, "application/json", { result: "ended" })).toBe(true);
  });
  it("ok never wipes and yields a snapshot", () => {
    const o = classifyResponse(200, JSON_CT, okBody);
    expect(o.kind).toBe("ok");
    expect(shouldWipeLocalData(o)).toBe(false);
  });
  it("401/403/500 never wipe, even with result in the body", () => {
    for (const st of [401, 403, 500]) {
      expect(wipes(st, JSON_CT, { result: "invalid" })).toBe(false);
      expect(wipes(st, JSON_CT, { error: "internal" })).toBe(false);
    }
  });
  it("409 never wipes and carries the snapshot", () => {
    const o = classifyResponse(409, JSON_CT, { error: "version_conflict", snapshot: snap({}) });
    expect(o.kind).toBe("conflict");
    expect(shouldWipeLocalData(o)).toBe(false);
    expect(wipes(409, JSON_CT, { result: "invalid" })).toBe(false);
  });
  it("non-JSON content type, unparseable body, unknown result never wipe", () => {
    expect(wipes(200, "text/html", { result: "invalid" })).toBe(false);
    expect(wipes(200, null, { result: "ended" })).toBe(false);
    expect(wipes(200, JSON_CT, undefined)).toBe(false);
    expect(wipes(200, JSON_CT, { result: "INVALID" })).toBe(false);
    expect(wipes(200, JSON_CT, "invalid")).toBe(false);
  });
  it("422 is a rejection, not a wipe", () => {
    expect(classifyResponse(422, JSON_CT, { error: "not_event_day" })).toEqual({ kind: "rejected", error: "not_event_day" });
  });
  it("network failure is represented as failure and never wipes", () => {
    expect(shouldWipeLocalData({ kind: "failure" })).toBe(false);
  });
});

describe("local snapshot key", () => {
  it("is snap: + SHA-256('bethere-local:' + token), never the token", async () => {
    const token = "AAAAAAAAAAAAAAAAAAAAAA";
    const bytes = await crypto.subtle.digest("SHA-256", new TextEncoder().encode("bethere-local:" + token));
    const hex = Array.from(new Uint8Array(bytes), (b) => b.toString(16).padStart(2, "0")).join("");
    const key = await snapKey(token);
    expect(key).toBe("snap:" + hex);
    expect(key).toMatch(/^snap:[0-9a-f]{64}$/);
    expect(key).not.toContain(token);
    expect(await snapKey("BBBBBBBBBBBBBBBBBBBBBB")).not.toBe(key);
  });
  it("known vector for SHA-256('bethere-local:abc')", async () => {
    const key = await snapKey("abc");
    const bytes = await crypto.subtle.digest("SHA-256", new TextEncoder().encode("bethere-local:abc"));
    expect(key.length).toBe(5 + 64);
    expect(key.slice(5)).toBe(Array.from(new Uint8Array(bytes), (b) => b.toString(16).padStart(2, "0")).join(""));
  });
});

describe("stale local snapshots", () => {
  const now = Date.parse("2027-06-10T00:00:00Z");
  it("Live older than 2 days is stale; Draft and recent Live are kept", () => {
    expect(isStaleLive({ eventStatus: "Live", serverTime: "2027-06-07T00:00:00Z" }, now)).toBe(true);
    expect(isStaleLive({ eventStatus: "Live", serverTime: "2027-06-08T12:00:00Z" }, now)).toBe(false);
    expect(isStaleLive({ eventStatus: "Draft", serverTime: "2027-01-01T00:00:00Z" }, now)).toBe(false);
  });
  it("pruneStale removes only stale snap: entries", () => {
    const m = new Map<string, string>([
      ["snap:a", JSON.stringify({ eventStatus: "Live", serverTime: "2027-06-01T00:00:00Z" })],
      ["snap:b", JSON.stringify({ eventStatus: "Draft", serverTime: "2027-06-01T00:00:00Z" })],
      ["other", "x"],
    ]);
    const store = {
      get length() { return m.size; },
      key: (i: number) => [...m.keys()][i] ?? null,
      getItem: (k: string) => m.get(k) ?? null,
      removeItem: (k: string) => void m.delete(k),
    } as unknown as Storage;
    pruneStale(store, now);
    expect([...m.keys()].sort()).toEqual(["other", "snap:b"]);
  });
});

describe("misc helpers", () => {
  it("formats 24-hour HH:MM in Asia/Taipei", () => {
    expect(formatHM("2027-06-06T03:15:00.123Z")).toBe("11:15");
    expect(formatHM("2027-06-05T16:05:00Z")).toBe("00:05");
  });
  it("date in zone", () => {
    expect(dateInZone(Date.parse("2027-06-05T16:30:00Z"), "Asia/Taipei")).toBe("2027-06-06");
  });
  it("token from fragment, LINE detection", () => {
    expect(tokenFromHash("#t=abc")).toBe("abc");
    expect(tokenFromHash("")).toBeNull();
    expect(isLineBrowser("Mozilla/5.0 Safari/604.1 Line/26.16.0")).toBe(true);
    expect(isLineBrowser("Mozilla/5.0 Safari/604.1")).toBe(false);
  });
});

describe("M2a helpers", () => {
  it("US-H-1.2 all is well below 5 minutes", () => {
    expect(delayText(4)).toEqual({ ok: true, text: "一切順利" });
    expect(delayText(5)).toEqual({ ok: false, text: "延誤 5 分" });
    expect(delayText(-3).ok).toBe(true);
  });
  it("US-M-5.3 suggested shift rounds up to 5", () => {
    expect([0, 1, 5, 7, 10, 11].map(suggestedShift)).toEqual([0, 5, 5, 10, 10, 15]);
  });
  it("signs and dates", () => {
    expect([10, -5, 0].map(signed)).toEqual(["+10", "−5", "0"]);
    expect(monthDay("2027-06-06")).toBe("6/6");
  });
  const n: ShiftNotice = {
    kind: "shift", at: "2027-06-06T02:00:00.000Z", deltaMin: 10,
    changes: [
      { cueId: 2, fromStart: "2027-06-06T05:00:00.000Z", toStart: "2027-06-06T05:10:00.000Z" },
      { cueId: 1, fromStart: "2027-06-06T03:15:00.000Z", toStart: "2027-06-06T03:25:00.000Z" },
    ],
  };
  it("US-ST-2.2 notice text names the earliest moved cue", () => {
    expect(noticeText(n, "Asia/Taipei")).toBe("時程已更新 +10 分（原 11:15 → 現 11:25）");
    expect(noticeText({ ...n, deltaMin: -10 }, "Asia/Taipei")).toMatch(/^時程已更新 −10 分/);
  });
  it("US-ST-2.4 a notice is shown once per device", () => {
    const m = new Map<string, string>();
    const store = {
      getItem: (k: string) => m.get(k) ?? null,
      setItem: (k: string, v: string) => void m.set(k, v),
    } as unknown as Storage;
    expect(freshNotice(n, store, "snap:x")).toBe(n);
    expect(freshNotice(n, store, "snap:x")).toBeNull();
    expect(freshNotice({ ...n, at: "2027-06-06T02:05:00.000Z" }, store, "snap:x")).not.toBeNull();
    expect(freshNotice(null, store, "snap:x")).toBeNull();
  });
  it("host snapshots have no cues and are still accepted", () => {
    const host = { eventVersion: 1, serverTime: "x", eventStatus: "Live", role: { type: "Host", name: "新人", view: "Dashboard" },
      eventDay: { date: "2027-06-06", timezone: "Asia/Taipei" }, delayMin: 0, currentTitle: null };
    expect(classifyResponse(200, JSON_CT, { result: "ok", snapshot: host }).kind).toBe("ok");
    expect(classifyResponse(200, JSON_CT, { result: "ok", snapshot: { ...host, role: { type: "Stylist", name: "x" } } }).kind)
      .toBe("failure");
  });
});

describe("ADR-0028 original time beside a shifted cue", () => {
  it("shows the rundown time, not the offset", () => {
    expect(originalTime("2027-06-06T08:28:00.000Z", 10, "Asia/Taipei")).toBe("原 16:18");
    expect(originalTime("2027-06-06T08:18:00.000Z", -5, "Asia/Taipei")).toBe("原 16:23");
    expect(originalTime("2027-06-06T08:28:00.000Z", 0, "Asia/Taipei")).toBeNull();
  });
});

function memoryStorage(): Storage {
  const m = new Map<string, string>();
  return {
    get length() { return m.size; },
    clear: () => m.clear(),
    getItem: (k) => m.get(k) ?? null,
    key: (i) => [...m.keys()][i] ?? null,
    removeItem: (k) => void m.delete(k),
    setItem: (k, v) => void m.set(k, v),
  };
}

describe("M2b: conditional requests (api.md 5.7)", () => {
  it("a 304 keeps the snapshot, never wipes, and needs the server time", () => {
    const o = classifyNotModified("2027-06-06T02:00:01.500Z");
    expect(o).toEqual({ kind: "notModified", serverTime: "2027-06-06T02:00:01.500Z" });
    expect(shouldWipeLocalData(o)).toBe(false);
    expect(classifyNotModified(null).kind).toBe("failure");
    expect(classifyNotModified("x").kind).toBe("failure");
  });
});

describe("M2b: write bodies (api.md section 3)", () => {
  it("cue writes carry the version; check-ins and links do not", () => {
    expect(writeBody({ path: "/api/cues/skip", cueId: 3 }, 7)).toEqual({ version: 7, cueId: 3 });
    expect(writeBody({ path: "/api/checkin", checkedIn: true }, 7)).toEqual({ checkedIn: true });
    expect(writeBody({ path: "/api/roles/checkin", roleId: 2, checkedIn: false }, 7)).toEqual({ roleId: 2, checkedIn: false });
    expect(writeBody({ path: "/api/roles/reissue", roleId: 2 }, 7)).toEqual({ roleId: 2 });
    expect(writeBody({ path: "/api/roles/revoke", roleId: 2 }, 7)).toEqual({ roleId: 2 });
    expect(writeBody({ path: "/api/cues/roles", cueId: 3, roles: [{ roleId: 2, roleNote: null }] }, 7)).toEqual({
      version: 7, cueId: 3, roles: [{ roleId: 2, roleNote: null }],
    });
  });
  it("a reissued token is never stored with the snapshot (US-M-7.1)", () => {
    const token = "abcdefghijklmnopqrstuv";
    const o = classifyResponse(200, JSON_CT, { result: "ok", snapshot: snap({}), token });
    expect(o.kind === "ok" && o.token).toBe(token);
    if (o.kind !== "ok") return;
    const store = memoryStorage();
    saveSnapshot(store, "snap:x", o.snapshot);
    expect(store.getItem("snap:x")).not.toContain(token);
    expect(loadSnapshot(store, "snap:x")).not.toBeNull();
  });
});

describe("M2b: notices and helpers", () => {
  const assign: AssignNotice = { kind: "assign", at: "2027-06-06T00:00:00Z", cueId: 9, title: "長輩致詞", effectiveStart: "2027-06-06T04:00:00Z" };
  it("an assignment reads 新增環節 with its time (US-X-7.1)", () => {
    expect(noticeText(assign, "Asia/Taipei")).toBe("新增環節：長輩致詞（12:00）");
    expect([...noticedCues(assign)]).toEqual(["9"]);
    expect(isEarlier(assign)).toBe(false);
    expect(noticedCues(null).size).toBe(0);
  });
  it("is shown once per device, like a shift (US-X-7.2)", () => {
    const store = memoryStorage();
    expect(freshNotice(assign, store, "snap:x")).toBe(assign);
    expect(freshNotice(assign, store, "snap:x")).toBeNull();
  });
  it("turns HH:MM on the event day into UTC", () => {
    expect(zonedToISO("2027-06-06", "12:00", "Asia/Taipei")).toBe("2027-06-06T04:00:00.000Z");
    expect(zonedToISO("2027-06-06", "00:30", "Asia/Taipei")).toBe("2027-06-05T16:30:00.000Z");
    expect(zonedToISO("2027-06-06", "24:00", "Asia/Taipei")).toBeNull();
    expect(zonedToISO("2027-06-06", "", "Asia/Taipei")).toBeNull();
  });
  it("says how long ago a link was used (US-M-1.6)", () => {
    const now = Date.parse("2027-06-06T01:05:00Z");
    expect(agoText(null, now)).toBe("沒有使用紀錄");
    expect(agoText("2027-06-06T01:05:00Z", now)).toBe("剛剛");
    expect(agoText("2027-06-06T01:00:00Z", now)).toBe("5 分鐘前");
    expect(agoText("2027-06-05T23:00:00Z", now)).toBe("2 小時前");
  });
});

describe("M2c: preparation (api.md section 3)", () => {
  it("a role preview is never taken for the caller's own snapshot (US-M-14.3)", () => {
    const o = classifyResponse(200, JSON_CT, { result: "ok", preview: snap({}) });
    expect(o.kind).toBe("preview");
    expect("snapshot" in o).toBe(false);
    expect(shouldWipeLocalData(o)).toBe(false);
  });
  it("a new link carries its token, QR code and role id; nothing else is a QR code", () => {
    const qr = "data:image/png;base64,iVBORw0KGgo=";
    const o = classifyResponse(200, JSON_CT, { ...okBody, token: "t".repeat(22), qr, roleId: 7 });
    expect(o).toMatchObject({ kind: "ok", token: "t".repeat(22), qr, roleId: 7 });
    const bad = classifyResponse(200, JSON_CT, { ...okBody, token: "t".repeat(22), qr: "javascript:alert(1)" });
    expect(bad.kind === "ok" && bad.qr).toBeFalsy();
    const plain = classifyResponse(200, JSON_CT, { ...okBody, qr });
    expect(plain.kind === "ok" && plain.qr).toBeFalsy();
  });
  it("writes the M2c bodies", () => {
    const cue = { cueId: 3, start: "2027-06-06T02:00:00Z", durationMin: 10, isFixed: true, title: "a", notePublic: null };
    expect(writeBody({ path: "/api/cues/update", cue }, 4)).toEqual({ version: 4, ...cue });
    expect(writeBody({ path: "/api/cues/move", cueId: 3, afterCueId: null }, 4)).toEqual({ version: 4, cueId: 3, afterCueId: null });
    expect(writeBody({ path: "/api/cues/delete", cueId: 3 }, 4)).toEqual({ version: 4, cueId: 3 });
    expect(writeBody({ path: "/api/rundown/import", text: "x", newRoles: [] }, 4)).toEqual({ version: 4, text: "x", newRoles: [] });
    // Roles are not cues: adding and renaming carry no version (ADR-0023).
    expect(writeBody({ path: "/api/roles/add", role: { name: "伴娘", view: "Glance", checksIn: true }, origin: "https://a.example" }, 4))
      .toEqual({ name: "伴娘", view: "Glance", checksIn: true, origin: "https://a.example" });
    expect(writeBody({ path: "/api/roles/rename", roleId: 2, name: "b" }, 4)).toEqual({ roleId: 2, name: "b" });
    expect(writeBody({ path: "/api/roles/delete", roleId: 2 }, 4)).toEqual({ version: 4, roleId: 2 });
    expect(writeBody({ path: "/api/roles/reissue", roleId: 2, origin: "https://a.example" }, 4)).toEqual({ roleId: 2, origin: "https://a.example" });
    expect(writeBody({ path: "/api/roles/reissue", roleId: 2 }, 4)).toEqual({ roleId: 2 });
  });
  it("reads a typed 24-hour time the same on every browser (US-M-10.C2 field test)", () => {
    for (const [typed, want] of [["09:20", "09:20"], ["9:20", "09:20"], ["0920", "09:20"], ["920", "09:20"], [" 09：20 ", "09:20"], ["21:05", "21:05"], ["0000", "00:00"]]) {
      expect(parseClock(typed)).toBe(want);
    }
    for (const typed of ["", "9", "24:00", "09:60", "9:2", "下午 9:20", "09:20:00", "12345"]) expect(parseClock(typed)).toBeNull();
  });
  it("locks the destructive edits from the event day on (ADR-0032)", () => {
    expect(dayStarted("2027-06-06", "2027-06-05")).toBe(false);
    expect(dayStarted("2027-06-06", "2027-06-06")).toBe(true);
    expect(dayStarted("2027-06-06", "2027-06-07")).toBe(true);
  });
});

describe("backup rundown rows (sync-and-offline 9)", () => {
  const mcue = (over: Partial<MasterCue>): MasterCue => ({
    id: 1, seq: 1, title: "T", scheduledStart: "2027-06-06T03:00:00Z", effectiveStart: "2027-06-06T03:00:00Z",
    durationMin: 10, isFixed: false, offsetMin: 0, status: "Pending", actualStart: null, actualEnd: null,
    notePublic: null, roles: [], ...over,
  });
  const master = (cues: MasterCue[]): MasterSnapshot => ({
    eventVersion: 3, serverTime: "2027-06-06T02:00:00Z", eventStatus: "Live",
    role: { type: "Master", name: "總召", view: "Console" },
    eventDay: { date: "2027-06-06", timezone: "Asia/Taipei" },
    event: { name: "婚禮", date: "2027-06-06", timezone: "Asia/Taipei" },
    cues, activeCueId: null, undo: { available: false, description: null }, roles: [],
    conflicts: [], overrunMin: 0, outOfOrder: [],
  });
  const cues = [
    mcue({ id: 2, seq: 2, title: "開席", isFixed: true, effectiveStart: "2027-06-06T04:10:00Z", offsetMin: 10,
      roles: [{ roleId: 7, name: "攝影", roleNote: "大門" }] }),
    mcue({ id: 1, seq: 1, title: "一進", status: "Completed", notePublic: "全體",
      roles: [{ roleId: 5, name: "新秘", roleNote: "白紗" }, { roleId: 7, name: "攝影", roleNote: null }] }),
  ];

  it("US-X-9.1: the Master prints every cue in order with the time in force, roles and notes", () => {
    const rows = printRows(master(cues));
    expect(rows.map((r) => r.title)).toEqual(["一進", "開席"]);
    expect(rows[0]).toMatchObject({ done: true, notePublic: "全體", roles: [{ name: "新秘", note: "白紗" }, { name: "攝影", note: null }] });
    expect(rows[1]).toMatchObject({ start: "2027-06-06T04:10:00Z", isFixed: true, durationMin: 10 });
  });
  it("US-X-9.2: the Master can print one role's copy: its cues and only its notes", () => {
    const rows = printRows(master(cues), 5);
    expect(rows.map((r) => r.title)).toEqual(["一進"]);
    expect(rows[0].roles).toEqual([{ name: "新秘", note: "白紗" }]);
    expect(printRows(master(cues), "7").map((r) => r.title)).toEqual(["一進", "開席"]);
  });
  it("US-X-9.3: another role prints its own cues with its own note", () => {
    const rows = printRows(snap({ cues: [cue({ id: 4, seq: 4, title: "B" }), cue({ id: 3, seq: 3, title: "A", roleNote: "n" })] }));
    expect(rows.map((r) => r.title)).toEqual(["A", "B"]);
    expect(rows[0].roles).toEqual([{ name: "新秘", note: "n" }]);
  });
  it("US-X-9.4: the hosts' link has nothing to print", () => {
    const host: HostSnapshot = {
      eventVersion: 1, serverTime: "2027-06-06T02:00:00Z", eventStatus: "Live",
      role: { type: "Host", name: "新人", view: "Dashboard" },
      eventDay: { date: "2027-06-06", timezone: "Asia/Taipei" }, delayMin: 0, currentTitle: null,
    };
    expect(printRows(host)).toEqual([]);
  });
});

describe("the four jobs (api.md section 9)", () => {
  const g = (id: number, name: string, table: string, arrived = false): Guest => ({ id, name, table, note: null, arrived });
  const guests = [g(1, "陳大文", "5"), g(2, "林阿姨", "主桌", true), g(3, "Amy Chen", "12")];

  it("US-RC-1.6: one character of a name or a table finds the guest; Latin ignores case", () => {
    expect(searchGuests(guests, "陳").map((x) => x.id)).toEqual([1]);
    expect(searchGuests(guests, "主").map((x) => x.id)).toEqual([2]);
    expect(searchGuests(guests, "chen").map((x) => x.id)).toEqual([3]);
    expect(searchGuests(guests, "1").map((x) => x.id)).toEqual([3]);
    expect(searchGuests(guests, "  ")).toEqual([]);
    expect(arrivedCount(guests)).toEqual({ arrived: 1, total: 3 });
  });
  it("US-RC-1.6: a table number reads「5 桌」; a named table keeps its name", () => {
    expect(tableText("5")).toBe("5 桌");
    expect(tableText("主桌")).toBe("主桌");
    expect(tableText("Head Table")).toBe("Head Table");
    expect(tableText("A3")).toBe("A3");
  });
  it("US-RC-2.1: the call names the reception and fits a task title", () => {
    expect(callRunnerTitle("接待")).toBe("接待需要機動組支援");
    expect(callRunnerTitle("名".repeat(120)).length).toBe(100);
  });
  it("api.md 9.5: a saved guest list goes 2 days after the event day, draft or live", () => {
    // Taipei: the day starts 2027-06-05T16:00Z and ends 24 hours later.
    const day = Date.parse("2027-06-05T16:00:00Z");
    const s = { guests: [guests[0]], eventDay: { date: "2027-06-06", timezone: "Asia/Taipei" } };
    expect(holdsStaleGuests(s, day + 3 * 24 * 3600 * 1000)).toBe(false);
    expect(holdsStaleGuests(s, day + 3 * 24 * 3600 * 1000 + 1)).toBe(true);
    expect(holdsStaleGuests({ ...s, guests: [] }, day + 9e9)).toBe(false);
    expect(holdsStaleGuests({ eventDay: s.eventDay }, day + 9e9)).toBe(false);
    const m = new Map<string, string>([
      ["snap:a", JSON.stringify({ eventStatus: "Draft", serverTime: "2027-06-05T00:00:00Z", ...s })],
      ["snap:b", JSON.stringify({ eventStatus: "Draft", serverTime: "2027-06-05T00:00:00Z", eventDay: s.eventDay })],
    ]);
    const store = {
      get length() { return m.size; },
      key: (i: number) => [...m.keys()][i] ?? null,
      getItem: (k: string) => m.get(k) ?? null,
      removeItem: (k: string) => void m.delete(k),
    } as unknown as Storage;
    pruneStale(store, day + 4 * 24 * 3600 * 1000);
    expect([...m.keys()]).toEqual(["snap:b"]);
  });
});

describe("the colour theme (screen-design.md section 2)", () => {
  const mem = (init: Record<string, string> = {}) => {
    const m = new Map(Object.entries(init));
    return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) } as unknown as Storage;
  };
  it("dark unless this device chose light; storage that throws or is missing means dark", () => {
    expect(readTheme(mem())).toBe("dark");
    expect(readTheme(mem({ theme: "light" }))).toBe("light");
    expect(readTheme(mem({ theme: "neon" }))).toBe("dark");
    expect(readTheme(null)).toBe("dark");
    const broken = { getItem: () => { throw new Error("blocked"); }, setItem: () => { throw new Error("blocked"); } } as unknown as Storage;
    expect(readTheme(broken)).toBe("dark");
    expect(() => saveTheme(broken, "light")).not.toThrow();
  });
  it("the choice is remembered on this device", () => {
    const s = mem();
    saveTheme(s, "light");
    expect(readTheme(s)).toBe("light");
    saveTheme(s, "dark");
    expect(readTheme(s)).toBe("dark");
  });
});
