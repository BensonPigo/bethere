// `/` entry: pick the screen from the snapshot's role type (tech-stack.md T16).
import { isMasterSnapshot } from "./api";
import type { RoleSnapshot } from "./api";
import { isLineBrowser, tokenFromHash } from "./env";
import { DEFAULT_TZ, formatHM } from "./format";
import { Master } from "./Master";
import { useSession } from "./session";
import { Stylist } from "./Stylist";

const OFFLINE_WARN_MS = 5 * 60 * 1000;

export function App() {
  const token = tokenFromHash(location.hash);
  const s = useSession(token);
  const line = isLineBrowser(navigator.userAgent);

  // product-vision.md V2: works fully inside LINE; only a quiet note, never
  // a request to switch browsers.
  const lineBanner = line && <div class="line-note">在 LINE 中開啟時，沒有網路可能打不開本頁</div>;

  if (!token) return <Full>{lineBanner}<div class="msg">找不到連結資訊，請使用總召給你的完整連結開啟</div></Full>;
  if (s.phase === "invalid") return <Full>{lineBanner}<div class="msg">連結已失效，請向總召索取新連結</div></Full>;
  if (s.phase === "ended") return <Full>{lineBanner}<div class="msg">活動已結束，感謝你的協助</div></Full>;

  const snap = s.snap;
  if (!snap) {
    return (
      <Full>
        {lineBanner}
        <div class="msg">{s.offline ? "無法連線，請檢查網路" : "載入中…"}</div>
      </Full>
    );
  }

  const correctedNow = s.now + s.skew;
  const tz = isMasterSnapshot(snap) ? snap.event.timezone || DEFAULT_TZ : DEFAULT_TZ;
  const offlineFor = s.now - (s.lastOk ?? s.now);

  const banners = (
    <>
      {lineBanner}
      {s.offline && (
        <div class="offline-banner">
          離線中，資料更新於 {formatHM(snap.serverTime, tz)}
          {s.lastOk !== null && offlineFor > OFFLINE_WARN_MS ? "・倒數可能不準" : ""}
        </div>
      )}
      {s.notice && <div class="notice">{s.notice}</div>}
    </>
  );

  if (isMasterSnapshot(snap)) {
    return (
      <div class="app">
        {banners}
        <Master snap={snap} correctedNow={correctedNow} offline={s.offline} busy={s.busy} write={s.write} />
      </div>
    );
  }
  if (snap.role.type === "Stylist") {
    return (
      <div class="app">
        {banners}
        <Stylist snap={snap as RoleSnapshot} now={s.now} skew={s.skew} />
      </div>
    );
  }
  return <Full>{banners}<div class="msg">此連結的畫面尚未開放</div></Full>;
}

function Full(props: { children: preact.ComponentChildren }) {
  return <div class="app full">{props.children}</div>;
}
