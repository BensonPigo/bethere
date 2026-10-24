// The one extra task of each job on the glance screen (api.md section 9,
// ADR-0035; screen-design.md 4.6): runners' task pool, the reception's
// guest search and call for help, the cashier's checklist.
import { useState } from "preact/hooks";
import type { Guest, RoleSnapshot, Task, WriteRequest } from "./api";
import { callRunnerTitle, doneCount, searchGuests, tableText, TASK_STATE_TEXT } from "./jobs";

type Write = (w: WriteRequest) => Promise<boolean>;
type Props = { snap: RoleSnapshot; lock: boolean; write: Write };

// hasPanel: the jobs whose glance screen leads with their own tool.
export const hasPanel = (snap: RoleSnapshot) => ["Runner", "Reception", "Cashier"].includes(snap.role.type);

export function JobPanel({ snap, lock, write }: Props) {
  switch (snap.role.type) {
    case "Runner":
      return <TaskPool tasks={snap.tasks ?? []} me={snap.role.name} lock={lock} write={write} />;
    case "Reception":
      return <Reception snap={snap} lock={lock} write={write} />;
    case "Cashier":
      return <Checklist snap={snap} lock={lock} write={write} />;
  }
  return null;
}

// US-RN-1: open tasks to take, the runner's own to release or finish.
function TaskPool({ tasks, me, lock, write }: { tasks: Task[]; me: string; lock: boolean; write: Write }) {
  const set = (t: Task, state: Task["state"]) => void write({ path: "/api/tasks/set", taskId: t.id, state });
  const open = tasks.filter((t) => t.state === "open");
  const mine = tasks.filter((t) => t.state === "claimed" && t.claimedBy === me);
  const others = tasks.filter((t) => t.state === "claimed" && t.claimedBy !== me);
  const done = tasks.filter((t) => t.state === "done");
  return (
    <div class="job-panel tasks">
      <div class="label">任務池</div>
      {open.length === 0 && mine.length === 0 && <div class="empty">目前沒有待認領的任務</div>}
      {mine.map((t) => (
        <div class="task mine" key={t.id}>
          <span class="task-title">{t.title}</span>
          <span class="task-actions">
            <button class="confirm" disabled={lock} onClick={() => set(t, "done")}>
              完成
            </button>
            <button disabled={lock} onClick={() => set(t, "open")}>
              放回
            </button>
          </span>
        </div>
      ))}
      {open.map((t) => (
        <div class="task" key={t.id}>
          <span class="task-title">
            {t.title}
            {t.createdBy && <small>（{t.createdBy}）</small>}
          </span>
          <button class="confirm" disabled={lock} onClick={() => set(t, "claimed")}>
            我來
          </button>
        </div>
      ))}
      {others.map((t) => (
        <div class="task other" key={t.id}>
          <span class="task-title">{t.title}</span>
          <small>{t.claimedBy} 處理中</small>
        </div>
      ))}
      {done.length > 0 && <div class="done-line">你完成了 {done.length} 個任務</div>}
    </div>
  );
}

// US-RC-1, US-RC-2: one-thumb search, arrival, and a call for a runner.
function Reception({ snap, lock, write }: Props) {
  const [q, setQ] = useState("");
  const guests = snap.guests ?? [];
  const found = searchGuests(guests, q);
  const calls = snap.tasks ?? [];
  const arrive = (g: Guest) => void write({ path: "/api/guests/arrive", guestId: g.id, arrived: !g.arrived });
  return (
    <div class="job-panel reception">
      <button class="call" disabled={lock} onClick={() => void write({ path: "/api/tasks/add", title: callRunnerTitle(snap.role.name) })}>
        呼叫機動組
      </button>
      {calls.slice(0, 3).map((t) => (
        <div class="call-state" key={t.id}>
          {t.title}：{TASK_STATE_TEXT[t.state]}
          {t.claimedBy && t.state !== "open" ? `（${t.claimedBy}）` : ""}
        </div>
      ))}
      <div class="label">{guests.length ? `找賓客（${guests.length} 位）` : "還沒有賓客名單"}</div>
      <input
        class="guest-search"
        type="search"
        inputMode="search"
        aria-label="找賓客"
        placeholder="姓名或桌號"
        value={q}
        onInput={(e) => setQ((e.currentTarget as HTMLInputElement).value)}
      />
      {found.map((g) => (
        <div class={`guest ${g.arrived ? "arrived" : ""}`} key={g.id}>
          <span class="guest-name">{g.name}</span>
          <span class="guest-table">{tableText(g.table)}</span>
          <button disabled={lock} onClick={() => arrive(g)}>
            {g.arrived ? "已報到 ✔" : "報到"}
          </button>
          {g.note && <span class="note">{g.note}</span>}
        </div>
      ))}
      {q.trim() !== "" && found.length === 0 && <div class="empty">找不到「{q.trim()}」</div>}
    </div>
  );
}

// US-CA-1: tick each step of the hand-over.
function Checklist({ snap, lock, write }: Props) {
  const items = snap.checklist ?? [];
  const c = doneCount(items);
  return (
    <div class="job-panel checklist">
      <div class="label">
        交接檢核表 {c.done}/{c.total}
      </div>
      {items.length === 0 && <div class="empty">總召還沒有設定檢核表</div>}
      {items.map((it) => (
        <label class={`check ${it.done ? "done" : ""}`} key={it.id}>
          <input
            type="checkbox"
            checked={it.done}
            disabled={lock}
            onChange={() => void write({ path: "/api/checklist/check", itemId: it.id, done: !it.done })}
          />
          <span>{it.title}</span>
          {it.done && it.doneBy && <small>{it.doneBy}</small>}
        </label>
      ))}
    </div>
  );
}
