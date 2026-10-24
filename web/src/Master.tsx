// Master console (screen-design.md section 3; US-M-1 to US-M-8).
import { useEffect, useRef, useState } from "preact/hooks";
import type {
  CueStatus, ImportPreview, MasterCue, MasterRole, MasterSnapshot, NewRole, ShiftPreview, ShiftRequest, Snapshot, WriteRequest,
} from "./api";
import { dateInZone, formatHM } from "./format";
import { CueDialog, InsertDialog, RoleMenu, RolesPanel } from "./MasterDialogs";
import { AddRoleDialog, dayStarted, ImportDialog, RolePreview, RundownEditor } from "./Prep";
import type { NewLink } from "./session";
import { OVERRUN_PROMPT_MIN, signed, suggestedShift } from "./notice";
import { OriginalTime } from "./OriginalTime";

const ICON: Record<CueStatus, string> = { Completed: "✔", InProgress: "▶", Pending: "○", Skipped: "⊘" };
const STATUS_TEXT: Record<CueStatus, string> = {
  Completed: "完成",
  InProgress: "進行中",
  Pending: "待辦",
  Skipped: "略過",
};

type Props = {
  snap: MasterSnapshot;
  correctedNow: number;
  offline: boolean;
  busy: boolean;
  write: (w: WriteRequest) => Promise<boolean>;
  reissue: (roleId: string | number) => Promise<NewLink | null>;
  preview: (req: ShiftRequest) => Promise<ShiftPreview | string>;
  // The preparation (M2c).
  addRole: (r: NewRole) => Promise<NewLink | null>;
  importPreview: (text: string) => Promise<ImportPreview | string>;
  rolePreview: (roleId: string | number) => Promise<Snapshot | string>;
  skew: number;
  now: number;
};

// What the console has open over the panels: one dialog at a time, over
// the rundown editor when it is open.
type Open =
  | { kind: "role"; id: string | number }
  | { kind: "cue"; id: string | number }
  | { kind: "insert"; after: string | number | null }
  | { kind: "import" }
  | { kind: "addRole" }
  | { kind: "preview"; snap: Snapshot; name: string };

export function Master(props: Props) {
  const { snap, correctedNow, offline, busy, write, reissue, preview } = props;
  const tz = snap.event.timezone || "Asia/Taipei";
  const active = snap.cues.find((c) => c.status === "InProgress") ?? null;
  const nextPending = snap.cues.find((c) => c.status === "Pending") ?? null;
  const focusId = (active ?? nextPending)?.id ?? null;
  const [shiftDelta, setShiftDelta] = useState<number | null>(null);
  const [open, setOpen] = useState<Open | null>(null);
  const [editing, setEditing] = useState(false);

  const listRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const list = listRef.current;
    if (!list || focusId === null) return;
    const row = list.querySelector<HTMLElement>(`[data-cue="${String(focusId)}"]`);
    // Put the current cue a quarter of the way down so the following cues stay visible.
    if (row) list.scrollTo({ top: row.offsetTop - list.clientHeight * 0.25, behavior: "smooth" });
  }, [focusId]);

  const today = dateInZone(correctedNow, tz);
  const isEventDay = today === snap.event.date.slice(0, 10);
  const draft = snap.eventStatus === "Draft";
  const lock = offline || busy;
  const conflicted = new Set(snap.conflicts.map((c) => String(c.cueId)));
  const outOfOrder = new Set((snap.outOfOrder ?? []).map((o) => String(o.cueId)));
  const cueById = (id: string | number) => snap.cues.find((c) => String(c.id) === String(id)) ?? null;
  const roleById = (id: string | number): MasterRole | null => snap.roles.find((r) => String(r.id) === String(id)) ?? null;

  // ADR-0020: no start button; the first advance on the event day starts.
  let advanceReason: string | null = null;
  if (offline) advanceReason = "離線中，無法操作";
  else if (draft && !isEventDay) advanceReason = "活動日當天才能開始";

  const canShift = !draft && !lock && nextPending !== null;
  const overrun = !draft && snap.overrunMin >= OVERRUN_PROMPT_MIN ? snap.overrunMin : 0;

  return (
    <div class="master">
      <div class="timeline" ref={listRef}>
        {snap.cues.map((c) => (
          <CueRow
            key={c.id}
            cue={c}
            tz={tz}
            conflict={conflicted.has(String(c.id))}
            outOfOrder={outOfOrder.has(String(c.id))}
            open={() => setOpen({ kind: "cue", id: c.id })}
          />
        ))}
      </div>
      <div class="panel">
        <div class="panel-head">
          {snap.event.name}・現在 {formatHM(correctedNow, tz)}
          {nextPending && nextPending.offsetMin !== 0 && <span class="offset-all">・目前偏移 {signed(nextPending.offsetMin)} 分</span>}
        </div>
        <div class="current">
          {draft ? (
            <>
              <div class="label">活動尚未開始</div>
              <div class="cue-title">{nextPending ? `第一個環節：${nextPending.title}` : "沒有環節"}</div>
            </>
          ) : (
            <>
              <div class="label">目前環節</div>
              {active ? (
                <>
                  <div class="cue-title">{active.title}</div>
                  <div class="elapsed">{elapsedText(active, correctedNow)}</div>
                </>
              ) : (
                <div class="cue-title">{nextPending ? "尚未開始下一個環節" : "所有環節已完成"}</div>
              )}
              {nextPending && (
                <div class="next">
                  下一個：{formatHM(nextPending.effectiveStart, tz)} {nextPending.title}
                </div>
              )}
            </>
          )}
        </div>

        {overrun > 0 && (
          <button class="overrun" disabled={!canShift} onClick={() => setShiftDelta(suggestedShift(overrun))}>
            已超時 {overrun} 分，要延後後續嗎？（{signed(suggestedShift(overrun))} 分）
          </button>
        )}

        <button
          class="big advance"
          disabled={lock || (draft && !isEventDay) || (!active && !nextPending)}
          onClick={() => void write({ path: "/api/cues/advance" })}
        >
          推進
        </button>
        {advanceReason && <div class="reason">{advanceReason}</div>}

        <div class="shift-row">
          {[5, 10, -5].map((d) => (
            <button key={d} class="shift" disabled={!canShift} onClick={() => setShiftDelta(d)}>
              {signed(d)}
            </button>
          ))}
          <button class="shift" disabled={!canShift} onClick={() => setShiftDelta(askMinutes())}>
            自訂…
          </button>
        </div>

        {/* In draft too: a skip there can be undone (US-M-6.4). */}
        <button class="undo" disabled={lock || !snap.undo.available} onClick={() => void write({ path: "/api/cues/undo" })}>
          <span>復原上一步</span>
          {snap.undo.available && <small>{snap.undo.description}</small>}
        </button>

        {draft && (
          <button class="edit-rundown" onClick={() => setEditing(true)}>
            編輯流程表
          </button>
        )}

        <RolesPanel
          snap={snap}
          now={correctedNow}
          open={(r) => setOpen({ kind: "role", id: r.id })}
          add={() => setOpen({ kind: "addRole" })}
        />
      </div>

      {editing && (
        <RundownEditor
          snap={snap}
          tz={tz}
          today={today}
          lock={lock}
          write={write}
          openCue={(c) => setOpen({ kind: "cue", id: c.id })}
          insertAfter={(c) => setOpen({ kind: "insert", after: c ? c.id : null })}
          openImport={() => setOpen({ kind: "import" })}
          close={() => setEditing(false)}
        />
      )}
      {open?.kind === "role" && roleById(open.id) && (
        <RoleMenu
          role={roleById(open.id)!}
          lock={lock}
          deleteReason={!draft ? "活動已開始，不能刪除角色" : dayStarted(snap.event.date, today) ? "活動日當天不能刪除角色，可撤銷連結" : null}
          write={write}
          reissue={reissue}
          preview={async (r) => {
            const s = await props.rolePreview(r.id);
            if (typeof s === "string") window.alert(s);
            else setOpen({ kind: "preview", snap: s, name: r.name });
          }}
          close={() => setOpen(null)}
        />
      )}
      {open?.kind === "addRole" && <AddRoleDialog lock={lock} addRole={props.addRole} close={() => setOpen(null)} />}
      {open?.kind === "import" && (
        <ImportDialog snap={snap} lock={lock} importPreview={props.importPreview} write={write} close={() => setOpen(null)} />
      )}
      {open?.kind === "preview" && (
        <RolePreview snap={open.snap} name={open.name} now={props.now} skew={props.skew} close={() => setOpen(null)} />
      )}
      {open?.kind === "cue" && cueById(open.id) && (
        <CueDialog
          key={String(open.id)}
          snap={snap}
          cue={cueById(open.id)!}
          tz={tz}
          lock={lock}
          write={write}
          insertAfter={(c) => setOpen({ kind: "insert", after: c.id })}
          close={() => setOpen(null)}
        />
      )}
      {open?.kind === "insert" && (open.after === null || cueById(open.after)) && (
        <InsertDialog
          snap={snap}
          after={open.after === null ? null : cueById(open.after)}
          tz={tz}
          lock={lock}
          write={write}
          close={() => setOpen(null)}
        />
      )}

      {shiftDelta !== null && shiftDelta !== 0 && nextPending && (
        <ShiftPanel
          snap={snap}
          tz={tz}
          from={nextPending}
          delta={shiftDelta}
          disabled={lock}
          preview={preview}
          apply={async (req) => {
            if (await write({ path: "/api/cues/shift", shift: req })) setShiftDelta(null);
          }}
          close={() => setShiftDelta(null)}
        />
      )}
    </div>
  );
}

// The custom shift; 0 or a non-number cancels.
function askMinutes(): number | null {
  const v = window.prompt("平移幾分鐘？（提早請輸入負數，例如 -5）", "15");
  const n = v === null ? NaN : Number.parseInt(v, 10);
  return Number.isFinite(n) && n !== 0 ? n : null;
}

type PanelProps = {
  snap: MasterSnapshot;
  tz: string;
  from: MasterCue;
  delta: number;
  disabled: boolean;
  preview: (req: ShiftRequest) => Promise<ShiftPreview | string>;
  apply: (req: ShiftRequest) => Promise<void>;
  close: () => void;
};

// The shift preview (US-M-2.1–2.5): every cue the shift would move, each
// can be unticked; nothing is written until confirmed.
function ShiftPanel({ snap, tz, from, delta, disabled, preview, apply, close }: PanelProps) {
  const [excluded, setExcluded] = useState<(string | number)[]>([]);
  const [candidates, setCandidates] = useState<(string | number)[] | null>(null);
  const [result, setResult] = useState<ShiftPreview | string | null>(null);
  const req: ShiftRequest = { fromCueId: from.id, deltaMin: delta, excluded };

  useEffect(() => {
    let live = true;
    void preview(req).then((r) => {
      if (!live) return;
      setResult(r);
      // The ticked list is what the shift would move with nothing unticked.
      if (candidates === null && typeof r !== "string") setCandidates(r.changes.map((c) => c.cueId));
    });
    return () => {
      live = false;
    };
    // Re-run when the request or the event changes.
  }, [delta, excluded.join(","), snap.eventVersion]);

  const byId = new Map(snap.cues.map((c) => [String(c.id), c]));
  const moves = new Map(typeof result === "object" && result ? result.changes.map((c) => [String(c.cueId), c]) : []);
  const tooEarly = typeof result === "object" && result ? result.tooEarly : null;
  const conflicts = typeof result === "object" && result ? result.conflicts : [];
  const toggle = (id: string | number) =>
    setExcluded((ex) => (ex.some((x) => String(x) === String(id)) ? ex.filter((x) => String(x) !== String(id)) : [...ex, id]));

  return (
    <div class="shift-panel" role="dialog">
      <div class="shift-title">
        從「{from.title}」起 {signed(delta)} 分
      </div>
      {typeof result === "string" && <div class="reason">{result}</div>}
      {tooEarly && (
        <div class="too-early">
          「{byId.get(String(tooEarly.cueId))?.title ?? "某個環節"}」會變成 {formatHM(tooEarly.newStart, tz)}，早於現在，無法提早
        </div>
      )}
      <div class="moves">
        {(candidates ?? []).map((id) => {
          const c = byId.get(String(id));
          const m = moves.get(String(id));
          const ticked = !excluded.some((x) => String(x) === String(id));
          return (
            <label class="move" key={String(id)}>
              <input type="checkbox" checked={ticked} onChange={() => toggle(id)} />
              <span class="title">{c?.title}</span>
              <span class="times">{m ? `${formatHM(m.fromStart, tz)} → ${formatHM(m.toStart, tz)}` : "不移動"}</span>
            </label>
          );
        })}
        {candidates !== null && candidates.length === 0 && <div class="reason">沒有會移動的環節</div>}
      </div>
      {conflicts.length > 0 && (
        <div class="conflicts">
          {conflicts.map((c) => (
            <div key={String(c.cueId)}>
              ⚠「{byId.get(String(c.cueId))?.title}」會晚於固定的「{byId.get(String(c.fixedCueId))?.title}」{c.overrunMin} 分
            </div>
          ))}
        </div>
      )}
      <div class="shift-actions">
        <button onClick={close}>取消</button>
        <button
          class="confirm"
          disabled={disabled || typeof result !== "object" || result === null || tooEarly !== null || moves.size === 0}
          onClick={() => void apply(req)}
        >
          確認平移
        </button>
      </div>
    </div>
  );
}

function elapsedText(active: MasterCue, correctedNow: number): string {
  const started = active.actualStart ? Date.parse(active.actualStart) : NaN;
  const elapsed = Number.isFinite(started) ? Math.max(0, Math.floor((correctedNow - started) / 60000)) : 0;
  return `已進行 ${elapsed} 分 / 預計 ${active.durationMin} 分`;
}

type RowProps = { cue: MasterCue; tz: string; conflict: boolean; outOfOrder: boolean; open: () => void };

function CueRow({ cue, tz, conflict, outOfOrder, open }: RowProps) {
  return (
    <div class={`cue ${cue.status.toLowerCase()}`} data-cue={String(cue.id)} onClick={open} role="button">
      <span class="icon" aria-hidden="true">{ICON[cue.status]}</span>
      <span class="time">
        {formatHM(cue.effectiveStart, tz)}
        <OriginalTime start={cue.effectiveStart} offsetMin={cue.offsetMin} tz={tz} />
      </span>
      <span class="title">{cue.title}</span>
      <span class="mark">
        {cue.isFixed && <span title="固定">■ 固定</span>} {conflict && <span title="固定環節衝突">⚠ 衝突</span>}{" "}
        {outOfOrder && <span title="時間早於前一個環節">⚠ 倒序</span>}{" "}
        {STATUS_TEXT[cue.status]}
      </span>
    </div>
  );
}
