// Local snapshot (sync-and-offline.md section 6, tech-stack.md T20, api.md A16).
import type { Snapshot } from "./api";

const PREFIX = "snap:";
const MAX_LIVE_AGE_MS = 2 * 24 * 3600 * 1000;

// A16: key = "snap:" + hex SHA-256("bethere-local:" + token). Never the plain token.
export async function snapKey(token: string): Promise<string> {
  const data = new TextEncoder().encode("bethere-local:" + token);
  const digest = await crypto.subtle.digest("SHA-256", data);
  const hex = Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("");
  return PREFIX + hex;
}

// Stale Live snapshots (older than 2 days by serverTime) are dropped on open; Draft ones stay.
export function isStaleLive(snap: { eventStatus?: unknown; serverTime?: unknown }, nowMs: number): boolean {
  if (snap.eventStatus !== "Live" || typeof snap.serverTime !== "string") return false;
  const t = Date.parse(snap.serverTime);
  return Number.isFinite(t) && nowMs - t > MAX_LIVE_AGE_MS;
}

export function pruneStale(store: Storage, nowMs: number): void {
  try {
    const stale: string[] = [];
    for (let i = 0; i < store.length; i++) {
      const k = store.key(i);
      if (!k || !k.startsWith(PREFIX)) continue;
      try {
        const snap = JSON.parse(store.getItem(k) ?? "null");
        if (snap && isStaleLive(snap, nowMs)) stale.push(k);
      } catch {
        stale.push(k); // unreadable entry
      }
    }
    stale.forEach((k) => store.removeItem(k));
  } catch {
    /* storage unavailable */
  }
}

export function loadSnapshot(store: Storage, key: string): Snapshot | null {
  try {
    const raw = store.getItem(key);
    if (!raw) return null;
    const s = JSON.parse(raw);
    return s && typeof s.eventVersion === "number" && s.role && Array.isArray(s.cues) ? (s as Snapshot) : null;
  } catch {
    return null;
  }
}

export function saveSnapshot(store: Storage, key: string, snap: Snapshot): void {
  try {
    store.setItem(key, JSON.stringify(snap));
  } catch {
    /* quota or private mode: run without a local copy */
  }
}

export function deleteSnapshot(store: Storage, key: string): void {
  try {
    store.removeItem(key);
  } catch {
    /* ignore */
  }
}
