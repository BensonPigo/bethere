// The Master's view of the four jobs (US-M-16; api.md 9.4–9.6): the task
// pool, the guest list and the cashier's checklist, in one dialog so the
// console keeps only the common actions (V1).
import { useState } from "preact/hooks";
import type { MasterSnapshot, Task, WriteRequest } from "./api";
import { arrivedCount, doneCount, searchGuests, tableText, TASK_STATE_TEXT } from "./jobs";

type Write = (w: WriteRequest) => Promise<boolean>;
type Props = { snap: MasterSnapshot; lock: boolean; locked: boolean; write: Write; close: () => void };
type Tab = "tasks" | "guests" | "checklist";

// The console's entry: counts at a glance.
export function jobsSummary(snap: MasterSnapshot): string {
  const open = (snap.tasks ?? []).filter((t) => t.state !== "done").length;
  const g = arrivedCount(snap.guests ?? []);
  const c = doneCount(snap.checklist ?? []);
  const parts = [`任務 ${open}`];
  if (g.total) parts.push(`賓客 ${g.arrived}/${g.total}`);
  if (c.total) parts.push(`檢核 ${c.done}/${c.total}`);
  return parts.join("・");
}

export function MasterJobs({ snap, lock, locked, write, close }: Props) {
  const [tab, setTab] = useState<Tab>("tasks");
  return (
    <div class="editor jobs" role="dialog">
      <div class="editor-head">
        <span class="shift-title">現場工作</span>
        {(["tasks", "guests", "checklist"] as Tab[]).map((t) => (
          <button key={t} class={tab === t ? "tab on" : "tab"} aria-pressed={tab === t} onClick={() => setTab(t)}>
            {{ tasks: "任務池", guests: "賓客名單", checklist: "禮金檢核" }[t]}
          </button>
        ))}
        <button class="secondary" onClick={close}>關閉</button>
      </div>
      {tab === "tasks" && <Tasks tasks={snap.tasks ?? []} lock={lock} write={write} />}
      {tab === "guests" && <Guests snap={snap} lock={lock} locked={locked} write={write} />}
      {tab === "checklist" && <ChecklistEditor snap={snap} lock={lock} locked={locked} write={write} />}
    </div>
  );
}

function Tasks({ tasks, lock, write }: { tasks: Task[]; lock: boolean; write: Write }) {
  const [title, setTitle] = useState("");
  const add = async () => {
    if (await write({ path: "/api/tasks/add", title })) setTitle("");
  };
  return (
    <div class="jobs-body">
      <div class="row">
        <input value={title} maxLength={100} placeholder="例如：第 5 桌加兒童椅" onInput={(e) => setTitle((e.currentTarget as HTMLInputElement).value)} />
        <button class="confirm" disabled={lock || title.trim() === ""} onClick={() => void add()}>
          新增任務
        </button>
      </div>
      {tasks.length === 0 && <div class="empty">還沒有任務</div>}
      {tasks.map((t) => (
        <div class={`task ${t.state}`} key={t.id}>
          <span class="task-title">
            {t.title}
            <small>
              {t.createdBy ?? "—"}・{TASK_STATE_TEXT[t.state]}
              {t.claimedBy && t.state !== "open" ? `（${t.claimedBy}）` : ""}
            </small>
          </span>
          <span class="task-actions">
            {t.state !== "done" ? (
              <button disabled={lock} onClick={() => void write({ path: "/api/tasks/set", taskId: t.id, state: "done" })}>
                完成
              </button>
            ) : (
              <button disabled={lock} onClick={() => void write({ path: "/api/tasks/set", taskId: t.id, state: "open" })}>
                重新開放
              </button>
            )}
            <button
              disabled={lock}
              onClick={() => window.confirm(`刪除任務「${t.title}」？`) && void write({ path: "/api/tasks/delete", taskId: t.id })}
            >
              刪除
            </button>
          </span>
        </div>
      ))}
    </div>
  );
}

function Guests({ snap, lock, locked, write }: { snap: MasterSnapshot; lock: boolean; locked: boolean; write: Write }) {
  const guests = snap.guests ?? [];
  const [text, setText] = useState("");
  const [q, setQ] = useState("");
  const c = arrivedCount(guests);
  const started = c.arrived > 0;
  const replace = async () => {
    const msg = guests.length ? `現有的 ${guests.length} 位賓客將被取代，確定嗎？` : "匯入這份名單？";
    if (window.confirm(msg) && (await write({ path: "/api/guests/import", text }))) setText("");
  };
  return (
    <div class="jobs-body">
      <div class="label">
        已報到 {c.arrived}／{c.total} 位
      </div>
      <input type="search" placeholder="找賓客：輸入姓名或桌號" value={q} onInput={(e) => setQ((e.currentTarget as HTMLInputElement).value)} />
      {searchGuests(guests, q).map((g) => (
        <div class={`guest ${g.arrived ? "arrived" : ""}`} key={g.id}>
          <span class="guest-name">{g.name}</span>
          <span class="guest-table">{tableText(g.table)}</span>
          <button disabled={lock} onClick={() => void write({ path: "/api/guests/arrive", guestId: g.id, arrived: !g.arrived })}>
            {g.arrived ? "已報到 ✔" : "報到"}
          </button>
          {g.note && <span class="note">{g.note}</span>}
        </div>
      ))}
      {/* US-RC-1.4: once it cannot be replaced, the paste box is not shown at all. */}
      {locked || started ? (
        <div class="hint">{locked ? "活動日當天不能更換整份名單" : "已經有賓客報到，不能更換整份名單"}</div>
      ) : (
        <>
          <div class="label">貼上名單（從試算表複製：姓名、桌號、備註，一行一位）</div>
          <textarea
            rows={6}
            value={text}
            placeholder={"例如：\n陳大文\t5\t素食\n林阿姨\t主桌\t輪椅"}
            onInput={(e) => setText((e.currentTarget as HTMLTextAreaElement).value)}
          />
          <button class="confirm" disabled={lock || text.trim() === ""} onClick={() => void replace()}>
            匯入（取代整份名單）
          </button>
        </>
      )}
    </div>
  );
}

function ChecklistEditor({ snap, lock, locked, write }: { snap: MasterSnapshot; lock: boolean; locked: boolean; write: Write }) {
  const items = snap.checklist ?? [];
  const [text, setText] = useState(() => items.map((i) => i.title).join("\n"));
  const c = doneCount(items);
  const started = c.done > 0;
  return (
    <div class="jobs-body">
      <div class="label">
        完成 {c.done}／{c.total} 項
      </div>
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
      {locked || started ? (
        <div class="hint">{locked ? "活動日當天不能更換檢核表" : "已經有項目完成，不能更換檢核表"}</div>
      ) : (
        <>
          <div class="label">檢核項目（一行一項，最多 30 項）</div>
          <textarea
            rows={6}
            value={text}
            placeholder={"例如：\n總金額確認\n簽名確認\n鑰匙交給："}
            onInput={(e) => setText((e.currentTarget as HTMLTextAreaElement).value)}
          />
          <button
            class="confirm"
            disabled={lock || text.trim() === ""}
            onClick={() => window.confirm("以這份內容取代檢核表？") && void write({ path: "/api/checklist/items", text })}
          >
            儲存檢核表
          </button>
        </>
      )}
    </div>
  );
}
