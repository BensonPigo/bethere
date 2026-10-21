// V1 Master console, M1 scope (screen-design.md section 3; US-M-1, US-M-3, US-M-4).
import { useEffect, useRef } from "preact/hooks";
import type { CueStatus, MasterCue, MasterSnapshot, WritePath } from "./api";
import { dateInZone, formatHM } from "./format";

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
  write: (path: WritePath) => void;
};

export function Master({ snap, correctedNow, offline, busy, write }: Props) {
  const tz = snap.event.timezone || "Asia/Taipei";
  const active = snap.cues.find((c) => c.status === "InProgress") ?? null;
  const nextPending = snap.cues.find((c) => c.status === "Pending") ?? null;
  const focusId = (active ?? nextPending)?.id ?? null;

  const listRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const list = listRef.current;
    if (!list || focusId === null) return;
    const row = list.querySelector<HTMLElement>(`[data-cue="${String(focusId)}"]`);
    // Put the current cue a quarter of the way down so the following cues stay visible.
    if (row) list.scrollTo({ top: row.offsetTop - list.clientHeight * 0.25, behavior: "smooth" });
  }, [focusId]);

  const isEventDay = dateInZone(correctedNow, tz) === snap.event.date.slice(0, 10);
  const draft = snap.eventStatus === "Draft";
  const lock = offline || busy;

  let startReason: string | null = null;
  if (offline) startReason = "離線中，無法操作";
  else if (!isEventDay) startReason = "今天不是活動日，無法開始活動";

  return (
    <div class="master">
      <div class="timeline" ref={listRef}>
        {snap.cues.map((c) => (
          <CueRow key={c.id} cue={c} tz={tz} />
        ))}
      </div>
      <div class="panel">
        <div class="panel-head">
          {snap.event.name}・現在 {formatHM(correctedNow, tz)}
        </div>
        {draft ? (
          <div class="current">
            <div class="label">活動尚未開始</div>
            <div class="cue-title">{nextPending ? `第一個環節：${nextPending.title}` : "沒有環節"}</div>
          </div>
        ) : (
          <div class="current">
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
          </div>
        )}

        {draft ? (
          <button class="big start" disabled={lock || !isEventDay} onClick={() => write("/api/event/start")}>
            開始活動
          </button>
        ) : (
          <button
            class="big advance"
            disabled={lock || (!active && !nextPending)}
            onClick={() => write("/api/cues/advance")}
          >
            推進
          </button>
        )}
        {draft && startReason && <div class="reason">{startReason}</div>}
        {!draft && offline && <div class="reason">離線中，無法操作</div>}

        <button class="undo" disabled={lock || draft || !snap.undo.available} onClick={() => write("/api/cues/undo")}>
          <span>復原上一步</span>
          {snap.undo.available && <small>{snap.undo.description}</small>}
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

function CueRow({ cue, tz }: { cue: MasterCue; tz: string }) {
  const off = cue.offsetMin ? `（${cue.offsetMin > 0 ? "+" : ""}${cue.offsetMin}）` : "";
  return (
    <div class={`cue ${cue.status.toLowerCase()}`} data-cue={String(cue.id)}>
      <span class="icon" aria-hidden="true">{ICON[cue.status]}</span>
      <span class="time">{formatHM(cue.effectiveStart, tz)}</span>
      <span class="title">
        {cue.title}
        {off}
      </span>
      <span class="mark">
        {cue.isFixed && <span title="固定">■ 固定</span>} {STATUS_TEXT[cue.status]}
      </span>
    </div>
  );
}
