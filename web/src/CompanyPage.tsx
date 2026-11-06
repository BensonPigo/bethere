// `/company`: a member's company page (US-CO-1–4; screen-design.md
// section 10). The login link is read from the fragment and kept only in
// the address and this page's state, never in storage (US-CO-1.6). A new
// event's Master link and recovery code are shown once and forgotten
// when the page is left (US-CO-3.3).
import { useCallback, useEffect, useState } from "preact/hooks";
import { CopyBox } from "./controls";
import {
  COMPANY_ERRORS,
  getCompany,
  postAddMember,
  postCompanyEvent,
  postLogout,
  postOpenConsole,
  postRemoveMember,
  rememberConsole,
} from "./company";
import type { Company, CompanyOutcome } from "./company";
import { dateRange } from "./createEvent";
import { tokenFromHash } from "./env";
import { snapKey } from "./storage";
import { ThemeToggle } from "./theme";

const STATUS_TEXT: Record<string, string> = { Draft: "準備中", Live: "進行中", Ended: "已結束" };
const UNAVAILABLE_TEXT = "系統維護中或連不上，請稍後再試";

type Created = { masterToken: string; recoveryCode: string };

function Denied() {
  return (
    <main class="recover">
      <div class="panel-head">
        <h1>婚顧公司</h1>
        <ThemeToggle />
      </div>
      <p class="bad" role="alert">
        請重新登入
      </p>
      <a href="/login">前往登入頁</a>
    </main>
  );
}

function LoggedOut(props: { failed: boolean }) {
  return (
    <main class="recover">
      <h1>已登出</h1>
      {props.failed && (
        <p class="bad" role="alert">
          這台裝置已登出；伺服器端的登入連結要到 30 天後才失效，懷疑外流請同事把你移除再加回
        </p>
      )}
      <a href="/login">前往登入頁</a>
    </main>
  );
}

export function CompanyPage() {
  const [token, setToken] = useState(() => tokenFromHash(location.hash));
  const [company, setCompany] = useState<Company | null>(null);
  const [denied, setDenied] = useState(token === null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<Created | null>(null);
  const [loggedOut, setLoggedOut] = useState<null | { failed: boolean }>(null);
  const [name, setName] = useState("");
  const [date, setDate] = useState("");
  const [email, setEmail] = useState("");
  const range = dateRange(new Date());

  // apply shows an answer that carries the page; a refusal never logs
  // out on its own, an unavailable server keeps what is shown.
  const apply = useCallback((o: CompanyOutcome): boolean => {
    if (o.kind === "ok") {
      setCompany(o.company);
      return true;
    }
    if (o.kind === "denied") setDenied(true);
    else setError(o.kind === "error" ? (COMPANY_ERRORS[o.code] ?? UNAVAILABLE_TEXT) : UNAVAILABLE_TEXT);
    return false;
  }, []);

  useEffect(() => {
    if (token) void getCompany(token).then(apply);
    // A page brought back from the back-forward cache never shows the
    // new event's link and code again.
    const forget = (e: PageTransitionEvent) => e.persisted && setCreated(null);
    addEventListener("pageshow", forget);
    return () => removeEventListener("pageshow", forget);
  }, [token, apply]);

  if (loggedOut) return <LoggedOut failed={loggedOut.failed} />;
  if (denied || !token) return <Denied />;

  const run = async (fn: () => Promise<void>) => {
    if (busy) return;
    setBusy(true);
    setError("");
    await fn();
    setBusy(false);
  };

  const logout = () =>
    run(async () => {
      // The link leaves the address and this page first (screen-design.md section 10).
      const t = token;
      history.replaceState(null, "", "/company");
      setToken(null);
      setLoggedOut({ failed: false });
      if (await postLogout(t)) location.replace("/login");
      else setLoggedOut({ failed: true });
    });

  const createEvent = () =>
    run(async () => {
      const o = await postCompanyEvent(token, name, date);
      if (o.kind === "ok") {
        setName("");
        setDate("");
        setCreated({ masterToken: o.masterToken, recoveryCode: o.recoveryCode });
        void getCompany(token).then(apply);
      } else if (o.kind === "denied") setDenied(true);
      else setError(o.kind === "error" ? (COMPANY_ERRORS[o.code] ?? UNAVAILABLE_TEXT) : UNAVAILABLE_TEXT);
    });

  const openConsole = (eventId: number) =>
    run(async () => {
      const o = await postOpenConsole(token, eventId);
      if (o.kind === "ok") {
        rememberConsole(localStorage, eventId, await snapKey(o.token));
        // assign, not replace: Back returns to this page (US-CO-4.1).
        location.assign(`/#t=${o.token}`);
      } else if (o.kind === "denied") setDenied(true);
      else setError(o.kind === "error" ? (COMPANY_ERRORS[o.code] ?? UNAVAILABLE_TEXT) : UNAVAILABLE_TEXT);
    });

  const addMember = () =>
    run(async () => {
      if (apply(await postAddMember(token, email))) setEmail("");
    });

  const removeMember = (id: number, who: string) =>
    window.confirm(`移除 ${who}？他的登入與開著的主控台會立刻失效。`) &&
    run(async () => {
      apply(await postRemoveMember(token, id));
    });

  const head = (
    <div class="panel-head">
      <h1>{company?.name ?? "婚顧公司"}</h1>
      <ThemeToggle />
      <button class="secondary" disabled={busy} onClick={() => void logout()}>
        登出
      </button>
    </div>
  );

  if (created) {
    return (
      <main class="recover">
        {head}
        <h2>活動已建立</h2>
        <p class="bad">下面兩樣只顯示這一次，請分開保存。</p>
        <CopyBox label="總召連結（交給當天的總召）" value={`${location.origin}/#t=${created.masterToken}`} />
        <CopyBox label="復原碼（交給總召以外、連結弄丟時能幫忙換發的人）" value={created.recoveryCode} />
        <button class="confirm" onClick={() => setCreated(null)}>
          回到公司頁面
        </button>
      </main>
    );
  }

  return (
    <main class="recover">
      {head}
      {error && (
        <p class="bad" role="alert">
          {error}
        </p>
      )}
      {!company ? (
        <p class="hint">讀取中…</p>
      ) : (
        <>
          <h2>活動（{company.events.length}）</h2>
          <label class="field">
            活動名稱
            <input type="text" autoComplete="off" value={name} disabled={busy}
              onInput={(e) => setName((e.currentTarget as HTMLInputElement).value)} />
          </label>
          <label class="field">
            活動日期
            <input type="date" min={range.min} max={range.max} value={date} disabled={busy}
              onInput={(e) => setDate((e.currentTarget as HTMLInputElement).value)}
              onChange={(e) => setDate((e.currentTarget as HTMLInputElement).value)} />
          </label>
          <button class="confirm" disabled={busy || name.trim() === "" || date === ""} onClick={() => void createEvent()}>
            新增活動
          </button>
          <ul class="admin-list">
            {company.events.map((e) => (
              <li key={e.id}>
                {e.name}・{e.date}・{STATUS_TEXT[e.status] ?? e.status}
                {e.status !== "Ended" && (
                  <button class="confirm" disabled={busy} onClick={() => void openConsole(e.id)}>
                    開啟主控台
                  </button>
                )}
              </li>
            ))}
          </ul>
          <h2>成員（{company.members.length}）</h2>
          <label class="field">
            同事的 email
            <input type="email" autoComplete="off" inputMode="email" autoCapitalize="off" spellcheck={false}
              value={email} disabled={busy} onInput={(e) => setEmail((e.currentTarget as HTMLInputElement).value)} />
          </label>
          <button class="confirm" disabled={busy || email.trim() === ""} onClick={() => void addMember()}>
            加入
          </button>
          <ul class="admin-list">
            {company.members.map((m) => (
              <li key={m.id}>
                {m.email}
                {m.id === company.me ? (
                  "（你）"
                ) : (
                  <button class="secondary" disabled={busy} onClick={() => removeMember(m.id, m.email)}>
                    移除
                  </button>
                )}
              </li>
            ))}
          </ul>
          <p class="hint">活動當天的總召請用總召連結；這裡的主控台用於婚前準備。</p>
        </>
      )}
    </main>
  );
}
