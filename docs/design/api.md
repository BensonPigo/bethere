# API 規格 (API)

> 狀態：PO 核准（2026-10-21）；已經安全審查（2026-10-21），無 P0／P1，發現均已處理；審查紀錄見 git 歷史。
> M2b 追加（第 3～8 節標示「M2b」的部分）：PO 核准（2026-10-22）；實作前安全審查（2026-10-22）無 P0，3 項 P1 與 P2 均已寫入本文件；完成後由 `verifier` 再驗一次。
> 範圍：`requirements/stories/m1.md`、`m2a.md`、`m2b.md`；之後的里程碑在本文件追加端點。
> 依據：`access-control.md`（身分、驗證結果、權限表）、`sync-and-offline.md`（輪詢與快照）、`data-model.md`（狀態轉換）、`tech-stack.md` 2.1（逾時與錯誤、Token 與復原碼、lastSeenAt、日誌、安全標頭）。

---

## 1. 共通慣例

| 項目 | 規則 |
| --- | --- |
| 格式 | 請求與回應皆為 JSON（`Content-Type: application/json`），UTF-8 |
| Token | 只放在 `Authorization: Bearer <token>` 標頭（Caddy 預設在存取日誌中遮蔽 `Authorization`，不要啟用 `log_credentials`）。不接受查詢字串、Cookie 或本文中的 Token |
| Token 格式 | 16 位元組隨機值的 base64url（無補位），恰好 22 字元；伺服器以 SHA-256（對這 22 個字元的 ASCII 位元組）查詢（指令產生連結與伺服器查詢用同一規則，不需先解碼）。格式不符視為「格式錯誤」（400），**不是**「無效」 |
| 快取 | 所有 `/api/` 回應（含錯誤）一律 `Cache-Control: no-store`，由中介層統一設定 |
| 本文 | 上限 16 KB；只接受單一 JSON 物件；**未知欄位一律拒絕**（400）；`version` 缺少、為 `null`、不是整數或小於 1 都回 400 |
| 活動與角色 | Token 驗證的請求**不帶** `eventId` 或自己的 `roleId`；活動與角色一律取自 Token（杜絕跨活動與冒用他人角色） |
| 時間 | 一律 UTC，RFC 3339（含小數秒），例如 `2027-06-06T03:15:00.123Z` |
| 跨來源 | 不回任何 `Access-Control-*` 標頭、不使用 Cookie；Token 只在標頭，所以 CSRF 不適用 |
| 頻率限制 | 不對 Token 限頻（`access-control.md` 3.3） |
| 日誌 | 不記錄請求標頭、本文、Token、Token 雜湊；錯誤回應只回固定的錯誤代碼，不回內部錯誤訊息（`tech-stack.md` 2.1「日誌」） |
| 時鐘 | `serve` 只使用系統時鐘，不提供任何覆寫時間的設定或參數（正式環境若殘留覆寫的時間，可能讓所有裝置收到 `ended` 而被清除）；彩排用另一場日期為彩排當天的活動 |
| 逾時 | 快照讀取的資料庫查詢帶 3 秒 context 逾時（`tech-stack.md` 2.1「逾時與錯誤」）；`http.Server` 設定讀取、寫入、閒置逾時 |

## 2. 回應的兩種形狀

### 2.1 驗證結果（帶 `result`）

**只有**以下三種回應帶 `result` 欄位，HTTP 狀態一律 **200**：

| `result` | 條件（依序判斷） | 內容 | 客戶端 |
| --- | --- | --- | --- |
| `invalid` | 儲存層查詢成功，且確實查無有效 Token（`store.ErrInvalidToken`） | 只有 `{"result":"invalid"}`；錯誤與已撤銷不區分 | 顯示「連結已失效」，停止輪詢，清除該 Token 的本機資料 |
| `ended` | Token 有效，但活動已過 `endsAt`（查詢時的 `store.ErrEventEnded`，或寫入時對**自己活動**的 `domain.ErrEnded`） | 只有 `{"result":"ended"}`，**不含任何活動資料** | 顯示「活動已結束」，清除該 Token 的本機資料 |
| `ok` | 其餘 | `{"result":"ok","snapshot":{…}}` | 正常顯示 |

客戶端**只有**在 HTTP 狀態為 200、`Content-Type` 為 JSON、本文可解析，且 `result` 明確為 `invalid` 或 `ended` 時才清除本機資料（`access-control.md` 3.3）。這是為了防止日後的處理器或代理在錯誤本文中帶出 `result`。

### 2.2 錯誤（不帶 `result`）

其他一切失敗都**不帶** `result`，以 `{"error":"<代碼>"}` 回應；客戶端一律視為「操作失敗」或「離線」，**絕不清除本機資料**。

| HTTP | `error` | 何時 |
| --- | --- | --- |
| 400 | `bad_request` | 缺少或重複的 `Authorization` 標頭、Token 格式不符、本文不是單一 JSON 物件、有未知欄位、超過 16 KB |
| 403 | `forbidden` | Token 有效，但角色類型沒有這個端點的權限（第 4 節） |
| 409 | `version_conflict` | 寫入帶的 `version` 不是目前版本；回應另附 `snapshot`（最新快照），客戶端更新畫面並提示「畫面已更新，請重新確認」 |
| 404 | `not_found` | 本文中的 Cue id 或角色 id 不在呼叫者的活動裡（不存在或屬於別的活動，兩者不區分；第 6 節） |
| 422 | `not_draft`、`not_live`、`not_event_day`、`nothing_to_advance`、`nothing_to_undo`、`undo_stale`、`shift_too_early`、`invalid_delta`；M2b 追加 `not_pending`、`invalid_position`、`invalid_cue`、`invalid_assignment`、`not_checkin_role`、`master_role` | 領域規則不允許（第 6 節） |
| 500 | `internal` | 資料庫錯誤、逾時、`SQLITE_BUSY`、未預期的狀態（例如兩個進行中的 Cue）、任何未列出的錯誤 |

## 3. 端點

| 方法與路徑 | 憑證 | 權限（第 4 節） | 本文 | 成功回應 |
| --- | --- | --- | --- | --- |
| `GET /api/snapshot` | Token | 讀取快照 | — | `ok` + 依角色的快照 |
| `POST /api/cues/advance` | Token | Master | `{"version":n}` | `ok` + 新的 Master 快照 |
| `POST /api/cues/undo` | Token | Master | `{"version":n}` | `ok` + 新的 Master 快照 |
| `POST /api/cues/shift/preview` | Token | Master | `{"fromCueId":n,"deltaMin":n,"excluded":[n…]}` | `{"result":"ok","preview":{"changes":[{"cueId","fromStart","toStart"}],"conflicts":[…],"tooEarly":{"cueId","newStart"}或null}}`；不寫入；活動須為 Live |
| `POST /api/cues/shift` | Token | Master | `{"version":n,"fromCueId":n,"deltaMin":n,"excluded":[n…]}` | `ok` + 新的 Master 快照 |
| `POST /api/cues/skip` | Token | Master | `{"version":n,"cueId":n}` | `ok` + 新的 Master 快照（M2b，US-M-6） |
| `POST /api/cues/insert` | Token | Master | `{"version":n,"afterCueId":n,"start":"<RFC 3339>","durationMin":n,"title":"…","notePublic":"…"或null,"roles":[{"roleId":n,"roleNote":"…"或null}…]}` | `ok` + 新的 Master 快照（M2b，US-M-8） |
| `POST /api/cues/roles` | Token | Master | `{"version":n,"cueId":n,"roles":[{"roleId":n,"roleNote":"…"或null}…]}` | `ok` + 新的 Master 快照；`roles` 是該 Cue **完整的**指派清單，伺服器比對差異（M2b，US-M-8） |
| `POST /api/cues/edit` | Token | Master | `{"version":n,"cueId":n,"title":"…","notePublic":"…"或null}` | `ok` + 新的 Master 快照（M2b，US-M-8.7） |
| `POST /api/checkin` | Token | 本人打卡 | `{"checkedIn":bool}` | `ok` + 自己的角色快照（M2b，US-X-5） |
| `POST /api/roles/checkin` | Token | Master | `{"roleId":n,"checkedIn":bool}` | `ok` + 新的 Master 快照（M2b，US-M-1.8） |
| `POST /api/roles/reissue` | Token | Master | `{"roleId":n}` | `{"result":"ok","snapshot":{…},"token":"<22 字元>"}`（M2b，US-M-7.1） |
| `POST /api/roles/revoke` | Token | Master | `{"roleId":n}` | `ok` + 新的 Master 快照（M2b，US-M-7.2） |
| `GET /api/ping` | 無 | — | — | `{"serverTime":…,"version":"<發行版>"}`；部署檢查用；不含活動資料 |

- `POST /api/event/start` 已移除（M2a）：活動日當天 Draft 狀態下的 `POST /api/cues/advance` 同時開始活動；非活動日回 422 `not_event_day`（ADR-0020）。
- 平移的本文與 `version` 一樣嚴格：欄位名稱完全相符、不可重複、只接受整數（不接受 `-0`、前導零、小數、字串）；`deltaMin` 可為負，範圍由領域規則檢查（±1440，否則 `invalid_delta`）；`excluded` 可省略，最多 200 個正整數，不在本活動的 id 會被忽略。預覽不帶 `version`。
- Master 的寫入都要帶 `version`（`sync-and-offline.md` 第 3 節）；寫入成功的回應直接附上新快照（`sync-and-offline.md` 第 1 節），總召不必等下一次輪詢。
- **M2b 寫入的共通規則**：
  - 改到 Cue 的寫入（略過、新增、指派、編輯）帶 `version`、成功後 `eventVersion` + 1。就位打卡、撤銷、重發**不帶** `version`、**不改** `eventVersion`（ADR-0023）；本文有 `version` 欄位視為未知欄位（400）。
  - 只有略過是「復原上一步」的步驟（`cue.skip`）；其他寫入的操作紀錄型別為 `rundown.insert`、`rundown.assign`、`rundown.edit`、`checkin.set`、`token.reissue`、`token.revoke`，不以 `cue.` 開頭（ADR-0021）。操作紀錄不含 Token 或其雜湊。
  - 活動階段：M2b 的寫入在 Draft 與 Live 都可以，因為活動日當天第一次推進前活動仍是 Draft（ADR-0020），總召可能需要在開始前臨時取消或加環節（US-M-6.3）。`POST /api/cues/undo` 因此也改為 Draft 可用（Draft 階段唯一的 Cue 操作是略過）；推進與平移的階段規則不變。
- **略過**：目標須為 Pending（否則 `not_pending`）。
- **新增**：`start` 為 UTC 時間，須落在活動日 00:00（活動時區）到 `endsAt` 之間；`durationMin` 0～1440；`title` 1～100 字（去除前後空白後）；`notePublic`、`roleNote` 最多 500 字；不合格回 `invalid_cue`。插入位置之後的 Cue 都必須是 Pending 或 Skipped，也就是不能插在進行中或已完成的 Cue 前面（否則 `invalid_position`）。`afterCueId` 必填，所以新 Cue 不會成為第一個。
- **指派**：目標 Cue 須為 Pending（`not_pending`）；`roles` 最多 50 個、`roleId` 不可重複（400）；只能指派 Stylist、Media、StageAV（Master 本來就看全部、Host 不看 Cue；否則 `invalid_assignment`）。新增時的 `roles` 同樣適用。
- **編輯**：只改標題與公開備註，限制同新增；任何狀態的 Cue 都可以。不改 `scheduledStart`、長度、固定（`access-control.md` 第 4 節）。
- **就位打卡**：設定值而不是切換，重複送出相同的值不產生新紀錄（US-X-5.4）。本人打卡的角色只取自 Token；代打卡的目標須為 Stylist、Media、StageAV（否則 `not_checkin_role`），寫入 `byRoleId` = Master。
- **歸屬先於一切檢查**（安全審查 P1）：M2b 的 store 函式接收呼叫者的活動 id，以「id 且屬於該活動」查詢目標 Cue 或角色；查不到時回 404，**排在** `master_role`、`invalid_assignment`、`not_checkin_role`、`not_pending`、`invalid_position` 等任何依目標類型或狀態的檢查之前，否則別的活動的 id 會因回應不同而洩漏它的類型或狀態。
- **目標的 Token 不影響驗證結果**（安全審查 P1）：撤銷、重發、代打卡、指派查詢的是**目標角色**，目標沒有有效 Token、Token 已撤銷都不是錯誤，絕不回 `store.ErrInvalidToken`、`store.ErrEventEnded` 或 `domain.ErrEnded`；`invalid` 與 `ended` 只描述**呼叫者自己**的 Token 與活動（第 2.1 節）。同理，新增 Cue 的 `start` 晚於 `endsAt` 回 `invalid_cue`，不得沿用 `domain.ErrEnded`。
- **撤銷與重發**：目標為 Master 時回 `master_role`（Master 只能以復原碼換發）；目標可以是 Host。重發在單一交易內撤銷舊 Token、建立新 Token；新 Token 的明文**只出現在這一次回應**，客戶端以 `location.origin + "/#t=" + token` 組成連結顯示，不存入本機快照或任何儲存。撤銷沒有有效 Token 的角色時照常成功（冪等，現有 `store.RevokeToken` 回 `ErrNotFound` 的行為要改）。兩台總召裝置同時重發同一角色時，後到的覆蓋前一個，先拿到的連結已失效（接受；畫面上的「連結最近使用時間」看得出新連結有沒有人開）。客戶端只把回應中的 `snapshot` 存入本機快照，`token` 只以文字加「複製」按鈕顯示，**不做成可點的連結**（總召在同一分頁點開會讓裝置變成該角色）。撤銷與重發的操作紀錄另記操作者使用的 `access_tokens.id`（不是雜湊），以復原碼換發 Master 連結後，才分得出哪些重發是外流的舊連結做的（`access-control.md` 3.4）。重發已寫入、但重新讀取快照失敗時回 500（第 7 節第 4 條），新 Token 的明文因此遺失；舊連結已失效，總召再按一次重發即可。
- **`POST /api/report` 移除**：實機驗證原型已完成；它不需憑證、會寫入日誌並記錄 Token 前綴。M3 的 Android 驗證改以截圖與手動紀錄。

## 4. 權限

角色類型**只取自** Token 所屬角色（`TokenOwner.RoleType`），不信任請求內容。**未列出的組合一律拒絕**（403）。

| 端點 | Master | Stylist | Media | StageAV | Host | 其他類型 |
| --- | --- | --- | --- | --- | --- | --- |
| `GET /api/snapshot` | Master 快照 | 角色快照 | 角色快照（M2b） | 角色快照（M2b） | 彙總快照 | 403 |
| `POST /api/cues/advance` | ✔ | 403 | 403 | 403 | 403 | 403 |
| `POST /api/cues/undo` | ✔ | 403 | 403 | 403 | 403 | 403 |
| `POST /api/cues/shift/preview` | ✔ | 403 | 403 | 403 | 403 | 403 |
| `POST /api/cues/shift` | ✔ | 403 | 403 | 403 | 403 | 403 |
| `POST /api/cues/skip`、`insert`、`roles`、`edit` | ✔ | 403 | 403 | 403 | 403 | 403 |
| `POST /api/checkin` | 403 | ✔ | ✔ | ✔ | 403 | 403 |
| `POST /api/roles/checkin`、`reissue`、`revoke` | ✔ | 403 | 403 | 403 | 403 | 403 |

- **M2a 開放 Master、Stylist 與 Host**：`bethere add-role` 接受 Stylist 與 Host，Media、StageAV 於 M2b 開放（未定義的類型不能落入寬鬆的預設權限）；開放前，資料庫中的 Media、StageAV 同樣適用下一句。P1 角色類型（VenueLead、Reception、Cashier、Runner）在 P0 不能建立。資料庫中出現任何未開放的類型時，每個端點都回 403，不落入任何預設分支。
- 權限檢查在 API 層。store 只確認操作者的憑證仍有效且屬於同一活動，**不檢查角色類型**（`access-control.md` 第 5 節第 6 條），所以每個寫入端點都必須先通過本表。

## 5. 快照

### 5.1 共通欄位

| 欄位 | 說明 |
| --- | --- |
| `eventVersion` | 單調遞增整數 |
| `serverTime` | 產生快照時的伺服器時間 |
| `eventStatus` | `Draft` 或 `Live`（結束時回 `ended`，不會有快照） |
| `role` | `{"type":"Stylist","name":"新秘","view":"HandsFree"}`：自己的角色；`view` 為解析過預設值後的畫面（ADR-0025，M2a 追加） |
| `eventDay` | M2a 追加：活動日期與時區 `{"date","timezone"}`，供 Draft 階段判斷「是否為活動日」與顯示日期（`screen-design.md` 4.5，ADR-0020）；Host 同樣帶 |

### 5.2 Master 快照（另含）

| 欄位 | 說明 |
| --- | --- |
| `event` | `{"name","date","timezone"}` |
| `cues[]` | 全部 Cue：`id, seq, title, scheduledStart, effectiveStart, durationMin, isFixed, offsetMin, status, actualStart, actualEnd, notePublic, roles[]`；`roles[]` 為 `{"roleId","name","roleNote"}` |
| `activeCueId` | 進行中的 Cue，沒有則為 `null` |
| `undo` | `{"available":bool,"description":"撤銷：推進『一進預備』"}`；描述由伺服器產生，沒有可復原的操作時為 `null`。推進的描述以「開始的那個 Cue」命名；推進的是最後一個 Cue（沒有開始新的）時，以「完成的那個 Cue」命名（PO 於 2026-10-22 確認） |
| `roles[]` | `{"id","name","type","checkedIn","lastSeenAt","hasLink"}`（M2b 追加後三者）：`checkedIn` 為是否就位（Master 與 Host 一律 `false`）；`lastSeenAt` 為該角色連結最近一次驗證成功的時間，**截到分鐘**，沒有時為 null；`hasLink` 為是否有有效 Token。不含 Token 或其雜湊 |
| `outOfOrder[]` | M2b 追加：時間倒序警告 `{"cueId","prevCueId"}`（`data-model.md` 4.2 第 5 步，US-M-8.6） |
| `conflicts[]` | 固定環節衝突：`{"cueId","fixedCueId","overrunMin"}`（`data-model.md` 4.2） |
| `overrunMin` | 超時分鐘（`data-model.md` 4.3，ADR-0022）；活動未開始時為 0 |

### 5.3 角色快照（Stylist、Media、StageAV）

| 欄位 | 說明 |
| --- | --- |
| `cues[]` | **只有指派給自己的 Cue**：`id, seq, title, effectiveStart, durationMin, isFixed, offsetMin, status, notePublic, roleNote`（只有自己的 `roleNote`） |
| `notice` | 變更提示，取以下兩種中**較新的一則**（US-X-7.4），沒有時為 `null`；客戶端以 `at` 判斷是否已顯示過（`screen-design.md` 4.4）。<br>平移（或平移的復原）：`{"kind":"shift","at","deltaMin","changes":[{"cueId","fromStart","toStart"}]}`。<br>新指派（M2b）：`{"kind":"assign","at","cueId","title","effectiveStart"}`，來自新增時的指派或改派，只算把**自己**加進去的那一次。<br>兩種都只看**目前仍指派給自己**的 Cue：`changes` 只列這些 Cue，指派之後又被移除的 Cue 不產生提示，避免揭露已不屬於自己的 Cue |
| `myCheckIn` | M2b 追加：自己是否已就位（bool） |
| `currentTitle` | M2b 追加：進行中 Cue 的標題，不論是否指派給自己，沒有時為 null；只有標題，不含時間、備註或 id（PO 於 2026-10-22 決定，US-X-6.4）。Stylist 同樣帶，三種類型的鍵集合相同 |

### 5.4 彙總快照（Host）

| 欄位 | 說明 |
| --- | --- |
| `delayMin` | 延誤分鐘：下一個待辦 Cue 的 `offsetMin` + 超時分鐘（`data-model.md` 4.3，ADR-0026）；活動未開始時為 0 |
| `currentTitle` | 進行中 Cue 的標題（不論指派給誰），沒有時為 null；Host 唯一看得到的 Cue 資訊 |

就位率於 M2 追加。

### 5.5 一律不出現

Token 或其雜湊、復原碼雜湊、其他角色的 `roleNote`、未指派給自己的 Cue（Master 除外）、操作紀錄與內部 id（`action_logs`、`check_ins`）。各角色快照的**鍵集合**以測試固定（`tech-stack.md` 2.2「型別」）。

### 5.6 Draft 階段

非 Master 角色在 Draft 階段也回完整的角色快照，讓裝置在活動前就存好離線快照（`operations/runbook.md` 第 3 節）；畫面依 `screen-design.md` 4.5 顯示：活動日當天照常倒數，活動日以前顯示 Cue 清單，「活動尚未開始」只是窄條（ADR-0020）。

### 5.7 條件請求與伺服器時間（M2b）

快照回應帶 ETag（不含 `serverTime` 的內容雜湊）與伺服器時間標頭；`If-None-Match` 相同時回 304，同樣 `no-store`。「無效」與「已結束」永遠不回 304。規則見 `sync-and-offline.md` 第 2 節，理由見 ADR-0023。

| 項目 | 規則 |
| --- | --- |
| 範圍 | 只有 `GET /api/snapshot`；寫入的回應不帶 ETag |
| ETag | 強 ETag：`"` + base64url（無補位）的 SHA-256 + `"`；雜湊的對象是 `ok` 回應本文以 `serverTime` 設為空字串後的 JSON（鍵順序由結構固定，以測試釘住同一內容產生同一值） |
| 伺服器時間 | `X-Server-Time`，RFC 3339 毫秒精度；200 與 304 都帶；200 時與本文的 `serverTime` 相同 |
| 比對 | 先驗 Token、檢查權限、組好呼叫者的快照，**之後**才比對 `If-None-Match`；只有恰好一行標頭、其值恰好是一個強 ETag 且完全相符才回 304；`*`、逗號分隔的多個值、重複的標頭行、弱 ETag 一律當作不符、回 200 |
| 304 | 不帶本文；帶 `ETag`、`X-Server-Time`、`Cache-Control: no-store`；`lastSeenAt` 照常更新（驗證已成功） |
| 客戶端 | ETag 只存在記憶體、依 Token 分開，不寫入本機儲存；重新整理後第一次請求不帶 `If-None-Match`。收到 304 時保留快照，只以 `X-Server-Time` 更新時鐘偏差 |

## 6. 錯誤對照

| 來源錯誤 | 回應 |
| --- | --- |
| `store.ErrInvalidToken`（查詢或寫入時） | `result: invalid` |
| `store.ErrEventEnded`（查詢） | `result: ended` |
| `domain.ErrEnded`（寫入自己的活動） | `result: ended` |
| 角色類型不符 | 403 `forbidden` |
| `store.ErrVersionConflict` | 409 `version_conflict` + 最新快照 |
| `domain.ErrNotDraft` | 422 `not_draft` |
| `domain.ErrNotLive` | 422 `not_live` |
| `domain.ErrNotEventDay` | 422 `not_event_day` |
| `domain.ErrNothingToAdvance` | 422 `nothing_to_advance` |
| `store.ErrNothingToUndo` | 422 `nothing_to_undo` |
| `domain.ErrUndoStale` | 422 `undo_stale` |
| `domain.ErrShiftTooEarly` | 422 `shift_too_early`（預覽改以 `tooEarly` 回報） |
| `domain.ErrInvalidDelta` | 422 `invalid_delta` |
| 略過或指派的 Cue 不是 Pending | 422 `not_pending` |
| 新增的插入位置在進行中或已完成的 Cue 前面 | 422 `invalid_position` |
| 新增或編輯的時間、長度、標題、備註不合格 | 422 `invalid_cue` |
| 指派給 Master、Host 或其他不開放的類型 | 422 `invalid_assignment` |
| 代打卡的目標不是 Stylist、Media、StageAV | 422 `not_checkin_role` |
| 撤銷或重發的目標是 Master | 422 `master_role` |
| 本文的角色 id 不在呼叫者的活動裡 | 404 `not_found`，不帶 `result`（與 Cue id 相同） |
| `domain.ErrCueNotFound` | 404 `not_found`，不帶 `result` |
| `store.ErrNoActor`、`store.ErrCrossEvent`、`store.ErrNotFound` | 500 `internal`（端點先以呼叫者的活動查詢目標，查不到已回 404；這三者出現即為程式錯誤） |
| `domain.ErrMultipleInProgress`、`store.ErrUnknownOp`、資料庫錯誤、逾時 | 500 `internal` |
| 其他 | 500 `internal` |

**帶 id 的請求**：store 先檢查操作者與目標是否同一活動（`ErrCrossEvent`），再檢查活動是否結束，所以別的已結束活動的 id 不會回 `ended` 而清掉呼叫者的資料（M2a 修正，以測試固定）。平移的 Cue id 只在呼叫者自己活動的 Cue 中尋找，屬於別的活動的 id 回與「不存在」相同的 404，不帶 `result`。M2b 的 Cue id（`cueId`、`afterCueId`）與角色 id（`roleId`）同樣只在呼叫者自己的活動中尋找。

## 7. 伺服器端規則

1. **每個請求**：解析標頭 → 查 Token（`store.LookupToken`）→ 依第 4 節檢查權限 → 執行。
2. **快照只有一個產生函式**：`GET`、寫入成功、409 三處都呼叫同一個函式，它在**單一唯讀交易**內查 Token 並組出**呼叫者自己角色**的快照，結果依第 6 節對照。在同一交易內查 Token 與組快照，撤銷與結束才會立即生效、沒有毫秒級的空窗；只有一個產生函式，避免 409 或寫入回應意外帶出 Master 快照。store 需新增這個函式（現有的 `LookupToken` 自己開關交易，分兩次呼叫會留下空窗）；以「兩步之間撤銷或結束」的測試驗證。
3. **處理順序**：解析標頭 → 查 Token → 權限（403）→ 解析本文（400）→ 寫入 → 重新讀取快照。409 只會在通過權限檢查後出現。
4. **寫入成功後重新讀取的結果**：讀到 `ok` 回 200 與新快照；讀到 `invalid` 或 `ended`（例如寫入與讀取之間被撤銷）就回那個結果；資料庫錯誤回 500（寫入已生效，客戶端下一次輪詢會看到）。一個回應**絕不同時**帶 `error` 與 `result`；409 附的快照同樣依此處理，讀不到 `ok` 就改回對應的 `result` 或 500。
5. **`lastSeenAt`**：只在 Token 驗證成功之後於記憶體更新（`tech-stack.md` 2.1「lastSeenAt」）；M3 加入維護模式後，改為通過維護檢查之後才更新。M2b 起以 **`access_tokens.id`** 為鍵（`TokenOwner` 帶出這個 id，不是雜湊）；Master 快照只對每個角色**目前有效的** Token 查詢，所以撤銷或重發後，舊連結的使用時間（包括撤銷前一刻才通過驗證的請求）不會算到新連結上，以復原碼換發的 Master 連結也一樣。
6. **所有寫入**都帶 store 的 `Actor`，在同一交易內重新確認憑證並寫入操作紀錄。
7. **指令（`bethere` 子命令）**：
   - `serve`：`BETHERE_INIT` 只接受 `1`（建立新資料庫）或未設定／空字串；其他值一律拒絕啟動（避免 `BETHERE_INIT=0` 之類的值被當成「要建立」而在還原失敗時建出空庫，ADR-0012）。
   - `create-event`、`add-role`、`import-rundown` 一律以「不建立新資料庫」的方式開啟；連結與復原碼只印到終端機（stdout），不經過結構化日誌。
   - `add-role` 與 `import-rundown` 以**總召的身分**寫入：執行時提示輸入 Master 連結（不回顯），從標準輸入讀取，**絕不從命令列參數或環境變數讀取**，再經 `LookupToken` 取得操作者（不留在 shell 歷史與程序清單）。不新增任何「伺服器操作者」，維持「操作者只能來自驗證過的憑證」；操作紀錄記為 Master。
   - `import-rundown` 在單一交易內寫入全部 Cue 與指派，任一列不合法就整份不寫入（US-M-0.2）。

## 8. 安全邊界相關的限制

- **維護模式**（`tech-stack.md` 2.1「維護模式」）在 M3 實作；在那之前**正式環境不放入真實活動資料**（`process/development-process.md`）。
- 第 2、4、6 節屬於「Token 權限與資料外洩」邊界：實作完成後由 `verifier` 依第 4 節權限表的**每一格**、第 6 節的**每一列**與第 5 節的鍵集合驗證。
- 必要的測試（安全審查要求）：
  - 資料庫中直接插入未開放的角色類型，每個端點都回 403。
  - Token 的雜湊規則以固定的測試向量釘住，指令與伺服器共用；另有一個測試把指令產生的連結送進 HTTP 處理器，預期 `ok`（雜湊規則不一致會讓所有裝置同時被清除）。
  - 查詢與寫入時 context 已逾時或已取消，結果都**不是** `invalid`。
  - 404、405、本文過大、400、403、409、500 的回應都有 `no-store` 且不帶 `result`。
  - `POST /api/report` 回 404 或 405。
  - `BETHERE_INIT` 為 `0`、`true`、` 1`、`yes` 時拒絕啟動。
  - M2b：
    - 第 4 節新增的每一格（含 Host 與未開放類型呼叫每個新端點都回 403）。
    - 本人打卡無法指定別的角色（本文帶 `roleId` 為 400）；代打卡、撤銷、重發、指派帶別的活動的角色 id 回 404 且不帶 `result`，帶別的**已結束**活動的角色 id 也一樣。
    - 帶別的活動的 **Master、Host 角色 id** 或**非 Pending 的 Cue id**，回 404 而不是 `master_role`、`not_checkin_role`、`invalid_assignment`、`not_pending`、`invalid_position`。
    - 撤銷一個沒有有效 Token 的角色回 200；重發、代打卡、指派一個 Token 已撤銷的角色回 200；總召都不會收到 `invalid`。
    - 新增 Cue 的 `start` ≥ `endsAt` 回 422 `invalid_cue`，不帶 `result`。
    - `If-None-Match` 為 `*`、逗號分隔多值、兩行標頭、弱 ETag 時都回 200。
    - 前端：重發回應的 `token` 不進本機儲存。
    - 重發後舊 Token 立即回 `invalid`、新 Token 回 `ok`；重發的回應以外，任何回應與操作紀錄都不含 Token 明文或雜湊。
    - 撤銷或結束後，帶著之前拿到的 ETag 請求仍回 200 與 `result`，不回 304。
    - 不同角色的快照內容剛好相同時 ETag 也相同；因為驗證與組快照都在比對之前完成，304 只表示「與你上次拿到的相同」，不會讓任何人看到別人的內容。以「A 角色的 ETag 送給 B 角色」的測試固定。
    - 指派之後移除的 Cue 不出現在該角色的 `notice`；`currentTitle` 以外不揭露未指派的 Cue。
    - Media、StageAV、Stylist 快照的鍵集合相同，並以測試固定。
