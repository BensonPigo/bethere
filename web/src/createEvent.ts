// The invited event's request (US-M-17; api.md section 3): the invite
// code in the body only, never a token, nothing kept anywhere.
import { postNoToken } from "./api";

// CREATE_TIMEOUT_MS is twice the server's 10 s bound on a creation, so a
// creation that commits is rarely given up on before its answer (US-M-17.9).
const CREATE_TIMEOUT_MS = 20_000;

export type CreateOutcome =
  | { kind: "ok"; token: string; recoveryCode: string }
  | { kind: "wrong" }
  | { kind: "invalid" }
  | { kind: "maintenance" }
  | { kind: "failure" };

// createOutcome reads the answer; anything not understood is a failure,
// so a server error never shows as "wrong code" (US-M-17.6).
export function createOutcome(status: number, body: unknown): CreateOutcome {
  const b = (body ?? {}) as { result?: unknown; token?: unknown; recoveryCode?: unknown; error?: unknown };
  if (
    status === 200 &&
    b.result === "ok" &&
    typeof b.token === "string" &&
    b.token.length === 22 &&
    typeof b.recoveryCode === "string" &&
    b.recoveryCode !== ""
  ) {
    return { kind: "ok", token: b.token, recoveryCode: b.recoveryCode };
  }
  if (status === 422 && b.error === "wrong_invite_code") return { kind: "wrong" };
  if (status === 422 && b.error === "invalid_event") return { kind: "invalid" };
  if (status === 503 && b.error === "maintenance") return { kind: "maintenance" };
  return { kind: "failure" };
}

// dateRange is the dates /new accepts (US-M-17.7): today in Taipei through
// two years ahead, as YYYY-MM-DD; the server checks again.
export function dateRange(now: Date): { min: string; max: string } {
  const today = new Intl.DateTimeFormat("en-CA", { timeZone: "Asia/Taipei" }).format(now);
  const [y, m, d] = today.split("-").map(Number);
  const max = new Date(Date.UTC(y + 2, m - 1, d)).toISOString().slice(0, 10);
  return { min: today, max };
}

export async function postCreateEvent(inviteCode: string, name: string, date: string): Promise<CreateOutcome> {
  const r = await postNoToken("/api/events", { inviteCode, name, date }, CREATE_TIMEOUT_MS);
  return r ? createOutcome(r.status, r.body) : { kind: "failure" };
}
