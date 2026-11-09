import { describe, expect, it } from "vitest";
import { companyOutcome, consoleOutcome, createdOutcome, loginOutcome, rememberConsole } from "./company";
import { organizationOutcome, overviewOutcome } from "./admin";

const TOKEN = "abcdefghijklmnopqrstuv"; // 22 characters
const SNAP_A = "snap:" + "a".repeat(64);
const SNAP_B = "snap:" + "b".repeat(64);
const SNAP_M = "snap:" + "c".repeat(64); // a Master link's snapshot on the same device

function memoryStorage(): Storage & { map: Map<string, string> } {
  const m = new Map<string, string>();
  return {
    map: m,
    get length() { return m.size; },
    clear: () => m.clear(),
    getItem: (k) => m.get(k) ?? null,
    key: (i) => [...m.keys()][i] ?? null,
    removeItem: (k) => void m.delete(k),
    setItem: (k, v) => void m.set(k, v),
  };
}

const page = {
  result: "ok",
  organization: { name: "甲" },
  me: 1,
  members: [{ id: 1, email: "a@x.tw" }],
  events: [{ id: 7, name: "甲婚宴", date: "2027-06-20", status: "Draft" }],
};

describe("US-CO-1 login answers", () => {
  it("reads a login link, a stranger's email and everything else", () => {
    expect(loginOutcome(200, { result: "ok", loginToken: TOKEN })).toEqual({ kind: "ok", token: TOKEN });
    expect(loginOutcome(200, { result: "ok", loginToken: "short" }).kind).toBe("unavailable");
    expect(loginOutcome(422, { error: "not_member" }).kind).toBe("notMember");
    expect(loginOutcome(422, { error: "other" }).kind).toBe("unavailable");
    expect(loginOutcome(503, { error: "maintenance" }).kind).toBe("unavailable");
    expect(loginOutcome(500, undefined).kind).toBe("unavailable");
  });
});

describe("US-CO-1.5 the company page's answers", () => {
  it("reads the page", () => {
    expect(companyOutcome(200, page)).toEqual({
      kind: "ok",
      company: { name: "甲", me: 1, members: page.members, events: page.events },
    });
  });
  it("refuses a page with a missing or wrong part", () => {
    for (const bad of [
      { ...page, organization: null },
      { ...page, organization: { name: 1 } },
      { ...page, me: "1" },
      { ...page, members: [{ id: "1", email: "a@x.tw" }] },
      { ...page, members: null },
      { ...page, events: [{ id: 7, name: "x", date: "2027-06-20" }] },
      { ...page, events: {} },
      { ...page, result: "invalid" },
    ]) {
      expect(companyOutcome(200, bad).kind).toBe("unavailable");
    }
  });
  it("tells a refused link from a server away, and keeps a 422's code", () => {
    expect(companyOutcome(404, { error: "not_found" }).kind).toBe("denied");
    expect(companyOutcome(404, { error: "other" }).kind).toBe("unavailable");
    expect(companyOutcome(503, { error: "maintenance" }).kind).toBe("unavailable");
    expect(companyOutcome(422, { error: "email_taken" })).toEqual({ kind: "error", code: "email_taken" });
    expect(companyOutcome(422, {}).kind).toBe("unavailable");
    expect(companyOutcome(200, undefined).kind).toBe("unavailable");
  });
});

describe("US-CO-3.3 a new event's answer", () => {
  it("reads the Master link and code once", () => {
    const ok = { result: "ok", eventId: 7, masterToken: TOKEN, recoveryCode: "ABCD" };
    expect(createdOutcome(200, ok)).toEqual({ kind: "ok", eventId: 7, masterToken: TOKEN, recoveryCode: "ABCD" });
    expect(createdOutcome(200, { ...ok, recoveryCode: "" }).kind).toBe("unavailable");
    expect(createdOutcome(200, { ...ok, eventId: "7" }).kind).toBe("unavailable");
    expect(createdOutcome(200, { ...ok, masterToken: "x" }).kind).toBe("unavailable");
    expect(createdOutcome(422, { error: "too_many_events" })).toEqual({ kind: "error", code: "too_many_events" });
    expect(createdOutcome(404, { error: "not_found" }).kind).toBe("denied");
  });
});

describe("US-CO-4.1 a console link's answer", () => {
  it("reads the console link", () => {
    expect(consoleOutcome(200, { result: "ok", consoleToken: TOKEN })).toEqual({ kind: "ok", token: TOKEN });
    expect(consoleOutcome(200, { result: "ok", consoleToken: 5 }).kind).toBe("unavailable");
    expect(consoleOutcome(404, { error: "not_found" }).kind).toBe("denied");
    expect(consoleOutcome(503, { error: "maintenance" }).kind).toBe("unavailable");
  });
});

describe("US-CO-4.7 console snapshots on a shared device", () => {
  it("deletes only the console snapshots the page recorded for that event", () => {
    const s = memoryStorage();
    s.setItem(SNAP_A, "{}");
    s.setItem(SNAP_M, "{}");
    rememberConsole(s, 7, SNAP_A);
    expect(s.getItem("console:7")).toBe(JSON.stringify([SNAP_A]));
    s.setItem(SNAP_B, "{}");
    rememberConsole(s, 7, SNAP_B);
    expect(s.getItem(SNAP_A)).toBeNull();
    expect(s.getItem(SNAP_B)).toBe("{}");
    expect(s.getItem(SNAP_M)).toBe("{}");
    expect(s.getItem("console:7")).toBe(JSON.stringify([SNAP_B]));
  });
  it("keeps the newest when it is opened again, and other events' lists", () => {
    const s = memoryStorage();
    s.setItem(SNAP_A, "{}");
    rememberConsole(s, 7, SNAP_A);
    rememberConsole(s, 7, SNAP_A);
    expect(s.getItem(SNAP_A)).toBe("{}");
    rememberConsole(s, 8, SNAP_B);
    expect(s.getItem("console:7")).toBe(JSON.stringify([SNAP_A]));
  });
  it("never deletes a key that is not a snapshot key, however the list was written", () => {
    const s = memoryStorage();
    for (const k of ["theme", "snap:short", SNAP_M + "x", "console:9"]) s.setItem(k, "keep");
    s.setItem("console:7", JSON.stringify(["theme", "snap:short", SNAP_M + "x", "console:9", 5]));
    rememberConsole(s, 7, SNAP_A);
    for (const k of ["theme", "snap:short", SNAP_M + "x", "console:9"]) expect(s.getItem(k)).toBe("keep");
    s.setItem("console:7", "not json");
    rememberConsole(s, 7, SNAP_B);
    expect(s.getItem("console:7")).toBe(JSON.stringify([SNAP_B]));
    s.setItem(SNAP_B, "{}"); // only an array is a list: nothing named in an object goes
    s.setItem("console:7", JSON.stringify({ a: SNAP_B }));
    rememberConsole(s, 7, SNAP_A);
    expect(s.getItem(SNAP_B)).toBe("{}");
    expect(s.getItem("console:7")).toBe(JSON.stringify([SNAP_A]));
  });
  it("runs without storage", () => {
    const broken = { getItem: () => { throw new Error("blocked"); } } as unknown as Storage;
    expect(() => rememberConsole(broken, 7, SNAP_A)).not.toThrow();
  });
});

describe("US-O-11 the admin page's companies", () => {
  const ev = { id: 1, name: "婚宴", date: "2027-06-06", status: "Draft", createdAt: "2027-05-01T00:00:00Z" };
  const org = { id: 2, name: "甲", members: 3, events: 4, createdAt: "2027-05-01T00:00:00Z" };
  it("reads companies and each event's company", () => {
    const body = { result: "ok", invites: [], events: [{ ...ev, organization: "甲" }, { ...ev, organization: null }], organizations: [org] };
    expect(overviewOutcome(200, body)).toEqual({ kind: "ok", invites: [], events: body.events, organizations: [org] });
    expect(overviewOutcome(200, { ...body, organizations: [{ ...org, members: "3" }] }).kind).toBe("failure");
    expect(overviewOutcome(200, { ...body, organizations: {} }).kind).toBe("failure");
    expect(overviewOutcome(200, { ...body, events: [{ ...ev, organization: 5 }] }).kind).toBe("failure");
  });
  it("reads a new company's answer", () => {
    expect(organizationOutcome(200, { result: "ok", id: 2 })).toEqual({ kind: "ok", id: 2 });
    expect(organizationOutcome(200, { result: "ok", id: "2" }).kind).toBe("failure");
    for (const [code, kind] of [
      ["invalid_organization", "invalidName"],
      ["invalid_email", "invalidEmail"],
      ["email_taken", "taken"],
      ["too_many_organizations", "tooManyOrgs"],
    ]) {
      expect(organizationOutcome(422, { error: code }).kind).toBe(kind);
    }
    expect(organizationOutcome(422, { error: "other" }).kind).toBe("failure");
    expect(organizationOutcome(404, { error: "not_found" }).kind).toBe("denied");
    expect(organizationOutcome(503, { error: "maintenance" }).kind).toBe("maintenance");
  });
});
