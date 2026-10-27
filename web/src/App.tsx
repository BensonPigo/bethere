// `/` entry: pick the screen from the role's view in the snapshot
// (tech-stack.md 2.2「路由」; ADR-0025).
import { inMaintenance, isHostSnapshot, isMasterSnapshot } from "./api";
import type { RoleSnapshot } from "./api";
import { Dashboard } from "./Dashboard";
import { isLineBrowser, tokenFromHash } from "./env";
import { DEFAULT_TZ, formatHM } from "./format";
import { Glance } from "./Glance";
import { HandsFree } from "./HandsFree";
import { Master } from "./Master";
import { monthDay } from "./notice";
import { isEventDay } from "./roleView";
import { useSession } from "./session";

const OFFLINE_WARN_MS = 5 * 60 * 1000;

export function App() {
  const token = tokenFromHash(location.hash);
  const s = useSession(token);
  const line = isLineBrowser(navigator.userAgent);

  // product/vision.md V2, US-X-3.1: works fully inside LINE; only a quiet
  // note, never a request to switch browsers.
  const lineBanner = line && <div class="line-note">斷網時可能打不開，可改用 Safari／Chrome 開啟</div>;

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
  const tz = snap.eventDay?.timezone || DEFAULT_TZ;
  const offlineFor = s.now - (s.lastOk ?? s.now);
  const master = isMasterSnapshot(snap);
  // ADR-0020: "not started" is a strip over the screen, never instead of it.
  const notStarted =
    !master &&
    snap.eventStatus === "Draft" &&
    (isEventDay(snap, correctedNow) || !snap.eventDay ? "活動尚未開始" : `活動尚未開始（${monthDay(snap.eventDay.date)}）`);

  const banners = (
    <>
      {lineBanner}
      {s.offline && (
        <div class="offline-banner">
          離線中，資料更新於 {formatHM(snap.serverTime, tz)}
          {s.lastOk !== null && offlineFor > OFFLINE_WARN_MS ? "・倒數可能不準" : ""}
        </div>
      )}
      {inMaintenance(snap) && (
        <div class="maintenance-banner" role="status">
          維護中，其他角色無法連線
        </div>
      )}
      {notStarted && <div class="draft-strip">{notStarted}</div>}
      {s.notice && <div class="notice">{s.notice}</div>}
    </>
  );

  let screen;
  if (master) {
    screen = (
      <Master
        snap={snap}
        correctedNow={correctedNow}
        offline={s.offline}
        busy={s.busy}
        write={s.write}
        reissue={s.reissue}
        preview={s.preview}
        addRole={s.addRole}
        importPreview={s.importPreview}
        rolePreview={s.rolePreview}
        now={s.now}
        skew={s.skew}
      />
    );
  } else if (isHostSnapshot(snap)) {
    screen = <Dashboard snap={snap} />;
  } else {
    const rs = snap as RoleSnapshot;
    // A snapshot saved by an older release has no view: the type's default.
    const view = rs.role.view ?? (rs.role.type === "Stylist" ? "HandsFree" : "Glance");
    screen =
      view === "Glance" ? (
        <Glance snap={rs} now={s.now} skew={s.skew} change={s.change} offline={s.offline} busy={s.busy} write={s.write} />
      ) : view === "HandsFree" ? (
        <HandsFree snap={rs} now={s.now} skew={s.skew} change={s.change} offline={s.offline} write={s.write} />
      ) : null;
  }
  if (!screen) return <Full>{banners}<div class="msg">此連結的畫面尚未開放</div></Full>;
  return (
    <div class="app">
      {banners}
      {screen}
    </div>
  );
}

function Full(props: { children: preact.ComponentChildren }) {
  return <div class="app full">{props.children}</div>;
}
