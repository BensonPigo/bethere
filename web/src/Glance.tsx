// Glance screen: phone in the pocket, a two-second look (screen-design.md
// section 4; ADR-0025). Check-in comes in M2b.
import type { RoleCue, RoleSnapshot, ShiftNotice } from "./api";
import { DEFAULT_TZ, formatHM } from "./format";
import { noticeText } from "./notice";
import { OriginalTime } from "./OriginalTime";
import { countdownText, inProgressCue, stylistView } from "./roleView";

type Props = { snap: RoleSnapshot; now: number; skew: number; change: ShiftNotice | null };

export function Glance({ snap, now, skew, change }: Props) {
  const view = stylistView(snap, now, skew);
  const tz = snap.eventDay?.timezone || DEFAULT_TZ;
  const moved = new Set((change?.changes ?? []).map((c) => String(c.cueId)));

  let main;
  switch (view.kind) {
    case "preDay":
      main = <PreDayList cues={view.cues} tz={tz} />;
      break;
    case "none":
      main = <div class="msg">沒有指派給你的環節</div>;
      break;
    case "done":
      main = <div class="msg">今日任務已完成</div>;
      break;
    case "inProgress":
      main = <div class="msg">{view.cue.title}進行中</div>;
      break;
    case "countdown": {
      const later = snap.cues
        .filter((c) => c.status === "Pending" && c.seq > view.target.seq)
        .sort((a, b) => a.seq - b.seq)
        .slice(0, 3);
      main = (
        <>
          <div class={`next-card ${moved.has(String(view.target.id)) ? "flash" : ""}`}>
            <div class="label">下一個與你有關的</div>
            <div class="when">
              {formatHM(view.target.effectiveStart, tz)}
              <OriginalTime start={view.target.effectiveStart} offsetMin={view.target.offsetMin} tz={tz} />
            </div>
            <div class="title">{view.target.title}</div>
            <div class="count">倒數 {countdownText(view.remaining).big}{countdownText(view.remaining).unit}</div>
            {view.target.roleNote && <div class="note">備註：{view.target.roleNote}</div>}
          </div>
          {later.length > 0 && (
            <div class="later">
              <div class="label">稍後</div>
              {later.map((c) => (
                <div class={moved.has(String(c.id)) ? "flash" : ""} key={c.id}>
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
  const active = inProgressCue(snap.cues);
  return (
    <div class="glance">
      {change && <div class={`change-banner ${change.deltaMin < 0 ? "earlier" : "later"}`}>{noticeText(change, tz)}</div>}
      <div class="glance-head">{snap.role.name}</div>
      {active && view.kind === "countdown" && <div class="current-line">目前：{active.title}</div>}
      {main}
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
