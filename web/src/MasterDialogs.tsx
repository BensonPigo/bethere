// The console's dialogs for M2b: a role's check-in and link (US-M-1.7,
// US-M-7), a cue's details with skip, edit and assignment (US-M-6,
// US-M-8.3, US-M-8.7), and adding a cue (US-M-8.1). Every write is off
// while offline or busy (US-X-8.4).
import { useState } from "preact/hooks";
import { CUE_ROLE_TYPES } from "./api";
import type { Assignment, MasterCue, MasterRole, MasterSnapshot, WriteRequest } from "./api";
import { agoText, formatHM, zonedToISO } from "./format";

type Write = (w: WriteRequest) => Promise<boolean>;

const CONFIRM_LINK = "舊連結將立即失效，對方需要新連結。確定嗎？";

export function RolesPanel(props: { snap: MasterSnapshot; now: number; open: (r: MasterRole) => void }) {
  const roles = props.snap.roles.filter((r) => r.type !== "Master");
  if (roles.length === 0) return null;
  return (
    <div class="roles-panel">
      <div class="label">就位狀態</div>
      {roles.map((r) => (
        <button class="role-row" key={String(r.id)} onClick={() => props.open(r)}>
          <span class={r.checkedIn ? "in" : "out"}>{r.checkedIn ? "● 已就位" : CUE_ROLE_TYPES.includes(r.type) ? "○ 未就位" : "　"}</span>
          <span class="name">{r.name}</span>
          <small>{r.hasLink ? agoText(r.lastSeenAt, props.now) : "連結已撤銷"}</small>
        </button>
      ))}
    </div>
  );
}

type RoleMenuProps = {
  role: MasterRole;
  lock: boolean;
  write: Write;
  reissue: (roleId: string | number) => Promise<string | null>;
  close: () => void;
};

export function RoleMenu({ role, lock, write, reissue, close }: RoleMenuProps) {
  const [link, setLink] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const canCheckIn = CUE_ROLE_TYPES.includes(role.type);

  const doReissue = async () => {
    if (!window.confirm(CONFIRM_LINK)) return;
    const token = await reissue(role.id);
    // Shown as text with a copy button, never as a link that could be
    // opened on this device (api.md section 3).
    if (token) setLink(`${location.origin}/#t=${token}`);
  };
  const copy = async () => {
    if (!link) return;
    try {
      await navigator.clipboard.writeText(link);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  };

  return (
    <div class="shift-panel dialog" role="dialog">
      <div class="shift-title">{role.name}</div>
      {link ? (
        <>
          <div>新連結（只顯示這一次，請用 LINE 傳給對方）：</div>
          <input class="link-text" readOnly value={link} onFocus={(e) => (e.currentTarget as HTMLInputElement).select()} />
          <button class="confirm" onClick={() => void copy()}>
            {copied ? "已複製 ✔" : "複製"}
          </button>
        </>
      ) : (
        <div class="menu">
          {canCheckIn && (
            <button disabled={lock} onClick={() => void write({ path: "/api/roles/checkin", roleId: role.id, checkedIn: !role.checkedIn }).then((ok) => ok && close())}>
              {role.checkedIn ? "取消打卡" : "代為打卡"}
            </button>
          )}
          <button disabled={lock} onClick={() => void doReissue()}>
            重發連結
          </button>
          <button
            class="danger"
            disabled={lock || !role.hasLink}
            onClick={() => {
              if (window.confirm(CONFIRM_LINK)) void write({ path: "/api/roles/revoke", roleId: role.id }).then((ok) => ok && close());
            }}
          >
            撤銷連結
          </button>
        </div>
      )}
      <div class="shift-actions">
        <button onClick={close}>關閉</button>
      </div>
    </div>
  );
}

type CueDialogProps = {
  snap: MasterSnapshot;
  cue: MasterCue;
  tz: string;
  lock: boolean;
  write: Write;
  insertAfter: (c: MasterCue) => void;
  close: () => void;
};

export function CueDialog({ snap, cue, tz, lock, write, insertAfter, close }: CueDialogProps) {
  const [title, setTitle] = useState(cue.title);
  const [note, setNote] = useState(cue.notePublic ?? "");
  const pending = cue.status === "Pending";
  const order = snap.outOfOrder.find((o) => String(o.cueId) === String(cue.id));
  const prev = order && snap.cues.find((c) => String(c.id) === String(order.prevCueId));
  const textChanged = title.trim() !== cue.title || note !== (cue.notePublic ?? "");

  return (
    <div class="shift-panel dialog" role="dialog">
      <div class="shift-title">
        {formatHM(cue.effectiveStart, tz)} {cue.title}
      </div>
      {prev && <div class="reason">⚠ 時間早於前一個環節 {formatHM(prev.effectiveStart, tz)}</div>}
      <label class="field">
        標題
        <input value={title} maxLength={100} onInput={(e) => setTitle((e.currentTarget as HTMLInputElement).value)} />
      </label>
      <label class="field">
        公開備註
        <textarea value={note} maxLength={500} onInput={(e) => setNote((e.currentTarget as HTMLTextAreaElement).value)} />
      </label>
      <button
        disabled={lock || !textChanged || title.trim() === ""}
        onClick={() => void write({ path: "/api/cues/edit", cueId: cue.id, title, notePublic: note === "" ? null : note })}
      >
        儲存標題與備註
      </button>
      <RolePicker
        snap={snap}
        initial={cue.roles.map((r) => ({ roleId: r.roleId, roleNote: r.roleNote }))}
        editable={pending}
        lock={lock}
        save={(roles) => write({ path: "/api/cues/roles", cueId: cue.id, roles })}
      />
      <div class="shift-actions">
        {pending && (
          <button class="danger" disabled={lock} onClick={() => void write({ path: "/api/cues/skip", cueId: cue.id }).then((ok) => ok && close())}>
            略過
          </button>
        )}
        <button disabled={lock} onClick={() => insertAfter(cue)}>
          在此之後新增環節
        </button>
        <button onClick={close}>關閉</button>
      </div>
    </div>
  );
}

// The assignment of a cue: only Stylist, Media and StageAV can be picked
// (api.md section 3), each with its own note. Only pending cues change
// (US-M-8.3).
function RolePicker(props: {
  snap: MasterSnapshot;
  initial: Assignment[];
  editable: boolean;
  lock: boolean;
  save?: (roles: Assignment[]) => Promise<boolean>;
  onChange?: (roles: Assignment[]) => void;
}) {
  const [roles, setRoles] = useState<Assignment[]>(props.initial);
  const options = props.snap.roles.filter((r) => CUE_ROLE_TYPES.includes(r.type));
  const update = (next: Assignment[]) => {
    setRoles(next);
    props.onChange?.(next);
  };
  const find = (id: string | number) => roles.find((a) => String(a.roleId) === String(id));
  const changed = JSON.stringify(roles) !== JSON.stringify(props.initial);
  return (
    <div class="role-picker">
      <div class="label">指派的角色{props.editable ? "" : "（環節已開始或結束，不能變更）"}</div>
      {options.map((r) => {
        const a = find(r.id);
        return (
          <div class="pick" key={String(r.id)}>
            <label>
              <input
                type="checkbox"
                checked={!!a}
                disabled={!props.editable}
                onChange={() => update(a ? roles.filter((x) => x !== a) : [...roles, { roleId: r.id, roleNote: null }])}
              />
              {r.name}
            </label>
            {a && (
              <input
                placeholder="角色備註"
                maxLength={500}
                disabled={!props.editable}
                value={a.roleNote ?? ""}
                onInput={(e) => {
                  const v = (e.currentTarget as HTMLInputElement).value;
                  update(roles.map((x) => (x === a ? { ...x, roleNote: v === "" ? null : v } : x)));
                }}
              />
            )}
          </div>
        );
      })}
      {props.save && props.editable && (
        <button disabled={props.lock || !changed} onClick={() => void props.save?.(roles)}>
          儲存指派
        </button>
      )}
    </div>
  );
}

type InsertProps = {
  snap: MasterSnapshot;
  after: MasterCue;
  tz: string;
  lock: boolean;
  write: Write;
  close: () => void;
};

// US-M-8.1: the time is the one it will really start at; no offset.
export function InsertDialog({ snap, after, tz, lock, write, close }: InsertProps) {
  const [time, setTime] = useState(() => formatHM(Date.parse(after.effectiveStart) + after.durationMin * 60000, tz));
  const [duration, setDuration] = useState("10");
  const [title, setTitle] = useState("");
  const [roles, setRoles] = useState<Assignment[]>([]);
  const start = zonedToISO(snap.event.date, time, tz);
  const minutes = Number.parseInt(duration, 10);
  const valid = start !== null && Number.isFinite(minutes) && minutes >= 0 && minutes <= 1440 && title.trim() !== "";

  return (
    <div class="shift-panel dialog" role="dialog">
      <div class="shift-title">在「{after.title}」之後新增環節</div>
      <label class="field">
        開始時間
        <input type="time" value={time} onInput={(e) => setTime((e.currentTarget as HTMLInputElement).value)} />
      </label>
      <label class="field">
        長度（分）
        <input type="number" min={0} max={1440} value={duration} onInput={(e) => setDuration((e.currentTarget as HTMLInputElement).value)} />
      </label>
      <label class="field">
        標題
        <input value={title} maxLength={100} onInput={(e) => setTitle((e.currentTarget as HTMLInputElement).value)} />
      </label>
      <RolePicker snap={snap} initial={[]} editable lock={lock} onChange={setRoles} />
      <div class="shift-actions">
        <button onClick={close}>取消</button>
        <button
          class="confirm"
          disabled={lock || !valid}
          onClick={() =>
            start &&
            void write({
              path: "/api/cues/insert",
              cue: { afterCueId: after.id, start, durationMin: minutes, title, notePublic: null, roles },
            }).then((ok) => ok && close())
          }
        >
          新增
        </button>
      </div>
    </div>
  );
}
