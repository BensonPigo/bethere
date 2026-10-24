// Where the end-to-end routes run (docs/process/e2e-test-routes.md):
//   E2E_TARGET=local (default): a server built from this checkout, on a fresh
//     database in a temporary directory.
//   E2E_TARGET=vm: the test service on the VM (bethere-e2e, its own
//     database), reached at its public URL; seeding and stopping it go
//     through `gcloud compute ssh` with the operator's own login.
// The real service and database on the VM are never touched.
import { execFileSync, spawn } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));

export const ROOT = resolve(HERE, "../..");
export const TZ = "Asia/Taipei";
export const ROLES = ["總召", "新秘", "攝影", "音控", "新人", "領班", "機動組", "接待", "禮金"] as const;
export type RoleName = (typeof ROLES)[number];
export type Links = Record<RoleName, string>; // tokens of one test event

const STATE = join(tmpdir(), "bethere-e2e-state.json");
const LOCAL_PORT = 18090;

type State = { baseURL: string; events: Links[]; local?: { dir: string; bin: string; pid: number } };

export const target = (process.env.E2E_TARGET ?? "local") as "local" | "vm";

export function saveState(s: State) {
  writeFileSync(STATE, JSON.stringify(s));
}

export function loadState(): State {
  return JSON.parse(readFileSync(STATE, "utf8")) as State;
}

// ---------------------------------------------------------------- the rundown

function minutesNow(now: Date): number {
  const [h, m] = new Intl.DateTimeFormat("en-GB", { timeZone: TZ, hour: "2-digit", minute: "2-digit", hourCycle: "h23" })
    .format(now)
    .split(":")
    .map(Number);
  return h * 60 + m;
}

const clock = (min: number) => `${String(Math.floor(min / 60)).padStart(2, "0")}:${String(min % 60).padStart(2, "0")}`;

export function today(now = new Date()): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: TZ, year: "numeric", month: "2-digit", day: "2-digit" }).format(now);
}

// tomorrow is the date of the events whose day has not begun: the
// preparation route replaces the rundown and deletes a role, which are
// locked from the event day on (ADR-0032).
export function tomorrow(now = new Date()): string {
  return today(new Date(now.getTime() + 24 * 3600 * 1000));
}

// The test rundown: the first cue started 10 minutes ago, the rest ahead,
// 95 minutes in all. Late in the evening the cues after now are moved
// closer together so they all stay on today; the next cue is always at
// least a few minutes ahead, so nothing is overrun when a route starts.
export function rundownCSV(now = new Date()): string {
  const n = minutesNow(now);
  const room = 24 * 60 - 1 - n;
  if (room < 10) throw new Error("the test rundown must fall on today: run the end-to-end routes before 23:50 Taipei time");
  const scale = Math.min(1, room / 80);
  const at = (offset: number) => clock(offset < 0 ? Math.max(0, n + offset) : n + Math.max(1, Math.round(offset * scale)));
  return [
    "seq,title,start,duration_min,fixed,roles,role_note,note",
    `1,新娘化妝,${at(-10)},30,,新秘,新娘房,`,
    `2,迎賓,${at(15)},15,,攝影;音控;領班,大門口 welcome.mp3,賓客入場`,
    `3,一進預備,${at(40)},10,Y,攝影;音控;新秘,側門 entrance_v2.wav,`,
    `4,敬酒,${at(60)},20,,攝影,跟拍主桌,`,
    `5,送客,${at(80)},15,,攝影;音控;新秘,bye.mp3,`,
  ].join("\n") + "\n";
}

function parseLinks(out: string, count: number): Links[] {
  const events: Partial<Links>[] = Array.from({ length: count }, () => ({}));
  for (const line of out.split("\n")) {
    const [i, name, tok] = line.trim().split(" ");
    if (i && name && tok) events[Number(i) - 1][name as RoleName] = tok;
  }
  for (const e of events) {
    for (const r of ROLES) if (!e[r] || e[r]!.length !== 22) throw new Error(`seeding gave no link for ${r}:\n${out}`);
  }
  return events as Links[];
}

// seedArgs creates events first..first+count-1 on date.
const seedArgs = (bin: string, db: string, count: number, date = today(), first = 1) => [
  bin, db, date, String(count), Buffer.from(rundownCSV()).toString("base64"), String(first),
];

// setUp seeds `today` events on today and `later` events on tomorrow, in
// that order of index.
export type Counts = { today: number; later: number };

// ---------------------------------------------------------------- local

function localServe(s: NonNullable<State["local"]>, init: boolean): number {
  // BETHERE_INIT=1 only for the very first start: a restart must reopen the database.
  const env: NodeJS.ProcessEnv = { ...process.env };
  delete env.BETHERE_INIT;
  if (init) env.BETHERE_INIT = "1";
  const p = spawn(s.bin, ["serve", "-db", join(s.dir, "e2e.db"), "-addr", `127.0.0.1:${LOCAL_PORT}`], {
    env, stdio: ["ignore", "ignore", "inherit"], detached: true,
  });
  p.unref();
  return p.pid!;
}

async function waitUp(baseURL: string, up: boolean) {
  for (let i = 0; i < 100; i++) {
    const ok = await fetch(`${baseURL}/healthz`).then((r) => r.ok, () => false);
    if (ok === up) return;
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`server at ${baseURL} did not become ${up ? "ready" : "unreachable"}`);
}

// ---------------------------------------------------------------- VM

// vm runs a command on the VM through ops/gcp/env.sh, with stdin if given.
function vm(command: string, stdin?: string): string {
  return execFileSync(
    "bash",
    ["-c", 'source ops/gcp/env.sh && gc compute ssh "$VM" --zone "$ZONE" --tunnel-through-iap --command "$1"', "_", command],
    { cwd: ROOT, input: stdin, encoding: "utf8", stdio: ["pipe", "pipe", "inherit"], timeout: 120000 },
  );
}

const VM_DB = "/var/lib/bethere-e2e/e2e.db";
const VM_BIN = "/opt/bethere/bethere";

function vmBaseURL(): string {
  const url = process.env.E2E_BASE_URL;
  if (!url) throw new Error("E2E_TARGET=vm needs E2E_BASE_URL, e.g. https://e2e-34-80-0-1.sslip.io");
  return url.replace(/\/$/, "");
}

// ---------------------------------------------------------------- setup, stop, start

export async function setUp({ today: count, later }: Counts): Promise<State> {
  if (target === "vm") {
    const baseURL = vmBaseURL();
    // A fresh test database on every run: the test service only (ops/gcp/e2e-setup.sh).
    vm(`set -e
      sudo systemctl stop bethere-e2e
      sudo rm -f ${VM_DB} ${VM_DB}-wal ${VM_DB}-shm
      sudo -u bethere env BETHERE_INIT=1 timeout 5 ${VM_BIN} serve -db ${VM_DB} -addr 127.0.0.1:18082 || true
      sudo test -s ${VM_DB}
      sudo systemctl start bethere-e2e`);
    await waitUp(baseURL, true);
    const script = readFileSync(join(HERE, "seed.sh"), "utf8");
    const out =
      vm(`sudo -u bethere bash -s -- ${seedArgs(VM_BIN, VM_DB, count).join(" ")}`, script) +
      vm(`sudo -u bethere bash -s -- ${seedArgs(VM_BIN, VM_DB, later, tomorrow(), count + 1).join(" ")}`, script);
    return { baseURL, events: parseLinks(out, count + later) };
  }
  // Another server on the port would answer for a database this run never seeded.
  if (await fetch(`http://127.0.0.1:${LOCAL_PORT}/healthz`).then(() => true, () => false)) {
    throw new Error(`port ${LOCAL_PORT} is in use: stop the server left on it (lsof -ti tcp:${LOCAL_PORT} | xargs kill)`);
  }
  const dir = mkdtempSync(join(tmpdir(), "bethere-e2e-"));
  const bin = join(dir, "bethere");
  execFileSync("go", ["build", "-o", bin, "./cmd/bethere"], { cwd: ROOT, stdio: "inherit" });
  if (!existsSync(join(ROOT, "web/dist/index.html"))) throw new Error("build the web bundle first: npm run build");
  const local = { dir, bin, pid: 0 };
  local.pid = localServe(local, true);
  const baseURL = `http://127.0.0.1:${LOCAL_PORT}`;
  await waitUp(baseURL, true);
  const seed = (args: string[]) => execFileSync(join(HERE, "seed.sh"), args, { encoding: "utf8" });
  const out =
    seed(seedArgs(bin, join(dir, "e2e.db"), count)) + seed(seedArgs(bin, join(dir, "e2e.db"), later, tomorrow(), count + 1));
  return { baseURL, events: parseLinks(out, count + later), local };
}

export async function tearDown() {
  if (!existsSync(STATE)) return;
  const s = loadState();
  if (s.local) {
    try {
      process.kill(s.local.pid);
    } catch {
      /* gone already */
    }
    rmSync(s.local.dir, { recursive: true, force: true });
  }
  rmSync(STATE, { force: true });
}

// stopServer and startServer are the server outage of the restart route.
export async function stopServer() {
  const s = loadState();
  if (target === "vm") vm("sudo systemctl stop bethere-e2e");
  else process.kill(s.local!.pid);
  await waitUp(s.baseURL, false);
}

export async function startServer() {
  const s = loadState();
  if (target === "vm") vm("sudo systemctl start bethere-e2e");
  else {
    s.local!.pid = localServe(s.local!, false);
    saveState(s);
  }
  await waitUp(s.baseURL, true);
}
