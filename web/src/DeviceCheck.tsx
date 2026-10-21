import { useEffect, useRef, useState } from "preact/hooks";
import { detectEnv, envLabel, percentile, tokenFromHash } from "./env";

const POLL_MS = 2000;
const POLL_TIMEOUT_MS = 8000;
const SAMPLE_SIZE = 30;
const SEEN_KEY = "bethere-check:first-seen";

type Ping = { rttMs: number; skewMs: number; version: string };

export function DeviceCheck() {
  const env = detectEnv();
  const token = tokenFromHash(location.hash);
  const [firstSeen] = useState(() => rememberFirstSeen(env));
  const [rtts, setRtts] = useState<number[]>([]);
  const [last, setLast] = useState<Ping | null>(null);
  const [failures, setFailures] = useState(0);
  const [online, setOnline] = useState(navigator.onLine);
  const [sw, setSw] = useState(swStatus());
  const [copied, setCopied] = useState(false);
  const [sent, setSent] = useState<string | null>(null);
  const wake = useWakeLock();
  const inFlight = useRef(false);

  useEffect(() => {
    const tick = async () => {
      if (inFlight.current) return;
      inFlight.current = true;
      try {
        const ping = await pingOnce();
        setLast(ping);
        setRtts((xs) => [...xs, ping.rttMs].slice(-SAMPLE_SIZE));
      } catch {
        setFailures((n) => n + 1);
      } finally {
        inFlight.current = false;
      }
    };
    tick();
    const id = setInterval(tick, POLL_MS);
    const onVisible = () => document.visibilityState === "visible" && tick();
    const onNet = () => setOnline(navigator.onLine);
    document.addEventListener("visibilitychange", onVisible);
    window.addEventListener("online", onNet);
    window.addEventListener("offline", onNet);
    navigator.serviceWorker?.addEventListener("controllerchange", () => setSw(swStatus()));
    navigator.serviceWorker?.ready.then(() => setSw(swStatus()));
    return () => {
      clearInterval(id);
      document.removeEventListener("visibilitychange", onVisible);
      window.removeEventListener("online", onNet);
      window.removeEventListener("offline", onNet);
    };
  }, []);

  const report = {
    env: envLabel(env),
    userAgent: env.userAgent,
    url: location.href.replace(/#t=[^&]+/, "#t=…"),
    hasToken: token !== null,
    tokenPrefix: token?.slice(0, 4) ?? null,
    firstSeen,
    swControlled: sw.controlled,
    wakeLock: wake.status,
    printAvailable: typeof window.print === "function",
    rttMedianMs: percentile(rtts, 50),
    rttP95Ms: percentile(rtts, 95),
    samples: rtts.length,
    failures,
  };

  const copyLink = async () => {
    try {
      await navigator.clipboard.writeText(location.href);
      setCopied(true);
    } catch {
      prompt("請手動複製這個連結", location.href);
    }
  };

  const send = async () => {
    try {
      const res = await fetch("/api/report", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(report),
      });
      setSent(res.ok ? `已送出（${new Date().toLocaleTimeString()}）` : `送出失敗：${res.status}`);
    } catch {
      setSent("送出失敗：連不上伺服器");
    }
  };

  return (
    <main>
      <h1>BeThere 裝置驗證</h1>
      <p class="env">{envLabel(env)}{online ? "" : "・離線中"}</p>

      <section>
        <h2>R1 連結中的 Token</h2>
        <Row label="網址中的 #t=" ok={token !== null}>
          {token ? `有（${token.slice(0, 4)}…，共 ${token.length} 字元）` : "沒有"}
        </Row>
        <Row label="這個環境第一次開啟">{firstSeen}</Row>
        <p class="hint">測法：從 LINE 點連結 →「以 Safari／Chrome 開啟」、加入書籤、加到主畫面，再從那裡打開，看這裡是否仍顯示「有」。</p>
        <button onClick={copyLink}>{copied ? "已複製本頁連結" : "複製本頁連結"}</button>
      </section>

      <section>
        <h2>R2 離線開啟</h2>
        <Row label="Service Worker 支援" ok={sw.supported}>{sw.supported ? "支援" : "不支援"}</Row>
        <Row label="本頁由 Service Worker 控制" ok={sw.controlled}>{sw.controlled ? "是" : "否"}</Row>
        <p class="hint">測法：上面兩項都是「是」之後，開飛航模式，關掉頁面再重新打開。</p>
      </section>

      <section>
        <h2>R3 螢幕常亮</h2>
        <Row label="Wake Lock" ok={wake.status === "啟用中"}>{wake.status}</Row>
        <button onClick={() => (wake.active ? wake.release() : wake.request())} disabled={!wake.supported}>
          {wake.active ? "關閉常亮" : "開啟常亮"}
        </button>
        <p class="hint">測法：開啟後把手機放著不動，超過自動鎖定時間後螢幕應仍亮著；切到別的 App 再回來，看狀態是否需要重新開啟。開啟過一次之後，關掉 App 再打開，看狀態是否顯示「開啟頁面時自動」。</p>
      </section>

      <section>
        <h2>R4 輪詢延遲</h2>
        <Row label="中位數" ok={ok(percentile(rtts, 50), 600)}>{ms(percentile(rtts, 50))}</Row>
        <Row label="95 百分位" ok={ok(percentile(rtts, 95), 600)}>{ms(percentile(rtts, 95))}</Row>
        <Row label="樣本／失敗">{`${rtts.length}／${failures}`}</Row>
        <Row label="時鐘偏差（伺服器－本機）">{last ? `${Math.round(last.skewMs)} ms` : "—"}</Row>
        <Row label="伺服器版本">{last?.version ?? "—"}</Row>
      </section>

      <section>
        <h2>R5 列印</h2>
        <a class="button" href={`/print${location.hash}`}>開啟列印頁</a>
      </section>

      <section>
        <h2>回報</h2>
        <p class="hint">每種環境測完按一次，結果會記在伺服器日誌裡。</p>
        <button onClick={send}>送出這個環境的結果</button>
        {sent && <p class="hint">{sent}</p>}
      </section>
    </main>
  );
}

function Row(props: { label: string; ok?: boolean; children: preact.ComponentChildren }) {
  const mark = props.ok === undefined ? "" : props.ok ? "✔ " : "✘ ";
  return (
    <div class={`row ${props.ok === false ? "bad" : ""}`}>
      <span>{props.label}</span>
      <strong>{mark}{props.children}</strong>
    </div>
  );
}

async function pingOnce(): Promise<Ping> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), POLL_TIMEOUT_MS);
  const start = performance.now();
  try {
    const res = await fetch("/api/ping", { cache: "no-store", signal: ctrl.signal });
    if (!res.ok) throw new Error(String(res.status));
    const body: { serverTime: string; version: string } = await res.json();
    const end = performance.now();
    const rttMs = end - start;
    // Server time is taken mid-flight; compare against the local midpoint.
    const localMid = Date.now() - rttMs / 2;
    return { rttMs, skewMs: Date.parse(body.serverTime) - localMid, version: body.version };
  } finally {
    clearTimeout(timer);
  }
}

const WAKE_KEY = "bethere-check:wake-wanted";

function useWakeLock() {
  const supported = "wakeLock" in navigator;
  const sentinel = useRef<WakeLockSentinel | null>(null);
  const [status, setStatus] = useState(supported ? "未開啟" : "不支援");
  const [wanted, setWanted] = useState(false);

  const request = async (auto = false) => {
    setWanted(true);
    storeWanted(true);
    try {
      sentinel.current = await navigator.wakeLock.request("screen");
      setStatus(auto ? "啟用中（開啟頁面時自動，未經點擊）" : "啟用中");
      sentinel.current.addEventListener("release", () => {
        sentinel.current = null;
        setStatus("已被系統釋放");
      });
    } catch (e) {
      setStatus(`${auto ? "自動開啟失敗" : "無法開啟"}：${(e as Error).name}`);
    }
  };
  const release = async () => {
    setWanted(false);
    storeWanted(false);
    await sentinel.current?.release();
    setStatus("未開啟");
  };

  // Does the browser grant the lock on page load, without a tap? (R3 follow-up)
  useEffect(() => {
    if (supported && loadWanted()) request(true);
  }, []);

  // Browsers drop the lock when the page is hidden; re-acquire on return.
  useEffect(() => {
    const onVisible = () => {
      if (wanted && document.visibilityState === "visible" && !sentinel.current) request();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => document.removeEventListener("visibilitychange", onVisible);
  }, [wanted]);

  return { supported, status, active: wanted && sentinel.current !== null, request, release };
}

function storeWanted(on: boolean) {
  try {
    on ? localStorage.setItem(WAKE_KEY, "1") : localStorage.removeItem(WAKE_KEY);
  } catch {}
}

function loadWanted(): boolean {
  try {
    return localStorage.getItem(WAKE_KEY) === "1";
  } catch {
    return false;
  }
}

function swStatus() {
  return {
    supported: "serviceWorker" in navigator,
    controlled: !!navigator.serviceWorker?.controller,
  };
}

function rememberFirstSeen(env: ReturnType<typeof detectEnv>): string {
  const key = `${SEEN_KEY}:${env.browser}`;
  try {
    const existing = localStorage.getItem(key);
    if (existing) return existing;
    const now = new Date().toLocaleString();
    localStorage.setItem(key, now);
    return `${now}（本次）`;
  } catch {
    return "無法使用本機儲存";
  }
}

const ms = (v: number | null) => (v === null ? "—" : `${Math.round(v)} ms`);
const ok = (v: number | null, limit: number) => (v === null ? undefined : v <= limit);
