// The company page's requests (api.md section 10; US-CO-1–4): the login
// link's token in the Authorization header only, never kept in storage.
// Every answer not understood is a failure, which the page shows as
// "maintenance or offline" and never as a reason to log out.
import { postNoToken, sendJSON } from "./api";

// COMPANY_TIMEOUT_MS is twice the server's 10 s bound on a write, as on /new.
const COMPANY_TIMEOUT_MS = 20_000;

export type Member = { id: number; email: string };
export type CompanyEvent = { id: number; name: string; date: string; status: string };
export type Company = { name: string; me: number; members: Member[]; events: CompanyEvent[] };

// "denied" is every refused login link (US-CO-1.5); "unavailable" is a 503
// maintenance or no answer at all (screen-design.md section 10).
export type Refusal = { kind: "denied" } | { kind: "unavailable" };

export type CompanyOutcome = { kind: "ok"; company: Company } | Refusal | { kind: "error"; code: string };
export type LoginOutcome = { kind: "ok"; token: string } | { kind: "notMember" } | { kind: "unavailable" };
export type CreatedOutcome = { kind: "ok"; eventId: number; masterToken: string; recoveryCode: string } | Refusal | { kind: "error"; code: string };
export type ConsoleOutcome = { kind: "ok"; token: string } | Refusal | { kind: "error"; code: string };

type Body = Record<string, unknown>;

const isObject = (v: unknown): v is Body => typeof v === "object" && v !== null;
const isToken = (v: unknown): v is string => typeof v === "string" && v.length === 22;
const isMember = (v: unknown): v is Member => isObject(v) && typeof v.id === "number" && typeof v.email === "string";
const isEvent = (v: unknown): v is CompanyEvent =>
  isObject(v) && typeof v.id === "number" && ["name", "date", "status"].every((k) => typeof v[k] === "string");

// refusal is the answer shared by every company endpoint, or null.
function refusal(status: number, b: Body): Refusal | null {
  if (status === 404 && b.error === "not_found") return { kind: "denied" };
  if (status === 503 && b.error === "maintenance") return { kind: "unavailable" };
  return null;
}

// failed is a refusal, a 422's code, or "unavailable" for anything else.
function failed(status: number, b: Body): Refusal | { kind: "error"; code: string } {
  const r = refusal(status, b);
  if (r) return r;
  if (status === 422 && typeof b.error === "string") return { kind: "error", code: b.error };
  return { kind: "unavailable" };
}

export function companyOutcome(status: number, body: unknown): CompanyOutcome {
  const b = (body ?? {}) as Body;
  const org = b.organization;
  if (
    status === 200 &&
    b.result === "ok" &&
    isObject(org) &&
    typeof org.name === "string" &&
    typeof b.me === "number" &&
    Array.isArray(b.members) &&
    b.members.every(isMember) &&
    Array.isArray(b.events) &&
    b.events.every(isEvent)
  ) {
    return { kind: "ok", company: { name: org.name, me: b.me, members: b.members, events: b.events } };
  }
  return failed(status, b);
}

export function loginOutcome(status: number, body: unknown): LoginOutcome {
  const b = (body ?? {}) as Body;
  if (status === 200 && b.result === "ok" && isToken(b.loginToken)) return { kind: "ok", token: b.loginToken };
  if (status === 422 && b.error === "not_member") return { kind: "notMember" };
  return { kind: "unavailable" };
}

export function createdOutcome(status: number, body: unknown): CreatedOutcome {
  const b = (body ?? {}) as Body;
  if (status === 200 && b.result === "ok" && typeof b.eventId === "number" && isToken(b.masterToken) &&
    typeof b.recoveryCode === "string" && b.recoveryCode !== "") {
    return { kind: "ok", eventId: b.eventId, masterToken: b.masterToken, recoveryCode: b.recoveryCode };
  }
  return failed(status, b);
}

export function consoleOutcome(status: number, body: unknown): ConsoleOutcome {
  const b = (body ?? {}) as Body;
  if (status === 200 && b.result === "ok" && isToken(b.consoleToken)) return { kind: "ok", token: b.consoleToken };
  return failed(status, b);
}

// The words for each 422 of api.md 10.3 the page can meet.
export const COMPANY_ERRORS: Record<string, string> = {
  invalid_email: "email 格式不正確",
  email_taken: "這個 email 已經是成員",
  too_many_members: "成員已達上限",
  cannot_remove_self: "不能移除自己",
  invalid_event: "活動名稱或日期不正確",
  too_many_events: "今天建立的活動已達上限，明天再建",
};

export async function postLogin(email: string): Promise<LoginOutcome> {
  const r = await postNoToken("/api/company/login", { email }, COMPANY_TIMEOUT_MS);
  return r ? loginOutcome(r.status, r.body) : { kind: "unavailable" };
}

async function send<T>(method: "GET" | "POST", path: string, body: unknown, token: string,
  read: (status: number, body: unknown) => T, none: T): Promise<T> {
  const r = await sendJSON(method, path, body, COMPANY_TIMEOUT_MS, token);
  return r ? read(r.status, r.body) : none;
}

const UNAVAILABLE = { kind: "unavailable" } as const;

export const getCompany = (token: string) => send("GET", "/api/company", undefined, token, companyOutcome, UNAVAILABLE);
export const postAddMember = (token: string, email: string) =>
  send("POST", "/api/company/members/add", { email }, token, companyOutcome, UNAVAILABLE);
export const postRemoveMember = (token: string, memberId: number) =>
  send("POST", "/api/company/members/remove", { memberId }, token, companyOutcome, UNAVAILABLE);
export const postCompanyEvent = (token: string, name: string, date: string) =>
  send("POST", "/api/company/events", { name, date }, token, createdOutcome, UNAVAILABLE);
export const postOpenConsole = (token: string, eventId: number) =>
  send("POST", "/api/company/events/open", { eventId }, token, consoleOutcome, UNAVAILABLE);

// postLogout reports whether the server ended the link (US-CO-1.4).
export async function postLogout(token: string): Promise<boolean> {
  const r = await sendJSON("POST", "/api/company/logout", {}, COMPANY_TIMEOUT_MS, token);
  return r !== null && r.status === 200 && isObject(r.body) && r.body.result === "ok";
}

// Console links' local snapshots (sync-and-offline.md section 6,
// US-CO-4.7): the company page lists, per event, the snapshot keys of the
// console links it opened, and deletes only those when it opens another.
const CONSOLE_PREFIX = "console:";
const SNAP_KEY = /^snap:[0-9a-f]{64}$/;

// rememberConsole records key as the event's newest console snapshot and
// deletes the others the page recorded for it; a snapshot it never
// recorded, such as a Master link's on the same device, is never touched.
export function rememberConsole(store: Storage, eventId: number, key: string): void {
  try {
    const listKey = CONSOLE_PREFIX + eventId;
    let old: unknown = [];
    try {
      old = JSON.parse(store.getItem(listKey) ?? "[]");
    } catch {
      old = [];
    }
    for (const k of Array.isArray(old) ? old : []) {
      if (typeof k === "string" && SNAP_KEY.test(k) && k !== key) store.removeItem(k);
    }
    store.setItem(listKey, JSON.stringify([key]));
  } catch {
    /* storage unavailable: nothing to clean */
  }
}
