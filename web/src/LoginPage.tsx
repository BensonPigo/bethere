// `/login`: a wedding planner company's member asks for a login mail
// (US-CO-1.1–1.3, ADR-0040), and `/login#t=<login mail's link>` trades the
// mail's link for a login link (US-CO-1.10). The mail's link leaves the
// address at once and lives only in this page's state; it is used only when
// the member presses 登入, since mail scanners open links in mail on their
// own. Nothing goes into storage (US-CO-1.6).
import { useEffect, useState } from "preact/hooks";
import { postConfirm, postLogin } from "./company";
import { tokenFromHash } from "./env";
import { ThemeToggle } from "./theme";

const UNAVAILABLE_TEXT = "系統維護中或連不上，請稍後再試";
const LOGIN_TEXT = {
  sent: "如果這是成員的 email，登入信已經寄出，15 分鐘內有效。沒收到請看垃圾信件匣，過一陣子再試",
  invalidEmail: "email 格式不正確",
  mailUnavailable: "登入信暫時寄不出去，請聯絡客服",
  unavailable: UNAVAILABLE_TEXT,
} as const;
const DENIED_TEXT = "這個登入連結已失效或用過了，請重新寄一封";

function Head(props: { title: string }) {
  return (
    <div class="panel-head">
      <h1>{props.title}</h1>
      <ThemeToggle />
    </div>
  );
}

// readMailToken takes the login mail's link from the address and clears it.
function readMailToken(): string | null {
  const t = tokenFromHash(location.hash);
  if (t) history.replaceState(null, "", "/login");
  return t;
}

export function LoginPage() {
  const [mailToken, setMailToken] = useState(readMailToken);
  // The mail's link opened in a tab already on /login only changes the
  // fragment; the page reads it then too.
  useEffect(() => {
    const read = () => {
      const t = readMailToken();
      if (t) setMailToken(t);
    };
    addEventListener("hashchange", read);
    return () => removeEventListener("hashchange", read);
  }, []);
  return mailToken ? <Confirm key={mailToken} mailToken={mailToken} /> : <AskMail />;
}

// AskMail is the email form (US-CO-1.1–1.3).
function AskMail() {
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<{ text: string; bad: boolean } | null>(null);

  const submit = async () => {
    if (busy || email.trim() === "") return;
    setBusy(true);
    setNote(null);
    const o = await postLogin(email);
    setBusy(false);
    setNote({ text: LOGIN_TEXT[o.kind], bad: o.kind !== "sent" });
  };

  return (
    <main class="recover">
      <Head title="婚顧公司登入" />
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
        {busy ? "寄送中…" : "寄登入信"}
      </button>
      {note && (
        <p class={note.bad ? "bad" : "hint"} role={note.bad ? "alert" : "status"}>
          {note.text}
        </p>
      )}
    </main>
  );
}

// Confirm trades the login mail's link once 登入 is pressed (US-CO-1.10).
function Confirm(props: { mailToken: string }) {
  const [busy, setBusy] = useState(false);
  const [state, setState] = useState<"ready" | "denied" | "unavailable">("ready");

  const confirm = async () => {
    if (busy) return;
    setBusy(true);
    const o = await postConfirm(props.mailToken);
    setBusy(false);
    if (o.kind === "ok") location.replace(`/company#t=${o.token}`);
    else setState(o.kind);
  };

  if (state === "denied") {
    return (
      <main class="recover">
        <Head title="婚顧公司登入" />
        <p class="bad" role="alert">
          {DENIED_TEXT}
        </p>
        <a href="/login">重新寄一封</a>
      </main>
    );
  }
  return (
    <main class="recover">
      <Head title="婚顧公司登入" />
      <p class="hint">按下後進入公司頁面</p>
      <button class="confirm" disabled={busy} onClick={() => void confirm()}>
        {busy ? "登入中…" : "登入"}
      </button>
      {state === "unavailable" && (
        <p class="bad" role="alert">
          {UNAVAILABLE_TEXT}
        </p>
      )}
    </main>
  );
}
