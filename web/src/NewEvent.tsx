// `/new`: someone with an invite code creates their own event (US-M-17;
// ADR-0036). It never reads the fragment, the session or the offline
// snapshot; the Master link and recovery code live only in this page's
// state until it is left.
import { useEffect, useState } from "preact/hooks";
import { CopyBox } from "./controls";
import { dateRange, postCreateEvent } from "./createEvent";
import { SupportNote } from "./support";
import { ThemeToggle } from "./theme";

const CREATE_TEXT = {
  wrong: "邀請碼不正確或已失效",
  invalid: "活動名稱或日期不正確",
  maintenance: "系統維護中，請稍後再試",
  failure: "連不上伺服器，請再按一次；若顯示邀請碼已失效，表示剛才可能已經建立，請聯絡給你邀請碼的人",
} as const;

type Created = { token: string; recoveryCode: string };

export function NewEventPage() {
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [date, setDate] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [created, setCreated] = useState<Created | null>(null);
  const range = dateRange(new Date());
  const ready = code.trim() !== "" && name.trim() !== "" && date !== "";

  // A page brought back from the back-forward cache (Back, then Forward)
  // never shows the link and code again (US-M-17.8).
  useEffect(() => {
    const forget = (e: PageTransitionEvent) => e.persisted && setCreated(null);
    addEventListener("pageshow", forget);
    return () => removeEventListener("pageshow", forget);
  }, []);

  const submit = async () => {
    if (busy || !ready) return;
    setBusy(true);
    setError("");
    const o = await postCreateEvent(code, name, date);
    setBusy(false);
    if (o.kind === "ok") {
      setCode("");
      setCreated({ token: o.token, recoveryCode: o.recoveryCode });
    } else {
      setError(CREATE_TEXT[o.kind]);
    }
  };

  if (created) {
    // replace, not assign: Back never brings this page, and the recovery
    // code on it, back (US-M-17.8).
    const enter = () => location.replace(`/#t=${created.token}`);
    return (
      <main class="recover">
        <div class="panel-head">
          <h1>活動已建立</h1>
          <ThemeToggle />
        </div>
        <p class="bad">下面兩樣只顯示這一次，請分開保存。</p>
        <CopyBox label="總召連結（打開就是總召畫面，請存在自己手機）" value={`${location.origin}/#t=${created.token}`} />
        <CopyBox label="復原碼（交給總召以外、連結弄丟時能幫忙換發的人，例如抄在紙上）" value={created.recoveryCode} />
        <p class="hint">邀請碼已用掉，不必保存。</p>
        <button class="confirm" onClick={enter}>
          進入總召畫面
        </button>
      </main>
    );
  }

  return (
    <main class="recover">
      <div class="panel-head">
        <h1>建立活動</h1>
        <ThemeToggle />
      </div>
      <p>輸入收到的邀請碼，建立自己的活動。一組邀請碼只能建立一場。</p>
      {/* No form and no names: nothing typed here goes into a URL or autofill. */}
      <label class="field">
        邀請碼
        <input
          class="recovery-code"
          type="text"
          placeholder="例如：ABCD-EFGH-…"
          autoComplete="off"
          autoCapitalize="characters"
          autoCorrect="off"
          spellcheck={false}
          value={code}
          disabled={busy}
          onInput={(e) => setCode((e.currentTarget as HTMLInputElement).value)}
        />
      </label>
      <label class="field">
        活動名稱
        <input
          type="text"
          placeholder="例如：小明與小華的婚禮"
          autoComplete="off"
          value={name}
          disabled={busy}
          onInput={(e) => setName((e.currentTarget as HTMLInputElement).value)}
        />
      </label>
      <label class="field">
        活動日期
        <input
          type="date"
          min={range.min}
          max={range.max}
          value={date}
          disabled={busy}
          // Some Safari date pickers fire only change.
          onInput={(e) => setDate((e.currentTarget as HTMLInputElement).value)}
          onChange={(e) => setDate((e.currentTarget as HTMLInputElement).value)}
        />
      </label>
      <button class="confirm" disabled={busy || !ready} onClick={() => void submit()}>
        {busy ? "建立中…" : "建立活動"}
      </button>
      {error && (
        <p class="bad" role="alert">
          {error}
        </p>
      )}
      <SupportNote />
    </main>
  );
}
