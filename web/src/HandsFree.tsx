// Hands-free screen: phone on the table, hands busy (screen-design.md
// section 5; ADR-0025; US-ST-1, US-ST-2).
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import type { RoleSnapshot, ShiftNotice } from "./api";
import { DEFAULT_TZ, formatHM } from "./format";
import { noticeText } from "./notice";
import { countdownText, stylistView } from "./roleView";
import { PreDayList } from "./Glance";

type Props = { snap: RoleSnapshot; now: number; skew: number; change: ShiftNotice | null };

export function HandsFree({ snap, now, skew, change }: Props) {
  const view = stylistView(snap, now, skew);
  const wake = useWakeLock();
  const tz = snap.eventDay?.timezone || DEFAULT_TZ;
  // US-ST-2.2: the countdown flashes when the change moved the cue it counts to.
  const flash = change !== null && view.kind === "countdown" && change.changes.some((c) => String(c.cueId) === String(view.target.id));

  let body;
  let level = "calm";
  switch (view.kind) {
    case "preDay":
      body = <PreDayList cues={view.cues} tz={tz} />;
      break;
    case "none":
      body = <div class="msg">沒有指派給你的環節</div>;
      break;
    case "inProgress":
      body = <div class="msg">{view.cue.title}進行中</div>;
      break;
    case "done":
      body = <div class="msg">今日任務已完成</div>;
      break;
    case "countdown": {
      level = view.level;
      const t = countdownText(view.remaining);
      const off = view.target.offsetMin
        ? `（${view.target.offsetMin > 0 ? "+" : ""}${view.target.offsetMin}）`
        : "";
      body = (
        <>
          <div class="top">{view.topLine}</div>
          <div class={`big-num ${view.level === "overdue" ? "overdue-text" : t.big.includes(":") ? "mss" : ""} ${flash ? "flash" : ""}`}>
            {t.big}
            {t.unit && <span class="unit">{t.unit}</span>}
          </div>
          <div class="target">
            距「{view.target.title}」{formatHM(view.target.effectiveStart, tz)}
            {off}
          </div>
          <div class="status-text">{statusText(view.level)}</div>
        </>
      );
    }
  }

  return (
    <div class={`stylist ${level}`}>
      {change && (
        <div class={`change-banner ${change.deltaMin < 0 ? "earlier" : "later"}`}>
          {change.deltaMin < 0 ? "▲ " : "▼ "}
          {noticeText(change, tz)}
        </div>
      )}
      {body}
      {!wake.held && (
        <div class="overlay" onClick={wake.request} role="button">
          {wake.supported ? "點一下，保持螢幕常亮" : "此瀏覽器不支援螢幕常亮，請把手機的自動鎖定調長；點一下關閉此提示"}
        </div>
      )}
    </div>
  );
}

function statusText(level: string): string {
  switch (level) {
    case "calm":
      return "時間充裕";
    case "amber":
      return "快到了";
    case "red":
      return "即將到時";
    default:
      return "已逾時";
  }
}

// US-ST-1.6: request on tap; re-show the overlay whenever the lock is not held.
function useWakeLock() {
  const supported = typeof navigator !== "undefined" && "wakeLock" in navigator;
  const sentinel = useRef<WakeLockSentinel | null>(null);
  const [held, setHeld] = useState(false);
  const [dismissed, setDismissed] = useState(false);

  const request = useCallback(async () => {
    if (!supported) {
      setDismissed(true);
      return;
    }
    try {
      const s = await navigator.wakeLock.request("screen");
      sentinel.current = s;
      setHeld(true);
      s.addEventListener("release", () => {
        if (sentinel.current === s) sentinel.current = null;
        setHeld(false);
      });
    } catch {
      setHeld(false);
    }
  }, [supported]);

  useEffect(() => {
    if (!supported) return;
    void request(); // may succeed without a tap (e.g. Android)
    const onVisible = () => {
      if (document.visibilityState === "visible" && !sentinel.current) void request();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      document.removeEventListener("visibilitychange", onVisible);
      void sentinel.current?.release();
    };
  }, [supported, request]);

  return { supported, held: held || (!supported && dismissed), request };
}
