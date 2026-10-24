// Glance screen: phone in the pocket, a two-second look (screen-design.md
// section 4; ADR-0025; US-X-6), with the check-in at the bottom (US-X-5).
import type { Notice, RoleCue, RoleSnapshot, WriteRequest } from "./api";
import { DEFAULT_TZ, formatHM } from "./format";
import { hasPanel, JobPanel } from "./JobPanels";
import { delayText, isEarlier, noticeText, noticedCues } from "./notice";
import { OriginalTime } from "./OriginalTime";
import { countdownText, inProgressCue, stylistView } from "./roleView";

type Props = {
  snap: RoleSnapshot;
  now: number;
  skew: number;
  change: Notice | null;
  offline: boolean;
  busy: boolean;
  write: (w: WriteRequest) => Promise<boolean>;
  // The Master's preview of this role: shown, never pressed (US-M-14.3).
  readOnly?: boolean;
};

export function Glance({ snap, now, skew, change, offline, busy, write, readOnly }: Props) {
  const view = stylistView(snap, now, skew);
  const tz = snap.eventDay?.timezone || DEFAULT_TZ;
  const marked = noticedCues(change);
  // StageAV notes hold file names: monospace, wrapped, never cut (US-X-6.6).
  const noteClass = snap.role.type === "StageAV" ? "note mono" : "note";
  const lock = offline || busy || !!readOnly;
  // US-VL-1: a venue lead answers the cue it is shown.
  const ack = (c: RoleCue) =>
    snap.role.type === "VenueLead" && (
      <button
        class={`ack ${c.ackedAt ? "done" : ""}`}
        disabled={lock}
        onClick={() => void write({ path: "/api/cues/ack", cueId: c.id, acked: !c.ackedAt })}
      >
        {c.ackedAt ? "已收到 ✔" : "收到"}
      </button>
    );

  let main;
  switch (view.kind) {
    case "preDay":
      main = <PreDayList cues={view.cues} tz={tz} />;
      break;
    case "none":
      // A job's own panel is the screen then; no big empty message over it.
      main = hasPanel(snap) ? null : <div class="msg">沒有指派給你的環節</div>;
      break;
    case "done":
      main = <div class="msg">你的環節都完成了</div>;
      break;
    case "inProgress":
      main = (
        <div class="msg">
          {view.cue.title}進行中
          {ack(view.cue)}
        </div>
      );
      break;
    case "countdown": {
      const later = snap.cues
        .filter((c) => c.status === "Pending" && c.seq > view.target.seq)
        .sort((a, b) => a.seq - b.seq)
        .slice(0, 3);
      main = (
        <>
          <div class={`next-card ${marked.has(String(view.target.id)) ? "flash" : ""}`}>
            <div class="label">下一個與你有關的</div>
            <div class="when">
              {formatHM(view.target.effectiveStart, tz)}
              <OriginalTime start={view.target.effectiveStart} offsetMin={view.target.offsetMin} tz={tz} />
            </div>
            <div class="title">{view.target.title}</div>
            <div class="count">倒數 {countdownText(view.remaining).big}{countdownText(view.remaining).unit}</div>
            {view.target.roleNote && <div class={noteClass}>備註：{view.target.roleNote}</div>}
            {view.target.notePublic && <div class={noteClass}>{view.target.notePublic}</div>}
            {ack(view.target)}
          </div>
          {later.length > 0 && (
            <div class="later">
              <div class="label">稍後</div>
              {later.map((c) => (
                <div class={marked.has(String(c.id)) ? "flash" : ""} key={c.id}>
                  {formatHM(c.effectiveStart, tz)} {c.title}
                  <OriginalTime start={c.effectiveStart} offsetMin={c.offsetMin} tz={tz} />
                </div>
              ))}
            </div>
          )}
        </>
      );
    }
  }
  // The cue in progress, whoever it is assigned to: its title only (US-X-6.4).
  const current = snap.currentTitle ?? inProgressCue(snap.cues)?.title ?? null;
  return (
    <div class="glance">
      {change && <div class={`change-banner ${isEarlier(change) ? "earlier" : "later"}`}>{noticeText(change, tz)}</div>}
      <div class="glance-head">{snap.role.name}</div>
      {view.kind !== "preDay" && snap.eventStatus === "Live" && (
        <div class="current-line">
          目前：{current ?? "準備中"}
          {/* US-X-10.2: the MC keeps an eye on the hosts' side. */}
          <span class={`delay ${delayText(snap.delayMin ?? 0).ok ? "ok" : "late"}`}>・{delayText(snap.delayMin ?? 0).text}</span>
        </div>
      )}
      {/* The job's tool first: it is what these roles came to do (screen-design.md 4.3). */}
      <JobPanel snap={snap} lock={lock} write={write} />
      {main && <div class="glance-main">{main}</div>}
      {snap.role.checksIn !== false && (
        <CheckInBar checkedIn={snap.myCheckIn ?? false} offline={offline} busy={busy || !!readOnly} write={write} />
      )}
      {/* screen-design.md 8; not in a role preview, where the hash is the Master's. */}
      {!readOnly && (
        <a class="print-link" href={`/print${location.hash}`}>
          列印我的流程表
        </a>
      )}
    </div>
  );
}

// US-X-5.1, US-X-5.7: fixed at the bottom; off while offline, never queued.
function CheckInBar(props: { checkedIn: boolean; offline: boolean; busy: boolean; write: Props["write"] }) {
  const { checkedIn, offline, busy, write } = props;
  return (
    <div class="checkin-bar">
      {checkedIn ? (
        <button class="checkin done" disabled={offline || busy} onClick={() => void write({ path: "/api/checkin", checkedIn: false })}>
          已就位 ✔（取消）
        </button>
      ) : (
        <button class="checkin" disabled={offline || busy} onClick={() => void write({ path: "/api/checkin", checkedIn: true })}>
          我已就位
        </button>
      )}
      {offline && <div class="reason">離線中，無法打卡</div>}
    </div>
  );
}

// Before the event day: the role's own cues and times, no countdown (US-X-2.1).
export function PreDayList({ cues, tz }: { cues: RoleCue[]; tz: string }) {
  return (
    <div class="preday">
      <div class="label">你當天的環節</div>
      {cues.map((c) => (
        <div class="preday-row" key={c.id}>
          <span class="time">{formatHM(c.effectiveStart, tz)}</span>
          <span class="title">{c.title}</span>
          {c.roleNote && <span class="note">{c.roleNote}</span>}
        </div>
      ))}
    </div>
  );
}
