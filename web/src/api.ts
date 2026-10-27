// Hand-written types and HTTP client for docs/design/api.md (tech-stack.md 2.2「型別」).

export type EventStatus = "Draft" | "Live";
export type CueStatus = "Pending" | "InProgress" | "Completed" | "Skipped";

// The screen a link opens, set per role (ADR-0025).
export type View = "Console" | "HandsFree" | "Glance" | "Dashboard";

// checksIn: whether the role checks in (M2c); absent in a snapshot saved by an older release.
export type RoleInfo = { type: string; name: string; view: View; checksIn?: boolean };

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
  // ackedAt: a venue lead's ack (api.md 9.3); absent in an older saved snapshot.
  roles: { roleId: string | number; name: string; roleNote: string | null; ackedAt?: string | null }[];
};

// The four jobs (api.md 9.4–9.6).
export type TaskState = "open" | "claimed" | "done";
export type Task = {
  id: string | number;
  title: string;
  createdBy: string | null;
  createdAt: string;
  claimedBy: string | null;
  state: TaskState;
};
export type Guest = { id: string | number; name: string; table: string; note: string | null; arrived: boolean };
export type CheckItem = { id: string | number; title: string; done: boolean; doneBy: string | null };

export type MasterRole = {
  id: string | number;
  name: string;
  type: string;
  view?: View; // M2c
  checksIn?: boolean; // M2c
  checkedIn: boolean;
  lastSeenAt: string | null; // to the minute (api.md 5.2)
  hasLink: boolean;
};

export type MasterSnapshot = SnapshotBase & {
  event: { name: string; date: string; timezone: string };
  cues: MasterCue[];
  activeCueId: string | number | null;
  undo: { available: boolean; description: string | null };
  roles: MasterRole[];
  conflicts: Conflict[];
  overrunMin: number; // minutes past the next pending cue's start (data-model.md 4.3)
  outOfOrder: { cueId: string | number; prevCueId: string | number }[]; // US-M-8.6
  // Absent in a snapshot saved by an older release.
  tasks?: Task[];
  guests?: Guest[];
  checklist?: CheckItem[];
  maintenance?: boolean; // only the Master gets through (US-O-2.5); absent in older snapshots
};

// The role types that read their own cues, check in and can be assigned
// cues (domain.RoleType.HasOwnCues).
export const CUE_ROLE_TYPES = ["Stylist", "Media", "StageAV", "VenueLead", "Runner", "Reception", "Cashier"];

// What a new role does besides reading its cues (api.md 9.2).
export type Job = "general" | "VenueLead" | "Runner" | "Reception" | "Cashier";

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
  ackedAt?: string | null; // own ack, venue leads only (api.md 9.3)
};

// A shift that moved at least one of the caller's own cues (screen-design.md 4.4).
export type ShiftNotice = {
  kind: "shift";
  at: string;
  deltaMin: number;
  changes: { cueId: string | number; fromStart: string; toStart: string }[];
};

// A cue newly assigned to the caller (ADR-0021).
export type AssignNotice = {
  kind: "assign";
  at: string;
  cueId: string | number;
  title: string;
  effectiveStart: string;
};

export type Notice = ShiftNotice | AssignNotice;

export type RoleSnapshot = SnapshotBase & {
  cues: RoleCue[];
  notice: Notice | null;
  // Absent in a snapshot saved by an older release.
  myCheckIn?: boolean;
  currentTitle?: string | null; // the cue in progress, whoever it is assigned to (US-X-6.4)
  // api.md 9.1, 9.4–9.6: the delay for all; each job's list only for that job, null otherwise.
  delayMin?: number;
  tasks?: Task[] | null;
  guests?: Guest[] | null;
  checklist?: CheckItem[] | null;
};

// The hosts' dashboard: no cues at all (api.md 5.4).
export type HostSnapshot = SnapshotBase & { delayMin: number; currentTitle: string | null };

export type Snapshot = MasterSnapshot | RoleSnapshot | HostSnapshot;

export function isMasterSnapshot(s: Snapshot): s is MasterSnapshot {
  return s.role.type === "Master";
}

// inMaintenance: the Master's banner while the other roles are kept out (US-M-9.1).
export function inMaintenance(s: Snapshot): boolean {
  return isMasterSnapshot(s) && s.maintenance === true;
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

export type Assignment = { roleId: string | number; roleNote: string | null };

export type NewCue = {
  afterCueId: string | number | null; // null: first, in draft only (M2c)
  start: string; // RFC 3339
  durationMin: number;
  title: string;
  notePublic: string | null;
  roles: Assignment[];
};

// The whole editable part of a draft cue (US-M-10.2).
export type CueUpdate = {
  cueId: string | number;
  start: string; // RFC 3339
  durationMin: number;
  isFixed: boolean;
  title: string;
  notePublic: string | null;
};

// A role the Master adds: a name, a screen and whether it checks in (US-M-11.1).
export type NewRole = { name: string; view: "HandsFree" | "Glance" | "Dashboard"; checksIn: boolean; job?: Job };

export type WriteRequest =
  | { path: "/api/cues/advance" | "/api/cues/undo" }
  | { path: "/api/cues/shift"; shift: ShiftRequest }
  | { path: "/api/cues/skip"; cueId: string | number }
  | { path: "/api/cues/insert"; cue: NewCue }
  | { path: "/api/cues/roles"; cueId: string | number; roles: Assignment[] }
  | { path: "/api/cues/edit"; cueId: string | number; title: string; notePublic: string | null }
  // No version: check-ins and links are not cues (ADR-0023).
  | { path: "/api/checkin"; checkedIn: boolean }
  | { path: "/api/roles/checkin"; roleId: string | number; checkedIn: boolean }
  | { path: "/api/roles/reissue"; roleId: string | number; origin?: string }
  | { path: "/api/roles/revoke"; roleId: string | number }
  // The preparation (M2c).
  | { path: "/api/cues/update"; cue: CueUpdate }
  | { path: "/api/cues/move"; cueId: string | number; afterCueId: string | number | null }
  | { path: "/api/cues/delete"; cueId: string | number }
  | { path: "/api/rundown/import"; text: string; newRoles: NewRole[] }
  | { path: "/api/roles/add"; role: NewRole; origin: string }
  | { path: "/api/roles/rename"; roleId: string | number; name: string }
  | { path: "/api/roles/delete"; roleId: string | number }
  // The four jobs: no version (api.md section 9).
  | { path: "/api/cues/ack"; cueId: string | number; acked: boolean }
  | { path: "/api/tasks/add"; title: string }
  | { path: "/api/tasks/set"; taskId: string | number; state: TaskState }
  | { path: "/api/tasks/delete"; taskId: string | number }
  | { path: "/api/guests/import"; text: string }
  | { path: "/api/guests/arrive"; guestId: string | number; arrived: boolean }
  | { path: "/api/checklist/items"; text: string }
  | { path: "/api/checklist/check"; itemId: string | number; done: boolean };

// writeBody is the JSON body of a write (api.md section 3).
export function writeBody(w: WriteRequest, version: number): Record<string, unknown> {
  switch (w.path) {
    case "/api/cues/advance":
    case "/api/cues/undo":
      return { version };
    case "/api/cues/shift":
      return { version, ...w.shift };
    case "/api/cues/skip":
      return { version, cueId: w.cueId };
    case "/api/cues/insert":
      return { version, ...w.cue };
    case "/api/cues/roles":
      return { version, cueId: w.cueId, roles: w.roles };
    case "/api/cues/edit":
      return { version, cueId: w.cueId, title: w.title, notePublic: w.notePublic };
    case "/api/checkin":
      return { checkedIn: w.checkedIn };
    case "/api/roles/checkin":
      return { roleId: w.roleId, checkedIn: w.checkedIn };
    case "/api/roles/reissue":
      return w.origin ? { roleId: w.roleId, origin: w.origin } : { roleId: w.roleId };
    case "/api/roles/revoke":
      return { roleId: w.roleId };
    case "/api/cues/update":
      return { version, ...w.cue };
    case "/api/cues/move":
      return { version, cueId: w.cueId, afterCueId: w.afterCueId };
    case "/api/cues/delete":
      return { version, cueId: w.cueId };
    case "/api/rundown/import":
      return { version, text: w.text, newRoles: w.newRoles };
    case "/api/roles/add":
      return { ...w.role, origin: w.origin };
    case "/api/roles/rename":
      return { roleId: w.roleId, name: w.name };
    case "/api/roles/delete":
      return { version, roleId: w.roleId };
    case "/api/cues/ack":
      return { cueId: w.cueId, acked: w.acked };
    case "/api/tasks/add":
      return { title: w.title };
    case "/api/tasks/set":
      return { taskId: w.taskId, state: w.state };
    case "/api/tasks/delete":
      return { taskId: w.taskId };
    case "/api/guests/import":
    case "/api/checklist/items":
      return { text: w.text };
    case "/api/guests/arrive":
      return { guestId: w.guestId, arrived: w.arrived };
    case "/api/checklist/check":
      return { itemId: w.itemId, done: w.done };
  }
}

// What a response means to the client (api.md section 2).
export type Outcome =
  | { kind: "invalid" }
  | { kind: "ended" }
  // token, qr, roleId: only in a reissue's or a new role's response, never
  // stored (US-M-7.1, US-M-11.2, US-M-13.2).
  | { kind: "ok"; snapshot: Snapshot; token?: string; qr?: string; roleId?: string | number; etag?: string }
  // 304 to a conditional GET: keep the snapshot, only resync the clock (api.md 5.7).
  | { kind: "notModified"; serverTime: string }
  // A shift, import or role preview; never a snapshot of the caller's own,
  // so it is never stored (api.md section 3). The caller knows which.
  | { kind: "preview"; preview: unknown }
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
    if (json.result === "ok" && isObject(json.preview)) return { kind: "preview", preview: json.preview };
    if (json.result === "ok") {
      const snapshot = asSnapshot(json.snapshot);
      if (!snapshot) return { kind: "failure" };
      if (typeof json.token !== "string") return { kind: "ok", snapshot };
      const o: Outcome = { kind: "ok", snapshot, token: json.token };
      if (typeof json.qr === "string" && json.qr.startsWith("data:image/png;base64,")) o.qr = json.qr;
      if (typeof json.roleId === "number" || typeof json.roleId === "string") o.roleId = json.roleId;
      return o;
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

// classifyNotModified is a 304: only with the server time header, else offline-like.
export function classifyNotModified(serverTime: string | null): Outcome {
  return serverTime && Number.isFinite(Date.parse(serverTime)) ? { kind: "notModified", serverTime } : { kind: "failure" };
}

async function request(
  method: "GET" | "POST",
  path: string,
  token: string,
  body?: unknown,
  ifNoneMatch?: string,
): Promise<Outcome> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), POLL_TIMEOUT_MS);
  try {
    const headers: Record<string, string> = { Authorization: `Bearer ${token}` };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (ifNoneMatch) headers["If-None-Match"] = ifNoneMatch;
    const res = await fetch(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      cache: "no-store",
      signal: ctrl.signal,
    });
    if (res.status === 304) return classifyNotModified(res.headers.get("X-Server-Time"));
    let parsed: unknown;
    try {
      parsed = await res.json();
    } catch {
      parsed = undefined;
    }
    const o = classifyResponse(res.status, res.headers.get("Content-Type"), parsed);
    const etag = res.headers.get("ETag");
    return o.kind === "ok" && etag ? { ...o, etag } : o;
  } catch {
    return { kind: "failure" };
  } finally {
    clearTimeout(timer);
  }
}

// The ETag is kept in memory only, per token (api.md 5.7).
export const fetchSnapshot = (token: string, etag?: string) => request("GET", "/api/snapshot", token, undefined, etag);

export const postWrite = (token: string, w: WriteRequest, version: number) =>
  request("POST", w.path, token, writeBody(w, version));

export const postShiftPreview = (token: string, shift: ShiftRequest) =>
  request("POST", "/api/cues/shift/preview", token, shift);

// The pasted import checked without writing (US-M-12.2).
export type ImportRow = {
  line: number;
  seq: number;
  title: string;
  start: string; // HH:MM
  durationMin: number;
  isFixed: boolean;
  roles: string[];
  roleNote: string | null;
  notePublic: string | null;
  errors: string[];
};

export type ImportPreview = {
  fileError: string | null;
  rows: ImportRow[];
  missingRoles: string[];
  tooManyRoles: boolean;
  replaces: number;
};

export const postImportPreview = (token: string, text: string) =>
  request("POST", "/api/rundown/import/preview", token, { text });

// Another role's own snapshot, shown to the Master only (US-M-14).
export const postRolePreview = (token: string, roleId: string | number) =>
  request("POST", "/api/roles/preview", token, { roleId });
