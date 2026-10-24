// The Master's preparation before the event (M2c, ADR-0031): the draft
// rundown editor (US-M-10), the pasted import (US-M-12), adding a role
// with its link and QR code (US-M-11, US-M-13) and the role preview
// (US-M-14). Every write is off while offline or busy (US-X-8.4).
import { useState } from "preact/hooks";
import { isHostSnapshot } from "./api";
import type { ImportPreview, MasterCue, MasterSnapshot, NewRole, RoleSnapshot, Snapshot, WriteRequest } from "./api";
import { Dashboard } from "./Dashboard";
import { formatHM, parseClock, zonedToISO } from "./format";
import { Glance } from "./Glance";
import { HandsFree } from "./HandsFree";
import type { NewLink } from "./session";

type Write = (w: WriteRequest) => Promise<boolean>;

// dayStarted: from 00:00 of the event day on, replacing the rundown and
// deleting a role are locked (ADR-0032).
export function dayStarted(eventDate: string, today: string): boolean {
  return today >= eventDate.slice(0, 10);
}

// The link shown once after it is made: text and copy, with its QR code
// for a phone held next to the tablet; never a link that opens here
// (api.md section 3).
export function LinkResult({ link }: { link: NewLink }) {
  const url = `${location.origin}/#t=${link.token}`;
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  };
  return (
    <div class="link-result">
      <div>新連結（只顯示這一次，請用 LINE 傳給對方，或讓對方掃描）：</div>
      <input class="link-text" readOnly value={url} onFocus={(e) => (e.currentTarget as HTMLInputElement).select()} />
      <button class="confirm" onClick={() => void copy()}>
        {copied ? "已複製 ✔" : "複製"}
      </button>
      {link.qr && <img class="qr" src={link.qr} alt="連結的 QR Code" width={220} height={220} />}
    </div>
  );
}

const VIEWS: { view: NewRole["view"]; label: string; hint: string }[] = [
  { view: "HandsFree", label: "免手持", hint: "手機放桌上，大字倒數（例如新秘）" },
  { view: "Glance", label: "快速瞄", hint: "手機在口袋，拿出來看下一個環節（例如攝影）" },
  { view: "Dashboard", label: "平安儀表板", hint: "只顯示一切順利或延誤（例如新人）" },
];

type AddRoleProps = {
  lock: boolean;
  addRole: (r: NewRole) => Promise<NewLink | null>;
  close: () => void;
};

// US-M-11.1: a name, a screen and whether it checks in; the server decides
// the rest (ADR-0031).
export function AddRoleDialog({ lock, addRole, close }: AddRoleProps) {
  const [name, setName] = useState("");
  const [view, setView] = useState<NewRole["view"]>("Glance");
  const [checksIn, setChecksIn] = useState(true);
  const [link, setLink] = useState<NewLink | null>(null);
  const dashboard = view === "Dashboard";
  return (
    <div class="shift-panel dialog" role="dialog">
      <div class="shift-title">新增角色</div>
      {link ? (
        <LinkResult link={link} />
      ) : (
        <>
          <label class="field">
            名稱
            <input value={name} maxLength={50} placeholder="例如：伴娘、婚攝 B" onInput={(e) => setName((e.currentTarget as HTMLInputElement).value)} />
          </label>
          <div class="label">畫面</div>
          {VIEWS.map((v) => (
            <label class="pick" key={v.view}>
              <input type="radio" name="view" checked={view === v.view} onChange={() => setView(v.view)} />
              {v.label}
              <small>{v.hint}</small>
            </label>
          ))}
          <label class="pick">
            <input type="checkbox" checked={checksIn && !dashboard} disabled={dashboard} onChange={() => setChecksIn(!checksIn)} />
            需要就位打卡
          </label>
          <button
            class="confirm"
            disabled={lock || name.trim() === ""}
            onClick={() => void addRole({ name, view, checksIn: checksIn && !dashboard }).then(setLink)}
          >
            新增並產生連結
          </button>
        </>
      )}
      <div class="shift-actions">
        <button onClick={close}>關閉</button>
      </div>
    </div>
  );
}

type EditorProps = {
  snap: MasterSnapshot;
  tz: string;
  today: string;
  lock: boolean;
  write: Write;
  openCue: (c: MasterCue) => void;
  insertAfter: (c: MasterCue | null) => void;
  openImport: () => void;
  close: () => void;
};

// US-M-10: the whole rundown as a table, in draft only; live, the
// structure is the shift's and only the console's limited edits remain.
export function RundownEditor({ snap, tz, today, lock, write, openCue, insertAfter, openImport, close }: EditorProps) {
  const live = snap.eventStatus !== "Draft";
  const order = new Set((snap.outOfOrder ?? []).map((o) => String(o.cueId)));
  return (
    <div class="editor" role="dialog">
      <div class="editor-head">
        <span class="shift-title">編輯流程表</span>
        <button disabled={lock || live} onClick={() => insertAfter(null)}>
          新增第一個環節
        </button>
        <button disabled={lock || live || dayStarted(snap.event.date, today)} onClick={openImport}>
          貼上匯入
        </button>
        <button onClick={close}>關閉</button>
      </div>
      {live && <div class="reason">活動已開始，時間請用平移；標題、備註與指派請點主控台的環節修改</div>}
      {!live && dayStarted(snap.event.date, today) && <div class="reason">今天是活動日：不能整份貼上取代，請逐一修改環節</div>}
      {snap.cues.length === 0 && <div class="msg">還沒有環節。按「新增第一個環節」或「貼上匯入」開始。</div>}
      <div class="editor-rows">
        {snap.cues.map((c, i) => (
          <EditorRow
            // A new version, from this device or another, starts the row over from the server's values.
            key={`${String(c.id)}:${snap.eventVersion}`}
            snap={snap}
            cue={c}
            prev={i > 0 ? snap.cues[i - 1] : null}
            next={i < snap.cues.length - 1 ? snap.cues[i + 1] : null}
            beforePrev={i > 1 ? snap.cues[i - 2] : null}
            warn={order.has(String(c.id))}
            tz={tz}
            disabled={lock || live}
            write={write}
            openCue={openCue}
            insertAfter={insertAfter}
          />
        ))}
      </div>
    </div>
  );
}

type RowProps = {
  snap: MasterSnapshot;
  cue: MasterCue;
  prev: MasterCue | null;
  next: MasterCue | null;
  beforePrev: MasterCue | null;
  warn: boolean;
  tz: string;
  disabled: boolean;
  write: Write;
  openCue: (c: MasterCue) => void;
  insertAfter: (c: MasterCue | null) => void;
};

function EditorRow({ snap, cue, prev, next, beforePrev, warn, tz, disabled, write, openCue, insertAfter }: RowProps) {
  const [time, setTime] = useState(formatHM(cue.scheduledStart, tz));
  const [duration, setDuration] = useState(String(cue.durationMin));
  const [title, setTitle] = useState(cue.title);
  const [fixed, setFixed] = useState(cue.isFixed);
  const [note, setNote] = useState(cue.notePublic ?? "");
  const clock = parseClock(time);
  const start = clock === null ? null : zonedToISO(snap.event.date, clock, tz);
  const minutes = Number.parseInt(duration, 10);
  const valid = start !== null && Number.isFinite(minutes) && minutes >= 0 && minutes <= 1440 && title.trim() !== "";
  const changed =
    (clock ?? time) !== formatHM(cue.scheduledStart, tz) ||
    minutes !== cue.durationMin ||
    title !== cue.title ||
    fixed !== cue.isFixed ||
    note !== (cue.notePublic ?? "");
  const move = (afterCueId: string | number | null) => void write({ path: "/api/cues/move", cueId: cue.id, afterCueId });
  return (
    <div class={`editor-row ${cue.status.toLowerCase()} ${changed ? "unsaved" : ""}`} data-cue={String(cue.id)}>
      <ClockInput value={time} disabled={disabled} onValue={setTime} />
      <input class="duration" type="number" aria-label="長度（分）" min={0} max={1440} value={duration} disabled={disabled} onInput={(e) => setDuration((e.currentTarget as HTMLInputElement).value)} />
      <input class="title" aria-label="標題" value={title} maxLength={100} disabled={disabled} onInput={(e) => setTitle((e.currentTarget as HTMLInputElement).value)} />
      <label class="fixed">
        <input type="checkbox" checked={fixed} disabled={disabled} onChange={() => setFixed(!fixed)} />
        固定
      </label>
      <input class="note" aria-label="公開備註" placeholder="公開備註" value={note} maxLength={500} disabled={disabled} onInput={(e) => setNote((e.currentTarget as HTMLInputElement).value)} />
      <button
        class="confirm"
        disabled={disabled || !changed || !valid}
        onClick={() =>
          start &&
          void write({
            path: "/api/cues/update",
            cue: { cueId: cue.id, start, durationMin: minutes, isFixed: fixed, title, notePublic: note === "" ? null : note },
          })
        }
      >
        儲存
      </button>
      <div class="row-actions">
        <button aria-label="上移" disabled={disabled || !prev} onClick={() => move(beforePrev ? beforePrev.id : null)}>
          ↑
        </button>
        <button aria-label="下移" disabled={disabled || !next} onClick={() => next && move(next.id)}>
          ↓
        </button>
        <button onClick={() => openCue(cue)}>指派{cue.roles.length > 0 ? `（${cue.roles.map((r) => r.name).join("、")}）` : ""}</button>
        <button disabled={disabled} onClick={() => insertAfter(cue)}>
          ＋ 在後面新增
        </button>
        <button
          class="danger"
          disabled={disabled}
          onClick={() => window.confirm(`刪除「${cue.title}」？指派與備註會一起刪除。`) && void write({ path: "/api/cues/delete", cueId: cue.id })}
        >
          刪除
        </button>
      </div>
      {changed && (
        <div class="unsaved-note">
          {clock === null ? "時間請用 24 小時制，例如 09:20 或 0920" : valid ? "尚未儲存，請按「儲存」" : "尚未儲存：長度或標題不正確"}
        </div>
      )}
      {warn && prev && <div class="reason">⚠ 時間早於前一個環節 {formatHM(prev.effectiveStart, tz)}</div>}
    </div>
  );
}

// ClockInput is a plain 24-hour time field: the same on every browser,
// with the number pad on a phone or tablet (see parseClock).
export function ClockInput(props: { value: string; disabled?: boolean; onValue: (v: string) => void }) {
  const set = (e: Event) => props.onValue((e.currentTarget as HTMLInputElement).value);
  return (
    <input
      class="time"
      type="text"
      inputMode="numeric"
      autoComplete="off"
      aria-label="開始時間"
      placeholder="09:20"
      maxLength={5}
      value={props.value}
      disabled={props.disabled}
      onInput={set}
      onChange={set}
    />
  );
}

type ImportProps = {
  snap: MasterSnapshot;
  lock: boolean;
  importPreview: (text: string) => Promise<ImportPreview | string>;
  write: Write;
  close: () => void;
};

const NEW_ROLE_VIEWS = VIEWS.filter((v) => v.view !== "Dashboard");

// US-M-12: paste from a spreadsheet, check, then replace the whole rundown
// after a second confirmation; roles the text mentions but the event lacks
// are created in the same step (US-M-12.6).
export function ImportDialog({ snap, lock, importPreview, write, close }: ImportProps) {
  const [text, setText] = useState("");
  const [checked, setChecked] = useState<{ text: string; result: ImportPreview | string } | null>(null);
  const [roles, setRoles] = useState<Record<string, { view: "HandsFree" | "Glance"; checksIn: boolean }>>({});
  const preview = checked && checked.text === text && typeof checked.result === "object" ? checked.result : null;
  const bad = preview
    ? preview.fileError !== null || preview.tooManyRoles || preview.rows.length === 0 || preview.rows.some((r) => r.errors.length > 0)
    : true;
  const roleOf = (name: string) => roles[name] ?? { view: "Glance" as const, checksIn: true };
  const run = async () => setChecked({ text, result: await importPreview(text) });
  const confirm = async () => {
    if (!preview) return;
    const n = preview.replaces;
    if (n > 0 && !window.confirm(`現有的 ${n} 個環節與指派將被取代。確定嗎？`)) return;
    const newRoles = preview.missingRoles.map((name) => ({ name, ...roleOf(name) }));
    if (await write({ path: "/api/rundown/import", text, newRoles })) close();
  };
  return (
    <div class="editor" role="dialog">
      <div class="editor-head">
        <span class="shift-title">貼上匯入（取代整份流程表）</span>
        <button onClick={close}>取消</button>
      </div>
      <div class="label">
        從 Excel 或 Google 試算表複製整份流程表貼上，第一列為欄位名稱：seq、title、start、duration_min、fixed、roles、role_note、note
      </div>
      <textarea class="import-text" value={text} rows={8} onInput={(e) => setText((e.currentTarget as HTMLTextAreaElement).value)} />
      <button disabled={text.trim() === ""} onClick={() => void run()}>
        預覽
      </button>
      {checked && checked.text === text && typeof checked.result === "string" && <div class="reason">{checked.result}</div>}
      {preview?.fileError && <div class="reason">{preview.fileError}</div>}
      {preview && preview.rows.length > 0 && (
        <table class="import-preview">
          <tbody>
            {preview.rows.map((r) => (
              <tr key={r.line} class={r.errors.length > 0 ? "bad" : ""}>
                <td>{r.line}</td>
                <td>{r.start}</td>
                <td>{r.title}</td>
                <td>{r.durationMin} 分</td>
                <td>{r.roles.join("、")}</td>
                <td>{r.errors.join("；")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {preview && preview.missingRoles.length > 0 && (
        <div class="missing-roles">
          <div class="label">以下角色還不存在，會一起建立（連結之後在角色清單產生）：</div>
          {preview.missingRoles.map((name) => (
            <div class="pick" key={name}>
              <strong>{name}</strong>
              <select
                value={roleOf(name).view}
                onChange={(e) => setRoles({ ...roles, [name]: { ...roleOf(name), view: (e.currentTarget as HTMLSelectElement).value as "HandsFree" | "Glance" } })}
              >
                {NEW_ROLE_VIEWS.map((v) => (
                  <option key={v.view} value={v.view}>
                    {v.label}
                  </option>
                ))}
              </select>
              <label>
                <input type="checkbox" checked={roleOf(name).checksIn} onChange={() => setRoles({ ...roles, [name]: { ...roleOf(name), checksIn: !roleOf(name).checksIn } })} />
                需要就位打卡
              </label>
            </div>
          ))}
        </div>
      )}
      {preview?.tooManyRoles && <div class="reason">建立這些角色後會超過 50 個角色的上限</div>}
      {preview && (
        <div class="shift-actions">
          <button class="confirm danger" disabled={lock || bad || snap.eventStatus !== "Draft"} onClick={() => void confirm()}>
            {preview.missingRoles.length > 0 ? `建立 ${preview.missingRoles.length} 個角色並匯入` : "匯入（取代整份）"}
          </button>
        </div>
      )}
    </div>
  );
}

type PreviewProps = { snap: Snapshot; name: string; now: number; skew: number; close: () => void };

// US-M-14: the role's own screen from its own snapshot, read-only; it is
// never stored and never handles a change notice.
export function RolePreview({ snap, name, now, skew, close }: PreviewProps) {
  const noWrite: Write = async () => false;
  let screen;
  if (isHostSnapshot(snap)) screen = <Dashboard snap={snap} />;
  else {
    const rs = snap as RoleSnapshot;
    screen =
      rs.role.view === "HandsFree" ? (
        <HandsFree snap={rs} now={now} skew={skew} change={null} offline={false} write={noWrite} readOnly />
      ) : (
        <Glance snap={rs} now={now} skew={skew} change={null} offline={false} busy={false} write={noWrite} readOnly />
      );
  }
  return (
    <div class="role-preview" role="dialog">
      <div class="preview-bar">
        <span>預覽：{name} 看到的畫面</span>
        <button onClick={close}>結束預覽</button>
      </div>
      <div class="app">{screen}</div>
    </div>
  );
}
