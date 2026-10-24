// Adding a role with its link and QR code (US-M-11, US-M-13) and the
// read-only role preview (US-M-14). Every write is off while offline or busy
// (US-X-8.4).
import { useState } from "preact/hooks";
import { isHostSnapshot } from "./api";
import type { Job, NewRole, RoleSnapshot, Snapshot, WriteRequest } from "./api";
import { LinkResult } from "./controls";
import { Dashboard } from "./Dashboard";
import { Glance } from "./Glance";
import { HandsFree } from "./HandsFree";
import type { NewLink } from "./session";

type Write = (w: WriteRequest) => Promise<boolean>;

export const VIEWS: { view: NewRole["view"]; label: string; hint: string }[] = [
  { view: "HandsFree", label: "免手持", hint: "手機放桌上，大字倒數（例如新秘）" },
  { view: "Glance", label: "快速瞄", hint: "手機在口袋，拿出來看下一個環節（例如攝影）" },
  { view: "Dashboard", label: "平安儀表板", hint: "只顯示一切順利或延誤（例如新人）" },
];

// What the role does besides reading its cues (api.md 9.2, ADR-0035).
const JOBS: { job: Job; label: string; hint: string }[] = [
  { job: "general", label: "一般", hint: "只看自己的環節與倒數（例如攝影、主持）" },
  { job: "VenueLead", label: "領班", hint: "對指派的環節按「收到」" },
  { job: "Runner", label: "機動組", hint: "認領任務池裡的任務" },
  { job: "Reception", label: "迎賓", hint: "查賓客桌號、報到、呼叫機動組" },
  { job: "Cashier", label: "禮金", hint: "勾選交接檢核表" },
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
  const [job, setJob] = useState<Job>("general");
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
              <input
                type="radio"
                name="view"
                checked={view === v.view}
                disabled={v.view === "Dashboard" && job !== "general"}
                onChange={() => setView(v.view)}
              />
              {v.label}
              <small>{v.hint}</small>
            </label>
          ))}
          <div class="label">工作</div>
          {JOBS.map((j) => (
            <label class="pick" key={j.job}>
              <input
                type="radio"
                name="job"
                checked={job === j.job}
                disabled={dashboard && j.job !== "general"}
                onChange={() => setJob(j.job)}
              />
              {j.label}
              <small>{j.hint}</small>
            </label>
          ))}
          <label class="pick">
            <input type="checkbox" checked={checksIn && !dashboard} disabled={dashboard} onChange={() => setChecksIn(!checksIn)} />
            需要就位打卡
          </label>
          <button
            class="confirm"
            disabled={lock || name.trim() === ""}
            onClick={() => void addRole({ name, view, checksIn: checksIn && !dashboard, job }).then(setLink)}
          >
            新增並產生連結
          </button>
        </>
      )}
      <div class="shift-actions">
        <button class="secondary" onClick={close}>關閉</button>
      </div>
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
