// `/admin`: the developer's page (US-O-9, US-O-10; ADR-0038). The admin
// link's token is read from the fragment and kept only in this page's
// state: never in storage, the session or the offline snapshot. A new
// code is shown once and forgotten when the page is left.
import { useCallback, useEffect, useState } from "preact/hooks";
import { CopyBox } from "./controls";
import { getOverview, postInvite, postOrganization } from "./admin";
import type { OverviewOutcome } from "./admin";
import { tokenFromHash } from "./env";
import { DEFAULT_TZ, dateInZone } from "./format";
import { ThemeToggle } from "./theme";

const ADMIN_TEXT = {
  denied: "無法進入後台",
  maintenance: "系統維護中，請稍後再試",
  failure: "連不上伺服器，請再試一次",
  tooMany: "今天發的邀請碼已達上限，明天再發",
  invalidName: "公司名稱不正確",
  invalidEmail: "email 格式不正確",
  taken: "這個 email 已經是成員",
  tooManyOrgs: "今天建立的公司已達上限，明天再建",
} as const;

const STATUS_TEXT: Record<string, string> = { Draft: "準備中", Live: "進行中", Ended: "已結束" };

const day = (iso: string) => dateInZone(Date.parse(iso), DEFAULT_TZ);

export function AdminPage() {
  const [token] = useState(() => tokenFromHash(location.hash));
  const [overview, setOverview] = useState<OverviewOutcome | null>(null);
  const [code, setCode] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [orgName, setOrgName] = useState("");
  const [orgEmail, setOrgEmail] = useState("");

  const load = useCallback(async () => {
    if (token) setOverview(await getOverview(token));
  }, [token]);

  useEffect(() => {
    void load();
    // A page brought back from the back-forward cache never shows the code again.
    const forget = (e: PageTransitionEvent) => e.persisted && setCode(null);
    addEventListener("pageshow", forget);
    return () => removeEventListener("pageshow", forget);
  }, [load]);

  const issue = async () => {
    if (busy || !token) return;
    setBusy(true);
    setError("");
    const o = await postInvite(token);
    setBusy(false);
    if (o.kind === "ok") {
      setCode(o.code);
      void load();
    } else {
      setError(ADMIN_TEXT[o.kind]);
    }
  };

  // US-O-11.1: a company and its first member.
  const createOrg = async () => {
    if (busy || !token) return;
    setBusy(true);
    setError("");
    const o = await postOrganization(token, orgName, orgEmail);
    setBusy(false);
    if (o.kind === "ok") {
      setOrgName("");
      setOrgEmail("");
      void load();
    } else {
      setError(ADMIN_TEXT[o.kind]);
    }
  };

  const head = (
    <div class="panel-head">
      <h1>系統後台</h1>
      <ThemeToggle />
    </div>
  );
  if (!token || (overview && overview.kind !== "ok")) {
    const kind = token && overview && overview.kind !== "ok" ? overview.kind : "denied";
    return (
      <main class="recover">
        {head}
        <p class="bad" role="alert">
          {ADMIN_TEXT[kind]}
        </p>
      </main>
    );
  }

  return (
    <main class="recover">
      {head}
      <button class="confirm" disabled={busy} onClick={() => void issue()}>
        {busy ? "產生中…" : "發一組邀請碼"}
      </button>
      {code && <CopyBox label="邀請碼（只顯示這一次；傳給對方，到 /new 建立活動，30 天內有效）" value={code} />}
      {error && (
        <p class="bad" role="alert">
          {error}
        </p>
      )}
      {!overview ? (
        <p class="hint">讀取中…</p>
      ) : (
        <>
          <h2>還沒用的邀請碼（{overview.invites.length}）</h2>
          <ul class="admin-list">
            {overview.invites.map((i) => (
              <li key={i.id}>
                第 {i.id} 組・{day(i.expiresAt)} 失效
              </li>
            ))}
          </ul>
          <h2>活動（{overview.events.length}）</h2>
          <ul class="admin-list">
            {overview.events.map((e) => (
              <li key={e.id}>
                {e.name}・{e.date}・{STATUS_TEXT[e.status] ?? e.status}・{day(e.createdAt)} 建立
                {e.organization ? `・${e.organization}` : ""}
              </li>
            ))}
          </ul>
          <h2>婚顧公司（{overview.organizations?.length ?? 0}）</h2>
          <label class="field">
            公司名稱
            <input type="text" autoComplete="off" value={orgName} disabled={busy}
              onInput={(e) => setOrgName((e.currentTarget as HTMLInputElement).value)} />
          </label>
          <label class="field">
            第一位成員的 email
            <input type="email" autoComplete="off" inputMode="email" autoCapitalize="off" spellcheck={false}
              value={orgEmail} disabled={busy} onInput={(e) => setOrgEmail((e.currentTarget as HTMLInputElement).value)} />
          </label>
          <button class="confirm" disabled={busy || orgName.trim() === "" || orgEmail.trim() === ""} onClick={() => void createOrg()}>
            建立公司
          </button>
          <ul class="admin-list">
            {(overview.organizations ?? []).map((o) => (
              <li key={o.id}>
                第 {o.id} 家・{o.name}・成員 {o.members}・活動 {o.events}・{day(o.createdAt)} 建立
              </li>
            ))}
          </ul>
        </>
      )}
    </main>
  );
}
