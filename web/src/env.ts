// Facts about the browsing environment.

// tech-stack.md 2.2「LINE 內建瀏覽器」: the LINE in-app browser carries " Line/" in its user agent.
export function isLineBrowser(ua: string): boolean {
  return ua.includes(" Line/");
}

export function tokenFromHash(hash: string): string | null {
  const t = new URLSearchParams(hash.replace(/^#/, "")).get("t");
  return t && t.length > 0 ? t : null;
}
