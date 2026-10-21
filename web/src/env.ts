// Facts about the browsing environment, gathered for the R1–R5 device checks.

export type Env = {
  browser: "line" | "standalone" | "browser";
  os: "ios" | "android" | "other";
  userAgent: string;
};

export function detectEnv(): Env {
  const ua = navigator.userAgent;
  const standalone =
    window.matchMedia("(display-mode: standalone)").matches ||
    (navigator as Navigator & { standalone?: boolean }).standalone === true;
  return {
    browser: / Line\//i.test(ua) ? "line" : standalone ? "standalone" : "browser",
    os: /iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1)
      ? "ios"
      : /Android/.test(ua) ? "android" : "other",
    userAgent: ua,
  };
}

export function tokenFromHash(hash: string): string | null {
  const t = new URLSearchParams(hash.replace(/^#/, "")).get("t");
  return t && t.length > 0 ? t : null;
}

export function percentile(values: number[], p: number): number | null {
  if (values.length === 0) return null;
  const sorted = [...values].sort((a, b) => a - b);
  const i = Math.min(sorted.length - 1, Math.ceil((p / 100) * sorted.length) - 1);
  return sorted[Math.max(0, i)];
}

export function envLabel(env: Env): string {
  const os = { ios: "iOS", android: "Android", other: "其他" }[env.os];
  const browser = { line: "LINE 內建瀏覽器", standalone: "主畫面模式", browser: "一般瀏覽器" }[env.browser];
  return `${os}・${browser}`;
}
