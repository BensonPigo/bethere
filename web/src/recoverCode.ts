// The recovery code's request (US-O-3; api.md section 3): the code in the
// body only, never a token, nothing kept anywhere.
import { POLL_TIMEOUT_MS } from "./api";

export type RecoverOutcome = { kind: "ok"; token: string } | { kind: "wrong" } | { kind: "ended" } | { kind: "failure" };

// recoverOutcome reads the answer; anything not understood is a failure,
// so a server error never shows as "wrong code" (US-O-3.4).
export function recoverOutcome(status: number, body: unknown): RecoverOutcome {
  const b = (body ?? {}) as { result?: unknown; token?: unknown; error?: unknown };
  if (status === 200 && b.result === "ok" && typeof b.token === "string" && b.token.length === 22) {
    return { kind: "ok", token: b.token };
  }
  if (status === 422 && b.error === "wrong_recovery_code") return { kind: "wrong" };
  if (status === 422 && b.error === "event_ended") return { kind: "ended" };
  return { kind: "failure" };
}

export async function postRecover(code: string): Promise<RecoverOutcome> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), POLL_TIMEOUT_MS);
  try {
    const res = await fetch("/api/recover/master", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ recoveryCode: code }),
      cache: "no-store",
      signal: ctrl.signal,
    });
    let parsed: unknown;
    try {
      parsed = await res.json();
    } catch {
      parsed = undefined;
    }
    return recoverOutcome(res.status, parsed);
  } catch {
    return { kind: "failure" };
  } finally {
    clearTimeout(timer);
  }
}
