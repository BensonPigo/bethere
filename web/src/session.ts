// Polling, clock skew, local snapshot and write handling for one token.
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { fetchSnapshot, postShiftPreview, postWrite, shouldWipeLocalData } from "./api";
import type { Notice, Outcome, ShiftPreview, ShiftRequest, Snapshot, WriteRequest } from "./api";
import { freshNotice } from "./notice";
import { addSkewSample, currentSkew } from "./clock";
import { deleteSnapshot, loadSnapshot, pruneStale, saveSnapshot, snapKey } from "./storage";

export const POLL_MS = 2000;
export const OFFLINE_AFTER_MS = 10000;
export const WRITE_LOCK_MS = 1000;

export type Phase = "loading" | "ready" | "invalid" | "ended";

const REJECTED: Record<string, string> = {
  not_draft: "活動已經開始",
  not_live: "活動尚未開始",
  not_event_day: "今天不是活動日，無法開始活動",
  nothing_to_advance: "沒有可以推進的環節",
  nothing_to_undo: "沒有可以復原的操作",
  undo_stale: "畫面已更新，請重新確認",
  shift_too_early: "有環節會早於現在，無法提早",
  invalid_delta: "平移分鐘數不正確",
  not_pending: "這個環節已經開始或結束，不能變更",
  invalid_position: "不能插在已開始的環節前面",
  invalid_cue: "時間、長度、標題或備註不正確",
  invalid_assignment: "只能指派給新秘、攝影或音控",
  not_checkin_role: "這個角色不需要就位打卡",
  master_role: "總召連結只能用復原碼換發",
};

// How long a change notice stays up (screen-design.md 4.4).
export const NOTICE_MS = 6000;

export function useSession(token: string | null) {
  const [phase, setPhase] = useState<Phase>("loading");
  const [snap, setSnap] = useState<Snapshot | null>(null);
  const [skew, setSkew] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  const [lastOk, setLastOk] = useState<number | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [change, setChange] = useState<Notice | null>(null);

  const openedAt = useRef(Date.now());
  const key = useRef<string | null>(null);
  const samples = useRef<number[]>([]);
  const stopped = useRef(false);
  const inFlight = useRef(false);
  const noticeTimer = useRef<number | undefined>(undefined);
  const writing = useRef(false);
  const changeTimer = useRef<number | undefined>(undefined);
  // The ETag of the last snapshot read by GET, in memory only (api.md 5.7).
  const etag = useRef<string | undefined>(undefined);

  const flash = useCallback((msg: string) => {
    setNotice(msg);
    clearTimeout(noticeTimer.current);
    noticeTimer.current = window.setTimeout(() => setNotice(null), 5000);
  }, []);

  // Apply a response; returns true when it carried a live snapshot.
  const apply = useCallback((o: Outcome, startedAt: number): boolean => {
    if (o.kind === "notModified") {
      // Same content as the snapshot on screen: only the clock (api.md 5.7).
      const received = Date.now();
      samples.current = addSkewSample(samples.current, Date.parse(o.serverTime), (startedAt + received) / 2);
      setSkew(currentSkew(samples.current));
      setLastOk(received);
      return true;
    }
    // A snapshot from anything but a GET has no ETag: the next GET is a
    // full one, so a 304 never brings back an older screen.
    etag.current = o.kind === "ok" ? o.etag : undefined;
    if (shouldWipeLocalData(o)) {
      stopped.current = true;
      if (key.current) deleteSnapshot(localStorage, key.current);
      setSnap(null);
      setPhase(o.kind === "invalid" ? "invalid" : "ended");
      return false;
    }
    const s = o.kind === "ok" || o.kind === "conflict" ? o.snapshot : null;
    if (!s) return false;
    const received = Date.now();
    samples.current = addSkewSample(samples.current, Date.parse(s.serverTime), (startedAt + received) / 2);
    setSkew(currentSkew(samples.current));
    if (key.current) {
      saveSnapshot(localStorage, key.current, s);
      // Each change notice is shown once per device, even across reloads (US-ST-2.4).
      const n = "notice" in s ? freshNotice(s.notice, localStorage, key.current) : null;
      if (n) {
        setChange(n);
        clearTimeout(changeTimer.current);
        changeTimer.current = window.setTimeout(() => setChange(null), NOTICE_MS);
      }
    }
    setSnap(s);
    setPhase("ready");
    setLastOk(received);
    return true;
  }, []);

  const poll = useCallback(async () => {
    if (!token || stopped.current || inFlight.current || key.current === null) return;
    inFlight.current = true;
    const startedAt = Date.now();
    try {
      apply(await fetchSnapshot(token, etag.current), startedAt);
    } finally {
      inFlight.current = false;
    }
  }, [token, apply]);

  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    let timer: number | undefined;
    const onVisible = () => document.visibilityState === "visible" && void poll();
    (async () => {
      let k: string;
      try {
        k = await snapKey(token);
      } catch {
        return; // crypto.subtle unavailable: no polling without a safe local key
      }
      if (cancelled) return;
      key.current = k;
      pruneStale(localStorage, Date.now());
      const cached = loadSnapshot(localStorage, k);
      if (cached) {
        setSnap(cached);
        setPhase("ready");
      }
      void poll();
      timer = window.setInterval(() => void poll(), POLL_MS);
      document.addEventListener("visibilitychange", onVisible);
    })();
    const tick = window.setInterval(() => setNow(Date.now()), 1000);
    return () => {
      cancelled = true;
      clearInterval(timer);
      clearInterval(tick);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [token, poll]);

  // send runs one write and applies its answer; the outcome is returned
  // when it carried a live snapshot, null otherwise.
  const send = useCallback(
    async (w: WriteRequest): Promise<Outcome | null> => {
      if (!token || !snap || writing.current) return null;
      writing.current = true;
      setBusy(true);
      const startedAt = Date.now();
      let done: Outcome | null = null;
      try {
        const o = await postWrite(token, w, snap.eventVersion);
        if (o.kind === "ok") done = apply(o, startedAt) ? o : null;
        else if (o.kind === "conflict") {
          apply(o, startedAt);
          flash("畫面已更新，請重新確認");
        } else if (o.kind === "rejected") flash(REJECTED[o.error] ?? "操作失敗，請稍後再試");
        else if (o.kind === "failure" || o.kind === "preview" || o.kind === "notModified") flash("操作失敗，請稍後再試");
        else apply(o, startedAt);
      } finally {
        writing.current = false;
        // US-M-3.3: keep the buttons disabled for about a second after a press.
        window.setTimeout(() => setBusy(false), WRITE_LOCK_MS);
      }
      return done;
    },
    [token, snap, apply, flash],
  );

  const write = useCallback(async (w: WriteRequest) => (await send(w)) !== null, [send]);

  // reissue returns the new link's token, which exists only in this
  // response and is never stored (US-M-7.1).
  const reissue = useCallback(
    async (roleId: string | number): Promise<string | null> => {
      const o = await send({ path: "/api/roles/reissue", roleId });
      return o?.kind === "ok" && o.token ? o.token : null;
    },
    [send],
  );

  // The shift preview (US-M-2.1): read-only, so it neither locks the buttons
  // nor touches the local snapshot.
  const preview = useCallback(
    async (req: ShiftRequest): Promise<ShiftPreview | string> => {
      if (!token) return "操作失敗，請稍後再試";
      const o = await postShiftPreview(token, req);
      if (o.kind === "preview") return o.preview;
      if (o.kind === "rejected") return REJECTED[o.error] ?? "無法試算，請稍後再試";
      if (shouldWipeLocalData(o)) apply(o, Date.now());
      return "無法試算，請稍後再試";
    },
    [token, apply],
  );

  const offline = now - (lastOk ?? openedAt.current) > OFFLINE_AFTER_MS;
  return { phase, snap, skew, now, offline, lastOk, notice, busy, write, reissue, preview, change };
}
