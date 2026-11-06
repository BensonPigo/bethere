// `/login`: a wedding planner company's member logs in with their email
// (US-CO-1, ADR-0040, test version: no password). It never reads the
// fragment, the session or the offline snapshot; the login link goes into
// the next page's address only.
import { useState } from "preact/hooks";
import { postLogin } from "./company";
import { ThemeToggle } from "./theme";

const LOGIN_TEXT = {
  notMember: "這個 email 不是任何公司的成員",
  unavailable: "系統維護中或連不上，請稍後再試",
} as const;

export function LoginPage() {
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async () => {
    if (busy || email.trim() === "") return;
    setBusy(true);
    setError("");
    const o = await postLogin(email);
    setBusy(false);
    if (o.kind === "ok") {
      location.replace(`/company#t=${o.token}`);
    } else {
      setError(LOGIN_TEXT[o.kind]);
    }
  };

  return (
    <main class="recover">
      <div class="panel-head">
        <h1>婚顧公司登入</h1>
        <ThemeToggle />
      </div>
      <label class="field">
        email
        <input
          type="email"
          autoComplete="email"
          inputMode="email"
          autoCapitalize="off"
          spellcheck={false}
          value={email}
          disabled={busy}
          onInput={(e) => setEmail((e.currentTarget as HTMLInputElement).value)}
          onKeyDown={(e) => e.key === "Enter" && void submit()}
        />
      </label>
      <button class="confirm" disabled={busy || email.trim() === ""} onClick={() => void submit()}>
        {busy ? "登入中…" : "登入"}
      </button>
      {error && (
        <p class="bad" role="alert">
          {error}
        </p>
      )}
      <p class="hint">測試版：只需 email，不檢查密碼</p>
    </main>
  );
}
