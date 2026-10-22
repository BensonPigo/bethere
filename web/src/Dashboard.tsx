// The hosts' dashboard (V3; US-H-1): one state and the current cue's
// title. No buttons, no vibration, no sound.
import type { HostSnapshot } from "./api";
import { delayText } from "./notice";

export function Dashboard({ snap }: { snap: HostSnapshot }) {
  const d = delayText(snap.delayMin);
  return (
    <div class={`dashboard ${d.ok ? "ok" : "late"}`}>
      <div class="state">{snap.eventStatus === "Draft" ? "活動尚未開始" : d.text}</div>
      {snap.eventStatus === "Live" && <div class="current">{snap.currentTitle ? `目前：${snap.currentTitle}` : "準備下一個環節"}</div>}
    </div>
  );
}
