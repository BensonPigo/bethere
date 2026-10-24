// `/print`: the backup rundown on paper (sync-and-offline.md 9, screen-design.md 8;
// ADR-0014). It reads the same snapshot as the screens, the saved one when offline.
import { useState } from "preact/hooks";
import { isHostSnapshot, isMasterSnapshot } from "./api";
import { isLineBrowser, tokenFromHash } from "./env";
import { DEFAULT_TZ, formatHM } from "./format";
import { printRows } from "./print";
import { useSession } from "./session";

export function PrintPage() {
  const token = tokenFromHash(location.hash);
  const s = useSession(token);
  const [roleId, setRoleId] = useState<string | null>(null);
  const back = <a href={`/${location.hash}`}>← 回到畫面</a>;

  if (!token) return <Note back={back}>找不到連結資訊，請使用總召給你的完整連結開啟</Note>;
  if (s.phase === "invalid") return <Note back={back}>連結已失效，請向總召索取新連結</Note>;
  if (s.phase === "ended") return <Note back={back}>活動已結束，感謝你的協助</Note>;
  const snap = s.snap;
  if (!snap) return <Note back={back}>{s.offline ? "無法連線，請檢查網路" : "載入中…"}</Note>;
  if (isHostSnapshot(snap)) return <Note back={back}>這個連結沒有可列印的流程表</Note>;

  const tz = snap.eventDay?.timezone || DEFAULT_TZ;
  const master = isMasterSnapshot(snap);
  const rows = printRows(snap, master ? roleId : null);
  const roleName = master ? snap.roles.find((r) => String(r.id) === roleId)?.name : snap.role.name;
  const title = master ? snap.event.name : snap.role.name;
  const canPrint = typeof window.print === "function";

  return (
    <main class="print">
      <div class="no-print">
        {back}
        {master && (
          <label>
            印給
            <select value={roleId ?? ""} onChange={(e) => setRoleId((e.target as HTMLSelectElement).value || null)}>
              <option value="">全部角色</option>
              {snap.roles
                .filter((r) => r.type !== "Master" && r.type !== "Host")
                .map((r) => (
                  <option key={r.id} value={String(r.id)}>
                    {r.name}
                  </option>
                ))}
            </select>
          </label>
        )}
        <button onClick={() => window.print()} disabled={!canPrint}>
          列印／另存 PDF
        </button>
        {isLineBrowser(navigator.userAgent) && <p class="hint">LINE 裡可能無法列印，請改用 Safari／Chrome 開啟這一頁。</p>}
        {s.offline && <p class="hint">離線中：印出的是 {formatHM(snap.serverTime, tz)} 的資料。</p>}
      </div>
      <h1>
        備援流程表・{title}
        {master && roleName ? `（${roleName}）` : ""}
      </h1>
      <p>
        匯出於 {formatHM(new Date(Date.now() + s.skew).toISOString(), tz)}・資料時間 {formatHM(snap.serverTime, tz)}・版本{" "}
        {snap.eventVersion}
      </p>
      {rows.length === 0 ? (
        <p>沒有指派給你的環節</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>時間</th>
              <th>分</th>
              <th>環節</th>
              <th>角色與備註</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id} class={r.done ? "done" : ""}>
                <td>{formatHM(r.start, tz)}</td>
                <td>{r.durationMin}</td>
                <td>
                  {r.title}
                  {r.isFixed && "（固定）"}
                  {r.done && "（已過）"}
                  {r.notePublic && <div class="note">{r.notePublic}</div>}
                </td>
                <td>
                  {r.roles.map((x) => (
                    <div key={x.name}>
                      {master ? (
                        <>
                          <b>{x.name}</b>
                          {x.note ? `：${x.note}` : ""}
                        </>
                      ) : (
                        x.note
                      )}
                    </div>
                  ))}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </main>
  );
}

function Note(props: { back: preact.ComponentChildren; children: preact.ComponentChildren }) {
  return (
    <main class="print">
      <div class="no-print">{props.back}</div>
      <p>{props.children}</p>
    </main>
  );
}
