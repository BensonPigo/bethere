// `/recover`: the backup operator replaces the Master's link with the
// recovery code (US-O-3; access-control.md section 2). It never reads the
// fragment, the session or the offline snapshot, and keeps nothing.
import { useState } from "preact/hooks";
import { LinkResult } from "./controls";
import { postRecover } from "./recoverCode";
import { ThemeToggle } from "./theme";

const RECOVER_TEXT = {
  wrong: "復原碼不正確",
  ended: "活動已結束，不能換發",
  failure: "連不上伺服器，請再試一次（舊的總召連結可能已經失效，再換發一次即可）",
} as const;

export function RecoverPage() {
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [token, setToken] = useState("");

  const submit = async () => {
    if (busy || code.trim() === "") return;
    setBusy(true);
    setError("");
    const o = await postRecover(code);
    setBusy(false);
    if (o.kind === "ok") {
      setCode("");
      setToken(o.token);
    } else {
      setError(RECOVER_TEXT[o.kind]);
    }
  };

  return (
    <main class="recover">
      <div class="panel-head">
        <h1>換發總召連結</h1>
        <ThemeToggle />
      </div>
      {token ? (
        <>
          <LinkResult link={{ token, qr: null, roleId: null }} />
          <p>舊的總召連結已經失效。</p>
          <p class="hint">若是外流，請總召到角色清單檢查並重發可疑的連結。</p>
        </>
      ) : (
        <>
          <p>總召的連結弄丟或外流時，輸入紙本上的復原碼，換一條新的總召連結。</p>
          {/* No form and no name: the code never goes into a URL. */}
          <input
            class="recovery-code"
            type="text"
            aria-label="復原碼"
            placeholder="例如：ABCD-EFGH-…"
            autoComplete="off"
            autoCapitalize="characters"
            autoCorrect="off"
            spellcheck={false}
            value={code}
            disabled={busy}
            onInput={(e) => setCode((e.currentTarget as HTMLInputElement).value)}
            onKeyDown={(e) => e.key === "Enter" && void submit()}
          />
          <button class="confirm" disabled={busy || code.trim() === ""} onClick={() => void submit()}>
            {busy ? "換發中…" : "換發總召連結"}
          </button>
          {error && (
            <p class="bad" role="alert">
              {error}
            </p>
          )}
        </>
      )}
    </main>
  );
}
