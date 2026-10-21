// Hand-written types and HTTP client for docs/design/api.md (T17).

export type EventStatus = "Draft" | "Live";
export type CueStatus = "Pending" | "InProgress" | "Completed" | "Skipped";

export type RoleInfo = { type: string; name: string };

type SnapshotBase = {
  eventVersion: number;
  serverTime: string;
  eventStatus: EventStatus;
  role: RoleInfo;
};

export type MasterCue = {
  id: string | number;
  seq: number;
  title: string;
  scheduledStart: string;
  effectiveStart: string;
  durationMin: number;
  isFixed: boolean;
  offsetMin: number;
  status: CueStatus;
  actualStart: string | null;
  actualEnd: string | null;
  notePublic: string | null;
  roles: { roleId: string | number; name: string; roleNote: string | null }[];
};

export type MasterSnapshot = SnapshotBase & {
  event: { name: string; date: string; timezone: string };
  cues: MasterCue[];
  activeCueId: string | number | null;
  undo: { available: boolean; description: string | null };
  roles: { id: string | number; name: string; type: string }[];
};

export type RoleCue = {
  id: string | number;
  seq: number;
  title: string;
  effectiveStart: string;
  durationMin: number;
  isFixed: boolean;
  offsetMin: number;
  status: CueStatus;
  notePublic: string | null;
  roleNote: string | null;
};

export type RoleSnapshot = SnapshotBase & { cues: RoleCue[] };

export type Snapshot = MasterSnapshot | RoleSnapshot;

export function isMasterSnapshot(s: Snapshot): s is MasterSnapshot {
  return s.role.type === "Master";
}

export type WritePath = "/api/event/start" | "/api/cues/advance" | "/api/cues/undo";

// What a response means to the client (api.md section 2).
export type Outcome =
  | { kind: "invalid" }
  | { kind: "ended" }
  | { kind: "ok"; snapshot: Snapshot }
  | { kind: "conflict"; snapshot: Snapshot | null }
  | { kind: "rejected"; error: string } // 422 domain rule
  | { kind: "failure" }; // anything else: offline-like, never wipes data

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function asSnapshot(v: unknown): Snapshot | null {
  if (!isObject(v)) return null;
  if (typeof v.eventVersion !== "number" || typeof v.serverTime !== "string") return null;
  if (!isObject(v.role) || typeof v.role.type !== "string") return null;
  if (!Array.isArray(v.cues)) return null;
  return v as unknown as Snapshot;
}

export function isJsonContentType(contentType: string | null): boolean {
  if (!contentType) return false;
  const mt = contentType.split(";")[0].trim().toLowerCase();
  return mt === "application/json" || mt.endsWith("+json");
}

// `body` is the parsed JSON, or undefined when it could not be parsed.
export function classifyResponse(status: number, contentType: string | null, body: unknown): Outcome {
  const json = isJsonContentType(contentType) && isObject(body) ? body : null;
  if (json === null) return { kind: "failure" };
  if (status === 200) {
    if (json.result === "invalid") return { kind: "invalid" };
    if (json.result === "ended") return { kind: "ended" };
    if (json.result === "ok") {
      const snapshot = asSnapshot(json.snapshot);
      return snapshot ? { kind: "ok", snapshot } : { kind: "failure" };
    }
    return { kind: "failure" };
  }
  if (status === 409 && json.error === "version_conflict") {
    return { kind: "conflict", snapshot: asSnapshot(json.snapshot) };
  }
  if (status === 422 && typeof json.error === "string") return { kind: "rejected", error: json.error };
  return { kind: "failure" };
}

// A15: local data is wiped only for HTTP 200 + JSON + parseable + result invalid/ended.
// Everything else (401/403/409/500, non-JSON, network failure) keeps it.
export function shouldWipeLocalData(outcome: Outcome): boolean {
  return outcome.kind === "invalid" || outcome.kind === "ended";
}

export const POLL_TIMEOUT_MS = 8000;

async function request(
  method: "GET" | "POST",
  path: string,
  token: string,
  body?: unknown,
): Promise<Outcome> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), POLL_TIMEOUT_MS);
  try {
    const headers: Record<string, string> = { Authorization: `Bearer ${token}` };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const res = await fetch(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      cache: "no-store",
      signal: ctrl.signal,
    });
    let parsed: unknown;
    try {
      parsed = await res.json();
    } catch {
      parsed = undefined;
    }
    return classifyResponse(res.status, res.headers.get("Content-Type"), parsed);
  } catch {
    return { kind: "failure" };
  } finally {
    clearTimeout(timer);
  }
}

export const fetchSnapshot = (token: string) => request("GET", "/api/snapshot", token);

export const postWrite = (token: string, path: WritePath, version: number) =>
  request("POST", path, token, { version });
