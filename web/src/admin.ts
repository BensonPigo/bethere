// The admin page's requests (US-O-9, US-O-10, US-O-11; api.md sections 3
// and 10): the admin link's token in the Authorization header, nothing
// kept anywhere.
import { sendJSON } from "./api";

// ADMIN_TIMEOUT_MS is the server's 10 s bound on a write, as on /new.
const ADMIN_TIMEOUT_MS = 10_000;

export type AdminInvite = { id: number; expiresAt: string };
// organization is the company's name, null for an event of none (US-O-11.3).
export type AdminEvent = { id: number; name: string; date: string; status: string; createdAt: string; organization?: string | null };
export type AdminOrganization = { id: number; name: string; members: number; events: number; createdAt: string };

// "denied" is every refusal of the link: none, wrong, replaced (US-O-9.1).
export type OverviewOutcome =
  | { kind: "ok"; invites: AdminInvite[]; events: AdminEvent[]; organizations?: AdminOrganization[] }
  | { kind: "denied" }
  | { kind: "maintenance" }
  | { kind: "failure" };

export type InviteOutcome =
  | { kind: "ok"; id: number; code: string; expiresAt: string }
  | { kind: "tooMany" }
  | { kind: "denied" }
  | { kind: "maintenance" }
  | { kind: "failure" };

export type OrganizationOutcome =
  | { kind: "ok"; id: number }
  | { kind: "invalidName" }
  | { kind: "invalidEmail" }
  | { kind: "taken" }
  | { kind: "tooManyOrgs" }
  | { kind: "denied" }
  | { kind: "maintenance" }
  | { kind: "failure" };

type Body = Record<string, unknown>;

const isObject = (v: unknown): v is Body => typeof v === "object" && v !== null;

const isInvite = (v: unknown): v is AdminInvite => isObject(v) && typeof v.id === "number" && typeof v.expiresAt === "string";

const isEvent = (v: unknown): v is AdminEvent =>
  isObject(v) &&
  typeof v.id === "number" &&
  ["name", "date", "status", "createdAt"].every((k) => typeof v[k] === "string") &&
  (v.organization === undefined || v.organization === null || typeof v.organization === "string");

const isOrganization = (v: unknown): v is AdminOrganization =>
  isObject(v) &&
  ["id", "members", "events"].every((k) => typeof v[k] === "number") &&
  ["name", "createdAt"].every((k) => typeof v[k] === "string");

function refused(status: number, b: Body): { kind: "denied" } | { kind: "maintenance" } | null {
  if (status === 404 && b.error === "not_found") return { kind: "denied" };
  if (status === 503 && b.error === "maintenance") return { kind: "maintenance" };
  return null;
}

export function overviewOutcome(status: number, body: unknown): OverviewOutcome {
  const b = (body ?? {}) as Body;
  const orgs = b.organizations;
  if (
    status === 200 &&
    b.result === "ok" &&
    Array.isArray(b.invites) &&
    b.invites.every(isInvite) &&
    Array.isArray(b.events) &&
    b.events.every(isEvent) &&
    (orgs === undefined || (Array.isArray(orgs) && orgs.every(isOrganization)))
  ) {
    return { kind: "ok", invites: b.invites, events: b.events, organizations: orgs as AdminOrganization[] | undefined };
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

const ORG_ERRORS: Record<string, OrganizationOutcome["kind"]> = {
  invalid_organization: "invalidName",
  invalid_email: "invalidEmail",
  email_taken: "taken",
  too_many_organizations: "tooManyOrgs",
};

// organizationOutcome reads the answer to a new company (US-O-11).
export function organizationOutcome(status: number, body: unknown): OrganizationOutcome {
  const b = (body ?? {}) as Body;
  if (status === 200 && b.result === "ok" && typeof b.id === "number") return { kind: "ok", id: b.id };
  const kind = status === 422 && typeof b.error === "string" ? ORG_ERRORS[b.error] : undefined;
  if (kind) return { kind } as OrganizationOutcome;
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

export async function postOrganization(token: string, name: string, email: string): Promise<OrganizationOutcome> {
  const r = await sendJSON("POST", "/api/admin/organizations", { name, email }, ADMIN_TIMEOUT_MS, token);
  return r ? organizationOutcome(r.status, r.body) : { kind: "failure" };
}
