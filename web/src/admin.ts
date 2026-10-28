// The admin page's requests (US-O-9, US-O-10; api.md section 3): the admin
// link's token in the Authorization header, nothing kept anywhere.
import { sendJSON } from "./api";

// ADMIN_TIMEOUT_MS is the server's 10 s bound on a write, as on /new.
const ADMIN_TIMEOUT_MS = 10_000;

export type AdminInvite = { id: number; expiresAt: string };
export type AdminEvent = { id: number; name: string; date: string; status: string; createdAt: string };

// "denied" is every refusal of the link: none, wrong, replaced (US-O-9.1).
export type OverviewOutcome =
  | { kind: "ok"; invites: AdminInvite[]; events: AdminEvent[] }
  | { kind: "denied" }
  | { kind: "maintenance" }
  | { kind: "failure" };

export type InviteOutcome =
  | { kind: "ok"; id: number; code: string; expiresAt: string }
  | { kind: "tooMany" }
  | { kind: "denied" }
  | { kind: "maintenance" }
  | { kind: "failure" };

type Body = Record<string, unknown>;

const isInvite = (v: unknown): v is AdminInvite =>
  typeof v === "object" && v !== null && typeof (v as Body).id === "number" && typeof (v as Body).expiresAt === "string";

const isEvent = (v: unknown): v is AdminEvent =>
  typeof v === "object" &&
  v !== null &&
  typeof (v as Body).id === "number" &&
  ["name", "date", "status", "createdAt"].every((k) => typeof (v as Body)[k] === "string");

function refused(status: number, b: Body): { kind: "denied" } | { kind: "maintenance" } | null {
  if (status === 404 && b.error === "not_found") return { kind: "denied" };
  if (status === 503 && b.error === "maintenance") return { kind: "maintenance" };
  return null;
}

export function overviewOutcome(status: number, body: unknown): OverviewOutcome {
  const b = (body ?? {}) as Body;
  if (
    status === 200 &&
    b.result === "ok" &&
    Array.isArray(b.invites) &&
    b.invites.every(isInvite) &&
    Array.isArray(b.events) &&
    b.events.every(isEvent)
  ) {
    return { kind: "ok", invites: b.invites, events: b.events };
  }
  return refused(status, b) ?? { kind: "failure" };
}

export function inviteOutcome(status: number, body: unknown): InviteOutcome {
  const b = (body ?? {}) as Body;
  if (status === 200 && b.result === "ok" && typeof b.id === "number" && typeof b.code === "string" && b.code !== "" &&
    typeof b.expiresAt === "string") {
    return { kind: "ok", id: b.id, code: b.code, expiresAt: b.expiresAt };
  }
  if (status === 422 && b.error === "too_many_invites") return { kind: "tooMany" };
  return refused(status, b) ?? { kind: "failure" };
}

export async function getOverview(token: string): Promise<OverviewOutcome> {
  const r = await sendJSON("GET", "/api/admin/overview", undefined, ADMIN_TIMEOUT_MS, token);
  return r ? overviewOutcome(r.status, r.body) : { kind: "failure" };
}

export async function postInvite(token: string): Promise<InviteOutcome> {
  const r = await sendJSON("POST", "/api/admin/invites", {}, ADMIN_TIMEOUT_MS, token);
  return r ? inviteOutcome(r.status, r.body) : { kind: "failure" };
}
