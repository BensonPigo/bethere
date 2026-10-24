// The draft rundown editor (US-M-10) and the pasted import (US-M-12), M2c
// and ADR-0031. Every write is off while offline or busy (US-X-8.4).
import { useState } from "preact/hooks";
import type { ImportPreview, MasterCue, MasterSnapshot, WriteRequest } from "./api";
import { ClockInput, dayStarted } from "./controls";
import { formatHM, parseClock, zonedToISO } from "./format";
import { VIEWS } from "./RoleDialogs";

type Write = (w: WriteRequest) => Promise<boolean>;

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
      <div class="editor-help">
        每一列改完後按該列的「儲存」才會生效；誰負責這個環節、給他們的備註，按「指派」設定。存好後，相關的人不用重新整理就會看到。
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
      {/* Every field says what it is and means, on the field itself (US-M-10.C1 field test). */}
      <label class="cell">
        <span>開始時間</span>
        <ClockInput value={time} disabled={disabled} onValue={setTime} />
        <small>24 小時制，例如 09:20</small>
      </label>
      <label class="cell">
        <span>長度（分鐘）</span>
        <input class="duration" type="number" min={0} max={1440} value={duration} disabled={disabled} onInput={(e) => setDuration((e.currentTarget as HTMLInputElement).value)} />
        <small>預計進行多久</small>
      </label>
      <label class="cell wide">
        <span>標題</span>
        <input class="title" value={title} maxLength={100} disabled={disabled} onInput={(e) => setTitle((e.currentTarget as HTMLInputElement).value)} />
        <small>&nbsp;</small>
      </label>
      <label class="cell fixed">
        <span>固定時間</span>
        <input type="checkbox" checked={fixed} disabled={disabled} onChange={() => setFixed(!fixed)} />
        <small>延後或提早時不跟著動，例如吉時、開席</small>
      </label>
      <label class="cell wide">
        <span>公開備註</span>
        <input class="note" value={note} maxLength={500} disabled={disabled} onInput={(e) => setNote((e.currentTarget as HTMLInputElement).value)} />
        <small>被指派的人都看得到</small>
      </label>
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
        從 Excel 或 Google 試算表複製整份流程表貼上。第一列必須是欄位名稱（英文），之後每列一個環節：
      </div>
      <table class="import-columns">
        <tbody>
          <tr><td>seq</td><td>順序（1、2、3…）</td><td>必填</td></tr>
          <tr><td>title</td><td>環節名稱</td><td>必填</td></tr>
          <tr><td>start</td><td>開始時間，24 小時制，例如 09:20</td><td>必填</td></tr>
          <tr><td>duration_min</td><td>長度，幾分鐘</td><td>必填</td></tr>
          <tr><td>fixed</td><td>固定時間填 Y，否則空白</td><td>選填</td></tr>
          <tr><td>roles</td><td>負責的角色名稱，多個用分號隔開，例如 攝影;新秘</td><td>選填</td></tr>
          <tr><td>role_note</td><td>給負責角色的備註</td><td>選填</td></tr>
          <tr><td>note</td><td>公開備註</td><td>選填</td></tr>
        </tbody>
      </table>
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
