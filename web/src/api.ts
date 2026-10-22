// Hand-written types and HTTP client for docs/design/api.md (tech-stack.md 2.2「型別」).

export type EventStatus = "Draft" | "Live";
export type CueStatus = "Pending" | "InProgress" | "Completed" | "Skipped";

// The screen a link opens, set per role (ADR-0025).
export type View = "Console" | "HandsFree" | "Glance" | "Dashboard";

export type RoleInfo = { type: string; name: string; view: View };

type SnapshotBase = {
  eventVersion: number;
  serverTime: string;
  eventStatus: EventStatus;
  role: RoleInfo;
  // Event day and zone, so a draft can still count down on the day offline (ADR-0020).
  eventDay: { date: string; timezone: string };
};

export type Conflict = { cueId: string | number; fixedCueId: string | number; overrunMin: number };

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
  conflicts: Conflict[];
  overrunMin: number; // minutes past the next pending cue's start (data-model.md 4.3)
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

// A shift that moved at least one of the caller's own cues (screen-design.md 4.4).
export type ShiftNotice = {
  kind: "shift";
  at: string;
  deltaMin: number;
  changes: { cueId: string | number; fromStart: string; toStart: string }[];
};

export type RoleSnapshot = SnapshotBase & { cues: RoleCue[]; notice: ShiftNotice | null };

// The hosts' dashboard: no cues at all (api.md 5.4).
export type HostSnapshot = SnapshotBase & { delayMin: number; currentTitle: string | null };

export type Snapshot = MasterSnapshot | RoleSnapshot | HostSnapshot;

export function isMasterSnapshot(s: Snapshot): s is MasterSnapshot {
  return s.role.type === "Master";
}

export function isHostSnapshot(s: Snapshot): s is HostSnapshot {
  return s.role.type === "Host";
}

export type ShiftRequest = { fromCueId: string | number; deltaMin: number; excluded: (string | number)[] };

export type ShiftPreview = {
  changes: { cueId: string | number; fromStart: string; toStart: string }[];
  conflicts: Conflict[];
  tooEarly: { cueId: string | number; newStart: string } | null;
};

export type WriteRequest =
  | { path: "/api/cues/advance" | "/api/cues/undo" }
  | { path: "/api/cues/shift"; shift: ShiftRequest };

// What a response means to the client (api.md section 2).
export type Outcome =
  | { kind: "invalid" }
  | { kind: "ended" }
  | { kind: "ok"; snapshot: Snapshot }
  | { kind: "preview"; preview: ShiftPreview }
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
  // Every snapshot but the hosts' carries its cues.
  if (v.role.type !== "Host" && !Array.isArray(v.cues)) return null;
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
    if (json.result === "ok" && isObject(json.preview)) {
      return { kind: "preview", preview: json.preview as unknown as ShiftPreview };
    }
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

// api.md section 7: local data is wiped only for HTTP 200 + JSON + parseable + result invalid/ended.
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

export const postWrite = (token: string, w: WriteRequest, version: number) =>
  request("POST", w.path, token, w.path === "/api/cues/shift" ? { version, ...w.shift } : { version });

export const postShiftPreview = (token: string, shift: ShiftRequest) =>
  request("POST", "/api/cues/shift/preview", token, shift);
