# API 規格 (API)

> 範圍：`requirements/stories.md` 中狀態為完成的故事。
> 依據：`access-control.md`（身分、驗證結果、權限表）、`sync-and-offline.md`（輪詢與快照）、`data-model.md`（狀態轉換）、`tech-stack.md` 2.1（逾時與錯誤、Token 與復原碼、lastSeenAt、日誌、安全標頭）。

---

## 1. 共通慣例

| 項目 | 規則 |
| --- | --- |
| 格式 | 請求與回應皆為 JSON（`Content-Type: application/json`），UTF-8 |
| Token | 只放在 `Authorization: Bearer <token>` 標頭（Caddy 預設在存取日誌中遮蔽 `Authorization`，不要啟用 `log_credentials`）。不接受查詢字串、Cookie 或本文中的 Token |
| Token 格式 | 16 位元組隨機值的 base64url（無補位），恰好 22 字元；伺服器以 SHA-256（對這 22 個字元的 ASCII 位元組）查詢（指令產生連結與伺服器查詢用同一規則，不需先解碼）。格式不符視為「格式錯誤」（400），**不是**「無效」 |
| 快取 | 所有 `/api/` 回應（含錯誤）一律 `Cache-Control: no-store`，由中介層統一設定 |
| 本文 | 上限 16 KB（`/api/rundown/import` 與 `import/preview` 上限 256 KB，只有這兩個端點；其中 `text` 的 100 KB 以解碼後的 UTF-8 位元組計算，JSON 跳脫後仍在 256 KB 內）；只接受單一 JSON 物件；**未知欄位一律拒絕**（400）；`version` 缺少、為 `null`、不是整數或小於 1 都回 400 |
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
| 400 | `bad_request` | 缺少或重複的 `Authorization` 標頭、Token 格式不符、本文不是單一 JSON 物件、有未知欄位、超過本文上限（第 1 節） |
| 403 | `forbidden` | Token 有效，但角色類型沒有這個端點的權限（第 4 節） |
| 409 | `version_conflict` | 寫入帶的 `version` 不是目前版本；回應另附 `snapshot`（最新快照），客戶端更新畫面並提示「畫面已更新，請重新確認」 |
| 404 | `not_found` | 本文中的 Cue id 或角色 id 不在呼叫者的活動裡（不存在或屬於別的活動，兩者不區分；第 6 節）；系統後台的端點不是帶著目前的管理員連結（沒有或格式不符的標頭、查無、任何角色的連結、被取代的管理員連結都相同，先於本文檢查；US-O-9.1） |
| 422 | `wrong_recovery_code`、`event_ended`（只有復原碼入口，見第 3 節）、`wrong_invite_code`、`invalid_event`（只有建立活動，見第 3 節）、`too_many_invites`（只有系統後台）、`not_draft`、`not_live`、`not_event_day`、`nothing_to_advance`、`nothing_to_undo`、`undo_stale`、`shift_too_early`、`invalid_delta`、`not_pending`、`invalid_position`、`invalid_cue`、`invalid_assignment`、`not_checkin_role`、`master_role`、`invalid_role`、`duplicate_name`、`too_many_roles`、`invalid_import`、`locked_on_event_day`，以及婚顧公司的代碼（10.3） | 領域規則不允許（第 6 節）；婚顧公司的端點另見 10.1 的 404 規則 |
| 500 | `internal` | 資料庫錯誤、逾時、`SQLITE_BUSY`、未預期的狀態（例如兩個進行中的 Cue）、任何未列出的錯誤 |
| 503 | `maintenance` | 維護模式下，Master 與復原碼換發以外的所有 `/api/` 請求，以及任何人的建立活動（第 8 節）；「成員連結待清除」標記存在時的婚顧公司端點與主控台連結（10.2） |

## 3. 端點

| 方法與路徑 | 憑證 | 權限（第 4 節） | 本文 | 成功回應 |
| --- | --- | --- | --- | --- |
| `GET /api/snapshot` | Token | 讀取快照 | — | `ok` + 依角色的快照 |
| `POST /api/cues/advance` | Token | Master | `{"version":n}` | `ok` + 新的 Master 快照 |
| `POST /api/cues/undo` | Token | Master | `{"version":n}` | `ok` + 新的 Master 快照 |
| `POST /api/cues/shift/preview` | Token | Master | `{"fromCueId":n,"deltaMin":n,"excluded":[n…]}` | `{"result":"ok","preview":{"changes":[{"cueId","fromStart","toStart"}],"conflicts":[…],"tooEarly":{"cueId","newStart"}或null}}`；不寫入；活動須為 Live |
| `POST /api/cues/shift` | Token | Master | `{"version":n,"fromCueId":n,"deltaMin":n,"excluded":[n…]}` | `ok` + 新的 Master 快照 |
| `POST /api/cues/skip` | Token | Master | `{"version":n,"cueId":n}` | `ok` + 新的 Master 快照（US-M-6） |
| `POST /api/cues/insert` | Token | Master | `{"version":n,"afterCueId":n,"start":"<RFC 3339>","durationMin":n,"title":"…","notePublic":"…"或null,"roles":[{"roleId":n,"roleNote":"…"或null}…]}` | `ok` + 新的 Master 快照（US-M-8） |
| `POST /api/cues/roles` | Token | Master | `{"version":n,"cueId":n,"roles":[{"roleId":n,"roleNote":"…"或null}…]}` | `ok` + 新的 Master 快照；`roles` 是該 Cue **完整的**指派清單，伺服器比對差異（US-M-8） |
| `POST /api/cues/edit` | Token | Master | `{"version":n,"cueId":n,"title":"…","notePublic":"…"或null}` | `ok` + 新的 Master 快照（US-M-8.7） |
| `POST /api/checkin` | Token | 本人打卡 | `{"checkedIn":bool}` | `ok` + 自己的角色快照（US-X-5） |
| `POST /api/roles/checkin` | Token | Master | `{"roleId":n,"checkedIn":bool}` | `ok` + 新的 Master 快照（US-M-1.8） |
| `POST /api/roles/reissue` | Token | Master | `{"roleId":n}`；可另帶 `"origin":"https://…"` | `{"result":"ok","snapshot":{…},"token":"<22 字元>"}`（US-M-7.1）；帶 `origin` 時另附 `"qr"`（US-M-13.1） |
| `POST /api/roles/revoke` | Token | Master | `{"roleId":n}` | `ok` + 新的 Master 快照（US-M-7.2） |
| `POST /api/cues/update` | Token | Master | `{"version":n,"cueId":n,"start":"<RFC 3339>","durationMin":n,"isFixed":bool,"title":"…","notePublic":"…"或null}` | `ok` + 新的 Master 快照；只限 Draft（US-M-10.2） |
| `POST /api/cues/move` | Token | Master | `{"version":n,"cueId":n,"afterCueId":n或null}` | `ok` + 新的 Master 快照；`null` 表示移到第一個；只限 Draft（US-M-10.3） |
| `POST /api/cues/delete` | Token | Master | `{"version":n,"cueId":n}` | `ok` + 新的 Master 快照；只限 Draft（US-M-10.4） |
| `POST /api/rundown/import/preview` | Token | Master | `{"text":"…"}` | `{"result":"ok","preview":{"fileError":"…"或null,"rows":[…],"missingRoles":["…"],"tooManyRoles":bool,"replaces":n}}`；不寫入；只限 Draft（US-M-12.2） |
| `POST /api/rundown/import` | Token | Master | `{"version":n,"text":"…","newRoles":[{"name":"…","view":"HandsFree"或"Glance","checksIn":bool}…]}` | `ok` + 新的 Master 快照；取代整份；只限 Draft（US-M-12.3、12.4、12.6） |
| `POST /api/roles/add` | Token | Master | `{"name":"…","view":"HandsFree"或"Glance"或"Dashboard","checksIn":bool,"origin":"https://…"}` | `{"result":"ok","snapshot":{…},"roleId":n,"token":"<22 字元>","qr":"data:image/png;base64,…"}`（US-M-11.1、11.2、US-M-13） |
| `POST /api/roles/rename` | Token | Master | `{"roleId":n,"name":"…"}` | `ok` + 新的 Master 快照（US-M-11.4） |
| `POST /api/roles/delete` | Token | Master | `{"version":n,"roleId":n}` | `ok` + 新的 Master 快照；只限 Draft（US-M-11.5） |
| `POST /api/roles/preview` | Token | Master | `{"roleId":n}` | `{"result":"ok","preview":{…}}`：`preview` 是目標角色自己會拿到的快照（US-M-14） |
| `POST /api/recover/master` | 復原碼（只在本文） | — | `{"recoveryCode":"…"}` | `{"result":"ok","token":"<22 字元>"}`：新的 Master 連結，不附快照（US-O-3） |
| `POST /api/events` | 邀請碼（只在本文） | — | `{"inviteCode":"…","name":"…","date":"YYYY-MM-DD"}` | `{"result":"ok","token":"<22 字元>","recoveryCode":"…"}`：新活動的 Master 連結與復原碼，不附快照（US-M-17） |
| `GET /api/admin/overview` | 管理員連結 | — | — | `{"result":"ok","invites":[{"id":n,"expiresAt":…}],"events":[{"id":n,"name":"…","date":"YYYY-MM-DD","status":"Draft\|Live\|Ended","createdAt":…}]}`：還沒用的邀請碼與所有活動，新的在前（US-O-9.2） |
| `POST /api/admin/invites` | 管理員連結 | — | `{}` | `{"result":"ok","id":n,"code":"…","expiresAt":…}`：新的邀請碼，只在這次回應（US-O-10） |
| `GET /support.json` | 無 | — | — | `{"email":"<客服信箱>"}`，沒有設定時為 `{"email":null}`；不在 `/api/` 下，維護模式照常回應；`Cache-Control: no-store`（US-O-8.1） |
| `GET /api/ping` | 無 | — | — | `{"serverTime":…,"version":"<發行版>"}`；部署檢查用；不含活動資料；維護模式下回 503 `maintenance`，`ops/gcp/maintenance.sh` 以此從外部確認模式 |

- 沒有 `POST /api/event/start`：活動日當天 Draft 狀態下的 `POST /api/cues/advance` 同時開始活動；非活動日回 422 `not_event_day`（ADR-0020）。
- 平移的本文與 `version` 一樣嚴格：欄位名稱完全相符、不可重複、只接受整數（不接受 `-0`、前導零、小數、字串）；`deltaMin` 可為負，範圍由領域規則檢查（±1440，否則 `invalid_delta`）；`excluded` 可省略，最多 200 個正整數，不在本活動的 id 會被忽略。預覽不帶 `version`。
- Master 的寫入都要帶 `version`（`sync-and-offline.md` 第 3 節）；寫入成功的回應直接附上新快照（`sync-and-offline.md` 第 1 節），總召不必等下一次輪詢。
- **略過、新增、指派、編輯、打卡、撤銷、重發的共通規則**：
  - 改到 Cue 的寫入（略過、新增、指派、編輯）帶 `version`、成功後 `eventVersion` + 1。就位打卡、撤銷、重發**不帶** `version`、**不改** `eventVersion`（ADR-0023）；本文有 `version` 欄位視為未知欄位（400）。
  - 只有略過是「復原上一步」的步驟（`cue.skip`）；其他寫入的操作紀錄型別為 `rundown.insert`、`rundown.assign`、`rundown.edit`、`checkin.set`、`token.reissue`、`token.revoke`（以復原碼換發 Master 為 `token.recover`），不以 `cue.` 開頭（ADR-0021）。操作紀錄不含 Token 或其雜湊。
  - 活動階段：這些寫入在 Draft 與 Live 都可以，因為活動日當天第一次推進前活動仍是 Draft（ADR-0020），總召可能需要在開始前臨時取消或加環節（US-M-6.3）。`POST /api/cues/undo` 因此在 Draft 也可用（Draft 階段唯一的 Cue 操作是略過）；推進與平移的階段規則不變。
- **略過**：目標須為 Pending（否則 `not_pending`）。
- **新增**：`start` 為 UTC 時間，須落在活動日 00:00（活動時區）到 `endsAt` 之間；`durationMin` 0～1440；`title` 1～100 字（去除前後空白後）；`notePublic`、`roleNote` 最多 500 字；不合格回 `invalid_cue`。插入位置之後的 Cue 都必須是 Pending 或 Skipped，也就是不能插在進行中或已完成的 Cue 前面（否則 `invalid_position`）。`afterCueId` 必填，所以新 Cue 不會成為第一個。
- **指派**：目標 Cue 須為 Pending（`not_pending`）；`roles` 最多 50 個、`roleId` 不可重複（400）；只能指派有自己 Cue 的七種類型（Stylist、Media、StageAV 與四種工作；Master 本來就看全部、Host 不看 Cue；否則 `invalid_assignment`）。新增時的 `roles` 同樣適用。
- **編輯**：只改標題與公開備註，限制同新增；任何狀態的 Cue 都可以。不改 `scheduledStart`、長度、固定（`access-control.md` 第 4 節）。
- **就位打卡**：設定值而不是切換，重複送出相同的值不產生新紀錄（US-X-5.4）。本人打卡的角色只取自 Token；代打卡的目標須為有自己 Cue 的七種類型（否則 `not_checkin_role`），寫入 `byRoleId` = Master。
- **歸屬先於一切檢查**：store 函式接收呼叫者的活動 id，以「id 且屬於該活動」查詢目標 Cue 或角色；查不到時回 404，**排在** `master_role`、`invalid_assignment`、`not_checkin_role`、`not_pending`、`invalid_position` 等任何依目標類型或狀態的檢查之前，否則別的活動的 id 會因回應不同而洩漏它的類型或狀態。
- **目標的 Token 不影響驗證結果**：撤銷、重發、代打卡、指派查詢的是**目標角色**，目標沒有有效 Token、Token 已撤銷都不是錯誤，絕不回 `store.ErrInvalidToken`、`store.ErrEventEnded` 或 `domain.ErrEnded`；`invalid` 與 `ended` 只描述**呼叫者自己**的 Token 與活動（第 2.1 節）。同理，新增 Cue 的 `start` 晚於 `endsAt` 回 `invalid_cue`，不得沿用 `domain.ErrEnded`。
- **撤銷與重發**：目標為 Master 時回 `master_role`（Master 只能以復原碼換發）；目標可以是 Host。重發在單一交易內撤銷舊 Token、建立新 Token；新 Token 的明文**只出現在這一次回應**，客戶端以 `location.origin + "/#t=" + token` 組成連結顯示，不存入本機快照或任何儲存。撤銷沒有有效 Token 的角色時照常成功（冪等，現有 `store.RevokeToken` 回 `ErrNotFound` 的行為要改）。兩台總召裝置同時重發同一角色時，後到的覆蓋前一個，先拿到的連結已失效（接受；畫面上的「連結最近使用時間」看得出新連結有沒有人開）。客戶端只把回應中的 `snapshot` 存入本機快照，`token` 只以文字加「複製」按鈕顯示，**不做成可點的連結**（總召在同一分頁點開會讓裝置變成該角色）。撤銷與重發的操作紀錄另記操作者使用的 `access_tokens.id`（不是雜湊），以復原碼換發 Master 連結後，才分得出哪些重發是外流的舊連結做的（`access-control.md` 3.4）。重發已寫入、但重新讀取快照失敗時回 500（第 7 節第 4 條），新 Token 的明文因此遺失；舊連結已失效，總召再按一次重發即可。
- **Rundown 編輯器、角色管理、匯入寫入的共通規則**：
  - **只限 Draft 的結構性修改**：`cues/update`、`cues/move`、`cues/delete`、`rundown/import`、`rundown/import/preview`、`roles/delete` 在 Live 回 422 `not_draft`，由 store 在交易內檢查，不只靠畫面（US-M-10.7）。Live 時的編輯只有有限編輯（`access-control.md` 第 4 節）。
  - **活動日當天鎖住兩項**（ADR-0032）：從活動日 00:00（活動時區）起，`rundown/import` 與 `roles/delete` 回 422 `locked_on_event_day`（`rundown/import/preview` 照常回應，讓畫面說明原因）；在交易內以伺服器時鐘判斷。當天仍可以逐一 `cues/update`、`move`、`delete`，以及新增、改名角色。判斷順序：歸屬（404）→ `not_draft` → `locked_on_event_day` → 其他檢查。
  - **版本**：改到 Cue 或指派的寫入（`cues/update`、`move`、`delete`、`rundown/import`、`roles/delete`）帶 `version`，成功後 `eventVersion` + 1；`roles/add`、`roles/rename` 不帶 `version`、不改 `eventVersion`（與重發相同，不影響任何人的 Cue；ADR-0023）。
  - **操作紀錄**：型別為 `rundown.update`、`rundown.move`、`rundown.delete`、`rundown.import`、`role.add`、`role.rename`、`role.delete`，都不以 `cue.` 開頭，不是「復原上一步」的步驟（ADR-0021）。被取代或刪除的內容**完整記錄**（ADR-0032）；`rundown.update` 另記改之前的欄位（`before`），活動日當天外流的 Master 連結逐一覆寫也查得出原本的內容：`rundown.delete` 記被刪 Cue 的欄位、指派與角色備註；`rundown.import` 記被取代的每個 Cue 的欄位、指派與角色備註，以及新建的角色；`role.delete` 記角色的名稱、類型、畫面、是否打卡與它的指派、角色備註；`role.add`、`role.rename` 記角色 id 與名稱。操作紀錄不進任何快照，隨活動一起刪除。**這些寫入的紀錄都另記操作者的 `byTokenId`**（`access_tokens.id`，不是雜湊；同重發與撤銷），以復原碼換發 Master 後，才查得出外流的舊連結建立、刪除或匯入了什麼（`access-control.md` 第 1 節、3.4）。都不含 Token、雜湊或 `origin`。
  - **歸屬先於一切檢查**與**目標的 Token 不影響驗證結果**（前述兩條）同樣適用於這一組帶 id 的端點：`cueId`、`afterCueId`、`roleId` 只在呼叫者的活動中尋找，查不到回 404，排在 `master_role`、`not_draft`、`invalid_*`、`duplicate_name`、`too_many_roles` 等檢查之前；重名與上限只以**呼叫者的活動**為範圍查詢，不得以目標 id 反查活動（否則 `duplicate_name` 會成為探測別的活動角色名稱的工具）；刪除、改名、預覽一個沒有有效 Token 的角色照常成功。
- **編輯 Cue**（`cues/update`）：整筆取代該 Cue 的開始時間、長度、固定、標題、公開備註；限制與新增相同（`start` 落在活動日 00:00 到 `endsAt` 之間、`durationMin` 0～1440、標題 1～100 字、備註最多 500 字，否則 `invalid_cue`）。`start` 直接寫入 `scheduledStart`：平移只在 Live（`opLiveOnly`），所以 Draft 的 `offsetMin` 一定是 0；store 仍在交易內確認 `offsetMin = 0`，不是 0 時回 500（程式錯誤，不該發生）。Pending 與 Skipped 的 Cue 都可以改（Draft 只有這兩種狀態）。指派與角色備註沿用 `POST /api/cues/roles`。
- **調整順序**（`cues/move`）：把 Cue 移到 `afterCueId` 之後，`null` 為移到第一個；`afterCueId` 等於 `cueId` 回 `invalid_position`。在單一交易內重排 `seq`，維持唯一、連續（`UNIQUE (event_id, seq)` 需兩段式更新，同新增）。只改順序，不改時間；順序與時間不一致時由 `outOfOrder[]` 提示（US-M-10.6）。移動與刪除後整份重新編號為 1..n，所以指令匯入時留下的間隔（例如 10、20、30）也會被整理。
- **刪除 Cue**（`cues/delete`）：刪除該 Cue 與其指派（外鍵 `ON DELETE CASCADE`），後面的 `seq` 依序遞補。不留墓碑；Live 時的「刪除」仍是略過。
- **新增 Cue 在 Draft 的放寬**：Draft 階段的 `POST /api/cues/insert` 可帶 `"afterCueId":null`，插在第一個；流程表是空的時只能帶 `null`。Live 帶 `null` 回 `invalid_position`，在 store 的交易內判斷活動階段；本文需要新的欄位種類「正整數或 null」。
- **復原上一步與 Rundown 編輯**：這些修改不是步驟，但可能讓上一個 Cue 操作無法復原（例如被略過的 Cue 已刪除、或整份已被匯入取代）；此時 `POST /api/cues/undo` 回 `undo_stale`（`domain.Undo` 找不到 Cue 就回 `ErrUndoStale`）。Master 快照的 `undo`：上一個 Cue 操作已無法復原時，與「沒有可復原的操作」相同，回 `{"available":false,"description":null}`（鍵集合不變），不顯示一個按了必定失敗的按鈕；判斷方式是對目前的 Cue 試跑 `domain.Undo`，回 `ErrUndoStale` 即為無法復原。
- **貼上匯入**：
  - `text` 為整份流程表文字，欄位與 `bethere import-rundown` 的 CSV 相同（`requirements/stories.md` US-M-0.2，含標題列）；第一列含 Tab 時以 Tab 分隔（從試算表複製），否則以逗號分隔。最多 200 列資料、100 KB；超過回 `invalid_import`。驗證與 `import-rundown` 共用同一個解析與驗證函式；這個函式補上與新增 Cue 相同的限制（標題 1～100 字、`notePublic` 與 `roleNote` 最多 500 字），讓匯入的 Cue 在編輯器原樣儲存不會失敗。寫入時依 CSV 的 `seq` 排序，重新編號為 1..n。
  - 預覽回 `rows[]`：`{"line","seq","title","start","durationMin","isFixed","roles":["…"],"roleNote","notePublic","errors":["…"]}`，`errors` 為人看得懂的中文原因；`missingRoles[]` 為文字中提到、但活動裡沒有的角色名稱（依出現順序、不重複）；`replaces` 為目前的 Cue 數量（畫面據此提示「現有的 N 個環節與指派將被取代」）。有任何一列錯誤時畫面不能確認。整份文字讀不懂時（缺少必要欄位、不認得的欄位、超過 100 KB 或 200 列，或只有標題列、沒有任何環節列），`fileError` 為原因、`rows` 為空陣列；匯入同樣的文字回 `invalid_import`，但排在版本、`not_draft`、`locked_on_event_day` 之後（只有標題列不得把流程表換成空的）。
  - **角色比對**：以該活動的**所有**角色（含 Master）比對名稱。對到 Master、Host 或不開放的類型就是錯誤列（與指派規則相同）；**只有完全對不到的名稱**才列入 `missingRoles`。缺少的名稱也要通過新增角色的名稱規則（不合格時該列標為錯誤）；建立後會超過 50 個角色時，預覽另回 `"tooManyRoles":true`，畫面不能確認。
  - 匯入在單一交易內：確認 `newRoles` 的名稱集合**恰好等於**此刻的 `missingRoles`（不多不少，否則 `invalid_import`，因為兩次請求之間別人可能已新增角色）→ `newRoles` 的名稱不可重複（400）、每一筆經過與 `roles/add` 相同的名稱檢查函式與上限檢查（`invalid_role`、`too_many_roles`）→ 建立 `newRoles`（不產生 Token，US-M-12.6）→ 刪除現有全部 Cue 與指派 → 寫入新的 Cue 與指派 → 一筆 `rundown.import` 紀錄 → `eventVersion` + 1。任一步失敗整份不寫入。`newRoles` 的 `view` 只能是 `HandsFree` 或 `Glance`（匯入的角色會被指派 Cue，平安儀表板不看 Cue）。
  - `bethere import-rundown` 維持「活動中尚無 Cue 才能匯入」（US-M-0.2a），不提供取代。
- **新增角色**：
  - `name` 去除前後空白後 1～50 字，不得含 `;`（CSV 以它分隔角色）、控制字元或零寬字元（否則 `invalid_role`），與同一活動的其他角色（含 Master）不可同名（`duplicate_name`）；一場活動最多 50 個角色（含 Master，`too_many_roles`）。改名的名稱規則相同。
  - **類型由伺服器依 `view` 決定**，總召不指定權限（ADR-0031）：`HandsFree` → Stylist、`Glance` → Media、`Dashboard` → Host。`Dashboard` 帶 `checksIn: true` 回 `invalid_role`。StageAV 只能以指令建立（快速瞄畫面以等寬字顯示備註的差異，見 `screen-design.md` 第 4 節）。
  - **`checksIn`** 存在欄位 `roles.checks_in`（可為空，ADR-0019；遷移 0003）：`NULL` 表示依類型（有自己 Cue 的七種類型打卡，Host 不打卡；既有角色與 `add-role` 建立的角色都是 `NULL`），`0` 表示不打卡；只寫入 `NULL` 或 `0`（`checksIn: true` 存成 `NULL`），遷移 0003 為 `ALTER TABLE roles ADD COLUMN checks_in INTEGER CHECK (checks_in IS NULL OR checks_in = 0)`，不帶 `bethere:foreign_keys=off`，只新增（ADR-0019）；解析時 Master、Host 一律不打卡，不理會這個欄位。打卡在同一交易內讀取這個欄位。退回到不認得這個欄位的舊版本時，`checksIn: false` 的角色又可以打卡（接受）。`checksIn: false` 的角色：本人打卡與代打卡都回 `not_checkin_role`，Master 快照的 `checkedIn` 為 `false`；仍然可以被指派 Cue。
  - 新增在單一交易內建立角色與 Token，回應附 `token` 與 `qr`，明文只出現這一次（同重發）。Token 在寫入前產生，QR Code 也在**寫入前**產生，產生失敗就不寫入、回 500；QR 函式庫的錯誤不得包含編碼內容（記錄的 err 裡不能有 Token）。新增已寫入、但重新讀取快照失敗而回 500 時，Token 明文遺失，總召在角色清單按「產生連結」即可（同重發）。Master 角色不能經此建立（`view` 不接受 `Console`）。
- **QR Code**：`origin` 是客戶端的 `location.origin`，最多 200 字元，以 `url.Parse` 解析後必須：scheme 為小寫的 `https`（`http` 只限主機為 `localhost` 或 `127.0.0.1`）；`User`、`Path`、`RawQuery`、`Fragment`、`Opaque` 全部為空；主機只含 `[a-z0-9.-]`；連接埠若有則為 1～65535；重新序列化後與輸入完全相同。`origin` 欄位一旦出現就必須通過檢查，空字串同樣 400，只有重發可以完全不帶。不符一律 400（擋下 `https://正式網域@evil.example` 這類讓 QR 指向別處、把 Token 交給攻擊者的值）；QR Code 的內容為 `origin + "/#t=" + token`，與畫面上的文字連結完全相同。以鎖定版本的函式庫在記憶體產生 PNG，以 data URI 回傳，不寫入磁碟、日誌或資料庫；客戶端只顯示，不存入本機快照或任何儲存（US-M-13.2）。`origin` 只用於這一次回應。
- **刪除角色**：只限 Draft；Master 回 `master_role`。同一交易內刪除角色列，其 Token、指派、打卡隨外鍵一併刪除，持有該連結的裝置下一次輪詢收到 `invalid`（查無 Token）。
- **改名**：Draft 與 Live 都可以；Master 可以改名。
- **角色預覽**：
  - 回應的鍵是 `preview`，**不是** `snapshot`：客戶端只把 `snapshot` 存入本機快照，`preview` 只在記憶體顯示，絕不寫入本機儲存、不影響 ETag 與輪詢（否則總召的裝置會把別人的快照當成自己的）。
  - `preview` 由第 7 節第 2 條的同一個快照產生函式，以**目標角色**的身分組出，內容與目標角色自己 `GET /api/snapshot` 拿到的相同（`serverTime` 除外），以測試比對；目標是 Host 時為彙總快照。目標為 Master 回 `master_role`。
  - 預覽只讀：不寫入任何資料與操作紀錄，不更新**目標**的 `lastSeenAt`（呼叫者自己的照常更新），不需要也不讀取目標的 Token。
  - 實作上，store 的預覽函式分開回傳「呼叫者的 `TokenOwner`」與「以目標組出的快照資料」；目標那一份的 `TokenID` 為 0、`Actor` 為零值（不帶任何憑證），API 層只對呼叫者呼叫 `seen`。目標是不開放的類型時回 404（與不存在相同），不回 500。前端預覽時不處理 `notice`，不寫入任何「已看過的提示」標記。
- **以復原碼換發 Master 連結**（`access-control.md` 第 2 節，US-O-3）：
  - 不讀取 `Authorization` 標頭，只認本文的 `recoveryCode`；比對前忽略空白、連字號與大小寫。
  - 復原碼不正確、格式不符、查無活動都回同一個 422 `wrong_recovery_code`，不透露活動是否存在。復原碼正確但活動已結束時回 422 `event_ended`；入口沒有 Token，所以不用 `result: ended`（第 2.1 節）。資料庫錯誤、逾時與取消一律 500，絕不回 `wrong_recovery_code`。
  - 單一交易內：再確認一次復原碼 → 活動未結束 → 撤銷 Master 的有效 Token、建立新的、刪除這場活動所有成員的主控台連結（10.2）→ 操作紀錄 `token.recover`（actor 為復原碼，只記角色 id）。與重發相同，不改 `eventVersion`（ADR-0023）。
  - 舊 Master 連結的裝置在下一次輪詢收到 `invalid`。新 Token 的明文只出現在這一次回應；`/recover` 頁面以文字加「複製」顯示，不做成可點的連結，不讀取網址片段，也不寫入任何儲存。
  - 不限頻（`access-control.md` 3.3：復原碼 128 位元）。維護模式下照常放行，是唯一放行的復原碼請求（`tech-stack.md` 2.1）。
- **以邀請碼建立活動**（`access-control.md` 第 2 節，US-M-17，ADR-0036）：
  - 不讀取 `Authorization` 標頭，只認本文的 `inviteCode`；比對規則與復原碼相同。
  - 先檢查名稱（1～50 字，沒有控制字元與隱形字元）與日期（`YYYY-MM-DD`，台灣的今天到兩年後），不合格回 422 `invalid_event`，不碰邀請碼。再檢查邀請碼：不正確、格式不符、已用、已過期都回同一個 422 `wrong_invite_code`。資料庫錯誤、逾時與取消一律 500，絕不回 `wrong_invite_code`。
  - 先以唯讀查詢確認邀請碼未過期，查無時不開寫入交易；符合時在單一交易內刪除邀請碼（已被別的請求刪掉時回 `wrong_invite_code`）→ 建立 Draft 活動（時區 `Asia/Taipei`）、Master 角色「總召」與其 Token、復原碼雜湊 → 操作紀錄 `create-event`（actor 為 `server`，只記 `inviteId`）。兩個請求同時用同一組碼，只有一個刪得到。
  - 建立最多處理 10 秒，逾時回 500 且不寫入；`/new` 等回應 20 秒，所以已寫入的建立幾乎都等得到回應。
  - Token 與復原碼的明文只出現在這一次回應；`/new` 頁面不讀取網址片段，也不寫入任何儲存。
  - 不限頻：邀請碼 128 位元，猜錯只走唯讀查詢。維護模式下一律回 503 `maintenance`，即使帶著 Master 的 Token（第 8 節）。
- **沒有 `POST /api/report`**：不提供不需憑證、會寫入日誌的回報端點。Android 驗證以截圖與手動紀錄。

## 4. 權限

角色類型**只取自** Token 所屬角色（`TokenOwner.RoleType`），不信任請求內容。**未列出的組合一律拒絕**（403）。

| 端點 | Master | Stylist | Media | StageAV | VenueLead、Runner、Reception、Cashier | Host | 其他類型 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `GET /api/snapshot` | Master 快照 | 角色快照 | 角色快照 | 角色快照 | 角色快照 | 彙總快照 | 403 |
| `POST /api/cues/advance` | ✔ | 403 | 403 | 403 | 403 | 403 | 403 |
| `POST /api/cues/undo` | ✔ | 403 | 403 | 403 | 403 | 403 | 403 |
| `POST /api/cues/shift/preview` | ✔ | 403 | 403 | 403 | 403 | 403 | 403 |
| `POST /api/cues/shift` | ✔ | 403 | 403 | 403 | 403 | 403 | 403 |
| `POST /api/cues/skip`、`insert`、`roles`、`edit` | ✔ | 403 | 403 | 403 | 403 | 403 | 403 |
| `POST /api/checkin` | 403 | ✔ | ✔ | ✔ | ✔ | 403 | 403 |
| `POST /api/roles/checkin`、`reissue`、`revoke` | ✔ | 403 | 403 | 403 | 403 | 403 | 403 |
| `POST /api/cues/update`、`move`、`delete`，`/api/rundown/import`、`import/preview`，`/api/roles/add`、`rename`、`delete`、`preview` | ✔ | 403 | 403 | 403 | 403 | 403 | 403 |
| `POST /api/cues/ack` | 403 | 403 | 403 | 403 | VenueLead ✔，其他 403 | 403 | 403 |
| `POST /api/tasks/add` | ✔ | 403 | 403 | 403 | Reception ✔，其他 403 | 403 | 403 |
| `POST /api/tasks/set` | ✔ | 403 | 403 | 403 | Runner ✔，其他 403 | 403 | 403 |
| `POST /api/tasks/delete`、`/api/guests/import`、`/api/checklist/items` | ✔ | 403 | 403 | 403 | 403 | 403 | 403 |
| `POST /api/guests/arrive` | ✔ | 403 | 403 | 403 | Reception ✔，其他 403 | 403 | 403 |
| `POST /api/checklist/check` | ✔ | 403 | 403 | 403 | Cashier ✔，其他 403 | 403 | 403 |

- **開放的類型**：Master、Stylist、Media、StageAV、Host 與四種工作（第 9 節）。資料庫中出現任何其他類型時，每個端點都回 403，不落入任何預設分支。
- 權限檢查在 API 層。store 只確認操作者的憑證仍有效且屬於同一活動，**不檢查角色類型**（`access-control.md` 第 5 節第 6 條），所以每個寫入端點都必須先通過本表。

## 5. 快照

### 5.1 共通欄位

| 欄位 | 說明 |
| --- | --- |
| `eventVersion` | 單調遞增整數 |
| `serverTime` | 產生快照時的伺服器時間 |
| `eventStatus` | `Draft` 或 `Live`（結束時回 `ended`，不會有快照） |
| `role` | `{"type":"Stylist","name":"新秘","view":"HandsFree","checksIn":true}`：自己的角色；`view` 為解析過預設值後的畫面（ADR-0025）；`checksIn` 為是否要就位打卡，解析過 `roles.checks_in` 的預設值（Master、Host 一律 `false`） |
| `eventDay` | 活動日期與時區 `{"date","timezone"}`，供 Draft 階段判斷「是否為活動日」與顯示日期（`screen-design.md` 4.5，ADR-0020）；Host 同樣帶 |

### 5.2 Master 快照（另含）

| 欄位 | 說明 |
| --- | --- |
| `event` | `{"name","date","timezone"}` |
| `cues[]` | 全部 Cue：`id, seq, title, scheduledStart, effectiveStart, durationMin, isFixed, offsetMin, status, actualStart, actualEnd, notePublic, roles[]`；`roles[]` 為 `{"roleId","name","roleNote"}` |
| `activeCueId` | 進行中的 Cue，沒有則為 `null` |
| `undo` | `{"available":bool,"description":"撤銷：推進『一進預備』"}`；描述由伺服器產生，沒有可復原的操作時 `available` 為 false、`description` 為 `null`；上一個 Cue 操作已無法復原時也一樣（第 3 節「復原上一步與 Rundown 編輯」）。推進的描述以「開始的那個 Cue」命名；推進的是最後一個 Cue（沒有開始新的）時，以「完成的那個 Cue」命名 |
| `roles[]` | `{"id","name","type","view","checksIn","checkedIn","lastSeenAt","hasLink"}`：`checkedIn` 為是否就位（Master 與 Host 一律 `false`）；`lastSeenAt` 為該角色連結最近一次驗證成功的時間，**截到分鐘**，沒有時為 null；`hasLink` 為是否有有效 Token。不含 Token 或其雜湊 |
| `outOfOrder[]` | 時間倒序警告 `{"cueId","prevCueId"}`（`data-model.md` 4.2 第 5 步，US-M-8.6） |
| `conflicts[]` | 固定環節衝突：`{"cueId","fixedCueId","overrunMin"}`（`data-model.md` 4.2） |
| `overrunMin` | 超時分鐘（`data-model.md` 4.3，ADR-0022）；活動未開始時為 0 |
| `maintenance` | 是否在維護模式（US-O-2.5）；一律帶，非維護時為 false |

### 5.3 角色快照（Stylist、Media、StageAV 與四種工作）

| 欄位 | 說明 |
| --- | --- |
| `cues[]` | **只有指派給自己的 Cue**：`id, seq, title, effectiveStart, durationMin, isFixed, offsetMin, status, notePublic, roleNote`（只有自己的 `roleNote`） |
| `notice` | 變更提示，取以下兩種中**較新的一則**（US-X-7.4），沒有時為 `null`；客戶端以 `at` 判斷是否已顯示過（`screen-design.md` 4.4）。<br>平移（或平移的復原）：`{"kind":"shift","at","deltaMin","changes":[{"cueId","fromStart","toStart"}]}`。<br>新指派：`{"kind":"assign","at","cueId","title","effectiveStart"}`，來自新增時的指派或改派，只算把**自己**加進去的那一次。<br>兩種都只看**目前仍指派給自己**的 Cue：`changes` 只列這些 Cue，指派之後又被移除的 Cue 不產生提示，避免揭露已不屬於自己的 Cue |
| `myCheckIn` | 自己是否已就位（bool） |
| `currentTitle` | 進行中 Cue 的標題，不論是否指派給自己，沒有時為 null；只有標題，不含時間、備註或 id（US-X-6.4）。Stylist 同樣帶，三種類型的鍵集合相同 |

### 5.4 彙總快照（Host）

| 欄位 | 說明 |
| --- | --- |
| `delayMin` | 延誤分鐘：下一個待辦 Cue 的 `offsetMin` + 超時分鐘（`data-model.md` 4.3，ADR-0026）；活動未開始時為 0 |
| `currentTitle` | 進行中 Cue 的標題（不論指派給誰），沒有時為 null；Host 唯一看得到的 Cue 資訊 |


### 5.5 一律不出現

Token 或其雜湊、復原碼雜湊、其他角色的 `roleNote`、未指派給自己的 Cue（Master 除外）、操作紀錄與內部 id（`action_logs`、`check_ins`）。各角色快照的**鍵集合**以測試固定（`tech-stack.md` 2.2「型別」）。

### 5.6 Draft 階段

非 Master 角色在 Draft 階段也回完整的角色快照，讓裝置在活動前就存好離線快照（`operations/runbook.md` 第 3 節）；畫面依 `screen-design.md` 4.5 顯示：活動日當天照常倒數，活動日以前顯示 Cue 清單，「活動尚未開始」只是窄條（ADR-0020）。

### 5.7 條件請求與伺服器時間

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
| 代打卡的目標不是有自己 Cue 的七種類型 | 422 `not_checkin_role` |
| 撤銷或重發的目標是 Master | 422 `master_role` |
| 刪除或預覽的目標是 Master | 422 `master_role` |
| Live 時的結構性修改（`cues/update`、`move`、`delete`、匯入、刪除角色） | 422 `not_draft` |
| `cues/move` 移到自己之後 | 422 `invalid_position` |
| 角色名稱空白或超過 50 字、`Dashboard` 要求打卡、`view` 不在允許的值 | 422 `invalid_role` |
| 角色名稱與同活動的角色重複 | 422 `duplicate_name` |
| 角色已達 50 個 | 422 `too_many_roles` |
| 匯入文字超過上限、任一列不合法、`newRoles` 與缺少的角色不完全相同 | 422 `invalid_import` |
| 打卡的角色 `checksIn` 為 false | 422 `not_checkin_role` |
| 活動日當天的匯入取代或刪除角色 | 422 `locked_on_event_day`（ADR-0032） |
| 本文的角色 id 不在呼叫者的活動裡 | 404 `not_found`，不帶 `result`（與 Cue id 相同） |
| `domain.ErrCueNotFound` | 404 `not_found`，不帶 `result` |
| 復原碼格式不符（`auth.ErrRecoveryCodeFormat`）或查無活動（`store.ErrInvalidRecoveryCode`） | 422 `wrong_recovery_code` |
| 復原碼正確但活動已結束 | 422 `event_ended` |
| 建立活動的名稱或日期不合格 | 422 `invalid_event` |
| 邀請碼格式不符（`auth.ErrRecoveryCodeFormat`）、查無、已用或已過期（`store.ErrInvalidInvite`） | 422 `wrong_invite_code` |
| `store.ErrNoActor`、`store.ErrCrossEvent`、`store.ErrNotFound` | 500 `internal`（端點先以呼叫者的活動查詢目標，查不到已回 404；這三者出現即為程式錯誤） |
| `domain.ErrMultipleInProgress`、`store.ErrUnknownOp`、資料庫錯誤、逾時 | 500 `internal` |
| 其他 | 500 `internal` |

**帶 id 的請求**：store 先檢查操作者與目標是否同一活動（`ErrCrossEvent`），再檢查活動是否結束，所以別的已結束活動的 id 不會回 `ended` 而清掉呼叫者的資料（以測試固定）。平移的 Cue id 只在呼叫者自己活動的 Cue 中尋找，屬於別的活動的 id 回與「不存在」相同的 404，不帶 `result`。`cueId`、`afterCueId` 與角色 id（`roleId`）同樣只在呼叫者自己的活動中尋找。

## 7. 伺服器端規則

1. **每個請求**：解析標頭 → 查 Token（`store.LookupToken`）→ 依第 4 節檢查權限 → 執行。
2. **快照只有一個產生函式**：`GET`、寫入成功、409 三處都呼叫同一個函式，它在**單一唯讀交易**內查 Token 並組出**呼叫者自己角色**的快照，結果依第 6 節對照。在同一交易內查 Token 與組快照，撤銷與結束才會立即生效、沒有毫秒級的空窗；只有一個產生函式，避免 409 或寫入回應意外帶出 Master 快照。store 提供這個函式（`LookupToken` 自己開關交易，分兩次呼叫會留下空窗）；以「兩步之間撤銷或結束」的測試驗證。`GET` 在同一交易內先讀資料世代，世代沒變時查詢結果與活動資料取自記憶體（ADR-0041）；任何寫入都會改變世代，所以結果與重讀一次相同。
3. **處理順序**：解析標頭 → 查 Token → 權限（403）→ 解析本文（400）→ 寫入 → 重新讀取快照。409 只會在通過權限檢查後出現。
4. **寫入成功後重新讀取的結果**：讀到 `ok` 回 200 與新快照；讀到 `invalid` 或 `ended`（例如寫入與讀取之間被撤銷）就回那個結果；資料庫錯誤回 500（寫入已生效，客戶端下一次輪詢會看到）。一個回應**絕不同時**帶 `error` 與 `result`；409 附的快照同樣依此處理，讀不到 `ok` 就改回對應的 `result` 或 500。
5. **`lastSeenAt`**：只在 Token 驗證成功之後於記憶體更新（`tech-stack.md` 2.1「lastSeenAt」）；維護模式下被擋的請求到不了處理函式，所以不會更新。以 **`access_tokens.id`** 為鍵（`TokenOwner` 帶出這個 id，不是雜湊）；Master 快照只對每個角色**目前有效的** Token 查詢，所以撤銷或重發後，舊連結的使用時間（包括撤銷前一刻才通過驗證的請求）不會算到新連結上，以復原碼換發的 Master 連結也一樣。
6. **所有寫入**都帶 store 的 `Actor`，在同一交易內重新確認憑證並寫入操作紀錄。
7. **指令（`bethere` 子命令）**：
   - `serve`：`BETHERE_INIT` 只接受 `1`（建立新資料庫）或未設定／空字串；其他值一律拒絕啟動（避免 `BETHERE_INIT=0` 之類的值被當成「要建立」而在還原失敗時建出空庫，ADR-0012）。
   - `create-event`、`add-role`、`import-rundown`、`create-invite`、`reset-recovery-code`、`issue-admin-link` 一律以「不建立新資料庫」的方式開啟；連結、復原碼與邀請碼只印到終端機（stdout），不經過結構化日誌。
   - `add-role` 與 `import-rundown` 以**總召的身分**寫入：執行時提示輸入 Master 連結（不回顯），從標準輸入讀取，**絕不從命令列參數或環境變數讀取**，再經只查 `access_tokens` 的 `LookupRoleToken` 取得操作者（主控台連結當作查無，10.2）（不留在 shell 歷史與程序清單）。不新增任何「伺服器操作者」，維持「操作者只能來自驗證過的憑證」；操作紀錄記為 Master。
   - `import-rundown` 在單一交易內寫入全部 Cue 與指派，任一列不合法就整份不寫入（US-M-0.2）。
   - `issue-admin-link`（US-O-9.1）不讀取任何憑證：能在主機上執行就是開發者。單一交易內刪掉舊的管理員連結、存入新的雜湊，連結只印到 stdout。
   - `reset-recovery-code`（US-O-4）以同樣的方式讀取 Master 連結**或**目前的復原碼，store 在單一交易內重新確認憑證、活動未結束，以 Master 連結時再確認角色類型是 Master，然後換掉復原碼雜湊並寫操作紀錄（`recovery-code.reset`）。新碼先印出再寫入：印出失敗時什麼都不改（沒人看到的碼絕不能取代手上可能唯一的憑證，RISK-19）；寫入失敗時以錯誤結束並說明印出的碼無效。

## 8. 安全邊界相關的限制

- **維護模式**（`tech-stack.md` 2.1「維護模式」，US-O-2）：所有路由之前的單一關卡。路徑（整理後）在 `/api` 底下時，只放行 `POST /api/recover/master`，以及 Bearer Token 查得到、類型為 Master 的請求；其他一律回 503 `maintenance`，包括其他角色、查無或已結束的連結、沒有或格式不符的標頭、資料庫錯誤、`/api/ping` 與不存在的路徑，絕不回 `invalid` 或 `ended`（還原後不存在的連結因此不會清掉裝置資料）。被擋的請求不到處理函式，不更新 `lastSeenAt`。放行的 Master 請求由處理函式再查一次 Token；兩次之間 Master 連結剛好被換發時回 `invalid`，與平常相同。`POST /api/events` 不看 Token，由處理函式自己在維護模式下回 503 `maintenance`，所以帶著 Master 的 Token 也建立不了活動。系統後台的端點同樣被擋（管理員連結不是 Master），回 503 `maintenance`。
- 第 2、4、6 節屬於「Token 權限與資料外洩」邊界：實作完成後由獨立審查依第 4 節權限表的**每一格**、第 6 節的**每一列**與第 5 節的鍵集合驗證。
- 必要的測試：
  - 資料庫中直接插入未開放的角色類型，每個端點都回 403。
  - Token 的雜湊規則以固定的測試向量釘住，指令與伺服器共用；另有一個測試把指令產生的連結送進 HTTP 處理器，預期 `ok`（雜湊規則不一致會讓所有裝置同時被清除）。
  - 查詢與寫入時 context 已逾時或已取消，結果都**不是** `invalid`。
  - 404、405、本文過大、400、403、409、500 的回應都有 `no-store` 且不帶 `result`。
  - `POST /api/report` 回 404 或 405。
  - `BETHERE_INIT` 為 `0`、`true`、` 1`、`yes` 時拒絕啟動。
  - 系統後台（US-O-9、US-O-10）：
    - 沒有標頭、格式不符、查無、總召與其他角色的連結、被取代的管理員連結，兩個端點都回同一個 404 `not_found`，本文格式錯誤也一樣；管理員連結打角色端點回 `invalid`。
    - 總覽的鍵集合固定，不含任何雜湊；發出的邀請碼送進 `POST /api/events` 可以建立活動；日誌不含管理員連結或邀請碼。
    - 24 小時內第 21 組回 422 `too_many_invites`；用掉的碼與重新產生的連結都不讓額度恢復；同時送出的請求不超過上限。
    - 維護模式下兩個端點都回 503 `maintenance`。
  - 建立活動（US-M-17）：
    - 維護模式下，不帶 Token 與帶 Master 的 Token 都回 503 `maintenance`，邀請碼仍可用。
    - 不正確、格式不符、已用、已過期的邀請碼都回同一個 422 `wrong_invite_code`；取消的請求與資料庫錯誤不回 `wrong_invite_code`。
    - 猜錯的邀請碼在寫入連線被佔用時仍立即回 `wrong_invite_code`（不等寫入）。
    - 多個請求同時用同一組碼，只建立一場活動。
    - 名稱或日期不合格回 `invalid_event`，之後同一組碼仍可建立；台灣時間 00:30 時前一天的日期被拒。
    - 日誌與操作紀錄不含邀請碼、Token、復原碼或它們的雜湊；指令印出的邀請碼送進 HTTP 處理器可以建立活動。
  - Cue 指派、打卡、撤銷、重發、ETag：
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
  - Rundown 編輯器、角色管理、匯入：
    - 第 4 節新增的每一格（含 Host、未開放類型呼叫每個新端點都回 403）。
    - Live 時 `cues/update`、`move`、`delete`、匯入、預覽匯入、刪除角色都回 `not_draft`，資料不變。
    - 帶別的活動的 Cue id、角色 id（含別的活動的 Master、別的**已結束**活動的角色）回 404 且不帶 `result`，不回 `master_role`、`not_draft`、`invalid_position`。
    - 刪除、改名、預覽 Token 已撤銷的角色回 200，總召不會收到 `invalid`；刪除角色後，該角色原本的 Token 回 `invalid`。
    - 預覽：回應沒有 `snapshot` 鍵；`preview` 與目標角色自己的快照（`serverTime` 除外）相同，七種有自己 Cue 的類型與 Host 各一；預覽後目標的 `lastSeenAt` 不變、操作紀錄筆數不變；前端：`preview` 不進本機儲存。
    - 新增角色與帶 `origin` 的重發：`token`、`qr` 只出現在這一次回應；QR Code 解碼後等於 `origin + "/#t=" + token`；`origin` 帶路徑、查詢、片段、`javascript:` 或超過 200 字元回 400；操作紀錄不含 Token、雜湊或 `origin`；前端：`token` 與 `qr` 不進本機儲存。
    - 匯入：任一列錯誤、`newRoles` 多一個或少一個時整份不寫入，現有 Cue 與角色不變；成功時舊 Cue 全部消失；匯入後原本可復原的略過變成 `undo` 為 `null`，`POST /api/cues/undo` 回 `undo_stale`。
    - `checksIn: false` 的角色本人打卡、被代打卡都回 `not_checkin_role`。
    - `cues/move` 後 `seq` 唯一且從 1 連續；`cues/delete` 後同樣。
    - Draft 的 `offsetMin` 不為 0 時，`cues/update` 回 500 而不是寫入（以直接改資料庫模擬）。
    - 所有這些寫入的操作紀錄都有操作者的 `byTokenId`。
    - 活動日當天（以注入的時鐘）`rundown/import` 與 `roles/delete` 回 `locked_on_event_day` 且資料不變；前一天 23:59 可以。
    - `rundown.import`、`rundown.delete`、`role.delete` 的紀錄足以重建被取代或刪除的 Cue、指派與角色（以紀錄重建後比對原資料的測試固定），且不含 Token 或雜湊。
    - 匯入：CSV 寫到 Master 的名稱是錯誤列，不進 `missingRoles`；`newRoles` 有重複名稱回 400；缺少的名稱含 `;` 或超過 50 字時該列為錯誤；從有間隔的 `seq` 匯入後為 1..n。
    - 改名別的活動的角色 id、並帶上與該活動某角色相同的名稱，回 404 而不是 `duplicate_name`。
    - 預覽從未發過連結的角色（匯入時建立的）回 200；預覽不開放類型的角色回 404。
    - `origin` 為 `https://a@b.example`、含反斜線、大寫 scheme、`http://` 加公開主機時回 400。

## 9. 現場工作：主持、領班、機動組、迎賓、禮金

四種「工作」各是一個角色類型（ADR-0035）：`VenueLead`（領班）、`Runner`（機動組）、`Reception`（迎賓）、`Cashier`（禮金）。它們和 Stylist、Media、StageAV 一樣**有自己的 Cue**：可被指派、讀自己的 Cue、可打卡、畫面是快速瞄或免手持（預設快速瞄）、角色快照的形狀相同（5.3）。每種多一項工作，只出現在快速瞄畫面。主持不是新類型，是一般的快速瞄角色，靠 9.1 的延誤看新人那邊的進度。

這些寫入**不帶也不改** `eventVersion`（與打卡相同，ADR-0023）：彼此不衝突，同一筆資料後到的覆蓋先到的。回應附呼叫者自己的新快照。都寫入操作紀錄，`type` 不以 `cue.` 開頭，不是「復原上一步」的步驟。

### 9.1 延誤（所有角色快照）

角色快照新增 `delayMin`，算法與 5.4 相同；Draft 時為 0。快速瞄畫面在「目前」那一行旁顯示「一切順利」或「延誤 N 分」（未滿 5 分為一切順利，ADR-0026）。

### 9.2 新增角色時選工作

`POST /api/roles/add` 新增選填欄位 `job`：`"general"`（預設）、`"VenueLead"`、`"Runner"`、`"Reception"`、`"Cashier"`。`general` 依 `view` 決定類型（`HandsFree` → Stylist、`Glance` → Media、`Dashboard` → Host）；其他值的類型就是 `job`，`view` 只能是 `HandsFree` 或 `Glance`，否則 422 `invalid_role`。`bethere add-role` 也接受這四種類型。匯入時缺少的角色一律建為 Media（不變）。

### 9.3 回覆收到（VenueLead）

| 方法與路徑 | 權限 | 本文 | 成功回應 |
| --- | --- | --- | --- |
| `POST /api/cues/ack` | VenueLead | `{"cueId":n,"acked":bool}` | `ok` + 自己的角色快照 |

- Cue 不在呼叫者的活動裡 → 404；在活動裡但沒有指派給呼叫者 → 422 `not_assigned`（指派與否只在確認同一活動之後才判斷）。Cue 的狀態不限。
- 儲存在 `cue_acks`（`data-model.md` CueAck）：`acked: true` 寫入或保留第一次的時間，`false` 刪除；與現狀相同時不寫入。
- 快照：角色快照的 `cues[]` 每個 Cue 新增 `ackedAt`（只有自己的；不是 VenueLead 時一律 `null`）；Master 快照 `cues[].roles[]` 新增 `ackedAt`。
- 移除指派（`cues/roles`、刪除 Cue、匯入取代）時，同一交易內刪除該角色對該 Cue 的回覆，再指派回來也不會出現舊的回覆。

### 9.4 任務池（Runner、Reception）

| 方法與路徑 | 權限 | 本文 | 成功回應 |
| --- | --- | --- | --- |
| `POST /api/tasks/add` | Master、Reception | `{"title":"…"}` | `ok` + 自己的快照 |
| `POST /api/tasks/set` | Master、Runner | `{"taskId":n,"state":"open"或"claimed"或"done"}` | `ok` + 自己的快照 |
| `POST /api/tasks/delete` | Master | `{"taskId":n}` | `ok` + 新的 Master 快照 |

- 標題 1～100 字（規則同 Cue 標題）；每場最多 200 個任務，超過回 422 `too_many_tasks`。
- 刪除任務時，被刪任務的完整欄位寫入操作紀錄（ADR-0032）。刪除角色時，它認領中的任務回到 `open`。取代名單、取代檢核表、刪除任務的紀錄都記下操作者使用的 `access_tokens.id`（`byTokenId`）。
- 狀態轉換：Runner 只能把 `open` 改成 `claimed`（認領者為自己）、把**自己認領的**改回 `open` 或改成 `done`；別人已認領的回 422 `task_taken`，其他不允許的轉換回 422 `invalid_task_state`。Master 可以把任何任務改成任何狀態；改成 `open` 時清除認領者，改成 `claimed` 回 422 `invalid_task_state`（Master 不替人認領）。
- 快照的 `tasks`：每項 `{"id","title","createdBy","createdAt","claimedBy","state"}`，`createdBy`、`claimedBy` 是角色名稱（角色已刪除時為 `null`），依建立時間由新到舊。
  - Master：全部任務。
  - Runner：所有 `open` 與 `claimed` 的任務，加上自己完成的最近 20 個。
  - Reception：自己建立的任務。
  - 其他角色：`tasks` 為 `null`。

### 9.5 賓客名單（Reception）

| 方法與路徑 | 權限 | 本文 | 成功回應 |
| --- | --- | --- | --- |
| `POST /api/guests/import` | Master | `{"text":"…"}` | `ok` + 新的 Master 快照 |
| `POST /api/guests/arrive` | Master、Reception | `{"guestId":n,"arrived":bool}` | `ok` + 自己的快照 |

- 匯入取代整份名單。每行一位：`姓名`、`桌號`、`備註`，以 Tab（從試算表複製）或逗號分隔，備註可省略；第一列若是 `姓名` 開頭就當標題列略過；空行略過。姓名 1～50 字、桌號 1～20 字、備註最多 100 字，規則同角色名稱（不含控制字元與看不見的格式字元）；最多 1000 位、文字最多 100 KB（本文上限 256 KB）。任一列不合格、或沒有任何一位（例如只有標題列），整份不寫入，回 422 `invalid_import`。已有任何賓客報到時不能取代，回 422 `guests_arrived`；從活動日 00:00（活動時區）起不能取代，回 422 `locked_on_event_day`（ADR-0032）。
- 只蒐集姓名、桌號與備註；隨活動一起刪除（NFR-5）。被取代的名單完整寫入操作紀錄（內部資料表，不進任何快照，隨活動刪除），需要時由開發者救回。
- 快照的 `guests`：Master 與 Reception 為全部賓客 `{"id","name","table","note","arrived"}`，依匯入順序；其他角色為 `null`。搜尋在客戶端進行。
- 本機快照含賓客名單時，活動日結束（活動時區 24:00）後 2 天就在開啟頁面時刪除，不論活動是 Draft 或 Live（NFR-5）。

### 9.6 檢核表（Cashier）

| 方法與路徑 | 權限 | 本文 | 成功回應 |
| --- | --- | --- | --- |
| `POST /api/checklist/items` | Master | `{"text":"…"}` | `ok` + 新的 Master 快照 |
| `POST /api/checklist/check` | Master、Cashier | `{"itemId":n,"done":bool}` | `ok` + 自己的快照 |

- 項目取代整份，每行一項，空行略過；每項 1～100 字，1～30 項（沒有任何一項回 422 `invalid_checklist`，不會清空）。已有任何項目完成時不能取代，回 422 `checklist_started`；從活動日 00:00 起不能取代，回 422 `locked_on_event_day`。被取代的項目完整寫入操作紀錄。
- 快照的 `checklist`：Master 與 Cashier 為全部項目 `{"id","title","done","doneBy"}`，`doneBy` 為完成者的角色名稱；其他角色為 `null`。

### 9.7 快照的新鍵

- 角色快照（5.3，七種有自己 Cue 的類型共用同一個鍵集合）新增 `delayMin`、`tasks`、`guests`、`checklist`；`cues[]` 新增 `ackedAt`。
- Master 快照（5.2）新增 `tasks`、`guests`、`checklist`（一律是陣列）；`cues[].roles[]` 新增 `ackedAt`。
- Host 快照不變，不含任何任務、賓客或檢核表資料。

### 9.8 錯誤（追加到第 6 節）

| 來源錯誤 | 回應 |
| --- | --- |
| 回覆的 Cue 沒有指派給呼叫者 | 422 `not_assigned` |
| 任務已被別人認領 | 422 `task_taken` |
| 不允許的任務狀態轉換 | 422 `invalid_task_state` |
| 任務已達 200 個 | 422 `too_many_tasks` |
| 任務標題不合格 | 422 `invalid_task` |
| 賓客名單不合格 | 422 `invalid_import` |
| 已有賓客報到時取代名單 | 422 `guests_arrived` |
| 檢核表項目不合格 | 422 `invalid_checklist` |
| 已有項目完成時取代檢核表 | 422 `checklist_started` |
| 任務、賓客、檢核項目的 id 不在呼叫者的活動裡 | 404 `not_found` |

**歸屬先於一切檢查**（同第 3 節）：`cueId`、`taskId`、`guestId`、`itemId` 只在呼叫者的活動中查詢，查不到回 404，排在 `not_assigned`、`task_taken`、`invalid_task_state`、`guests_arrived`、`checklist_started` 之前；別的已結束活動的 id 同樣回 404。任務、賓客、檢核項目的 id 以 AUTOINCREMENT 產生，取代整份後也不會重用，離線裝置送出的舊 id 只會得到 404。

## 10. 婚顧公司（US-CO-1～US-CO-4、US-O-11，ADR-0039）

### 10.1 端點

公司頁面的端點以 `Authorization: Bearer <登入連結>` 驗證（格式同第 1 節的 Token）；成員與公司一律取自登入連結，請求不帶公司編號。沒有或格式不符的標頭、查無、過期、登出或成員已移除的登入連結、登入信連結、任何角色或管理員的連結，一律回同一個 404 `not_found`，先於本文檢查（同系統後台，US-O-9.1）。`login/confirm` 反過來只接受登入信連結。

| 方法與路徑 | 憑證 | 本文 | 成功回應 |
| --- | --- | --- | --- |
| `POST /api/company/login` | 無（不讀取標頭） | `{"email":"…"}` | `{"result":"ok"}`：不論是不是成員、是否寄出（US-CO-1.3） |
| `POST /api/company/login/confirm` | 登入信連結 | `{}` | `{"result":"ok","loginToken":"…"}`：新的登入連結，只在這次回應；查無、用過、過期、成員已移除一律 404 `not_found`（US-CO-1.10） |
| `POST /api/company/logout` | 登入連結 | `{}` | `{"result":"ok"}`：作廢這一條登入連結 |
| `GET /api/company` | 登入連結 | — | `{"result":"ok","organization":{"name":"…"},"me":n,"members":[{"id":n,"email":"…"}],"events":[{"id":n,"name":"…","date":"YYYY-MM-DD","status":"Draft\|Live\|Ended"}]}`；`events` 依 US-CO-3.1 排序 |
| `POST /api/company/members/add` | 登入連結 | `{"email":"…"}` | 同 `GET /api/company` |
| `POST /api/company/members/remove` | 登入連結 | `{"memberId":n}` | 同 `GET /api/company` |
| `POST /api/company/events` | 登入連結 | `{"name":"…","date":"YYYY-MM-DD"}` | `{"result":"ok","eventId":n,"masterToken":"…","recoveryCode":"…"}`，與 `POST /api/events` 相同的明文規則（第 3 節） |
| `POST /api/company/events/open` | 登入連結 | `{"eventId":n}` | `{"result":"ok","consoleToken":"…"}`：這位成員對這場活動的新主控台連結（`access-control.md` 第 7 節），頁面以 `/#t=<consoleToken>` 開啟 |
| `POST /api/admin/organizations` | 管理員連結 | `{"name":"…","email":"…"}` | `{"result":"ok","id":n}`（US-O-11.1） |

`GET /api/admin/overview` 另帶 `organizations:[{"id":n,"name":"…","members":n,"events":n,"createdAt":…}]`，`events[]` 另帶 `organization`（名稱，不屬於任何公司時為 `null`）；不帶任何 email（US-O-11.3）。

### 10.2 規則

- **主控台連結的解析**：加在 store 的 `resolveToken`（`LookupToken`、`Snapshot`、`PreviewSnapshot` 共用，在同一個讀取交易內）：先查 `access_tokens`，查不到時再以單一查詢確認主控台連結存在、成員仍在、活動屬於成員的公司，解析出那場活動的 Master 角色；`TokenOwner` 帶出新的操作者種類「成員」（成員編號與主控台連結的編號），`TokenID` 為 0。所以輪詢、寫入後的快照、平移與預覽都認得它（US-CO-4.8）。兩段查詢都成功、都查無，才是 `ErrInvalidToken`；第二段查詢出錯時是 500，以測試固定。因此要改的既有程式：
  - store 寫入前確認操作者的函式（`checkActor`）在期待角色操作者的地方也接受「成員」：在同一交易內以同樣的單一查詢再確認一次，查不到就是 `ErrInvalidToken`。只接受復原碼的寫入（第 2 節的四件事）仍然拒絕「成員」。
  - API 層凡以 `TokenID == 0` 判斷「呼叫者查不到」的地方（例如角色預覽），改以「是否已解析出呼叫者」判斷；以測試固定：用主控台連結預覽不存在的角色回 404，不是 500。
  - `lastSeenAt` 只屬於 `access_tokens`：`TokenID` 為 0 時 `LastSeen` 一律回報沒看過，主控台連結的使用不會算到任何角色連結。
  - 操作紀錄的 `byTokenId` 為 0（不是任何角色連結），`actor` 記 `member:<成員編號>/console:<主控台連結編號>`（取代第 4 節「都有操作者的 `byTokenId`」對這種請求的要求）。
  - 第 4 節權限表的 Master 欄、第 7 節與第 8 節的 Master 規則照常適用；主控台連結不在 Master 快照的角色連結裡。
- **伺服器指令**（第 7 節）讀入「Master 連結」時用只查 `access_tokens` 的 `LookupRoleToken`，主控台連結當作查無；`LookupToken` 只給 API 用。`find-member`、`add-member`、`remove-member`、`issue-login-link`（US-O-12）不讀取任何憑證，與 `issue-admin-link` 相同；`issue-login-link` 照 `login/confirm` 的後半段產生登入連結（`verified` 為 1），`-base-url` 規則同 `issue-admin-link`，不受寄信的上限。
- 本節的每個寫入端點（`logout` 除外）都在單一寫入交易內：再確認登入連結有效、呼叫者仍是成員 → 檢查 → 寫入；交易內查不到時回 404 `not_found`。
- `login`：
  - 依序：email 格式（同 US-O-11.2）不符回 422 `invalid_email` → 沒有可用的寄信設定回 503 `mail_unavailable` → 以一次唯讀查詢找成員 → 是成員時把成員編號放進寄信佇列 → 回 `{"result":"ok"}`。兩條路都只做這一次唯讀查詢、都不寫入，回應的內容與做的事不分是不是成員（回應時間不另外保證）；匿名的請求不會寫進資料庫。
  - 寄信佇列：放的是成員編號，同一位成員同時最多一件（已在佇列裡就不再放；背景工作取出時就不算在佇列裡），最多 100 位，滿了就不放。單一個背景工作依序處理每一件：
    1. 先以唯讀查詢確認成員仍在、他最近 24 小時的 LoginMail 不到 10 列、他公司最近 24 小時的不到 100 列、全系統最近 24 小時的不到 400 列；任一項不成立就不寄、不寫入；
    2. 都成立時存入一列 LoginMail（成員、公司、新登入信連結的雜湊，15 分鐘後失效）。LoginMail 只有這個背景工作會寫，讀與寫之間不會多出別的列，所以不必再確認；成員剛好被移除時，外鍵讓存入失敗，算一次寄信失敗（種類 `store`）；
    3. 提交後寄信（逾時 30 秒）；失敗時那一列留著、照樣算進上限，所以寄信帳號壞掉時重試次數也有上限，寫入每天最多 400 次。
  - 寄信失敗、因上限或佇列已滿而不寄的件數只累計，由計時器每分鐘最多以 `mail` 開頭記錄一行：三種各自的件數，與最後一次寄信錯誤的種類：有 SMTP 回應時記狀態碼與增強狀態碼，沒有時記 `timeout`、`connect`、`tls` 或 `canceled`，寫不進資料庫時記 `store`（一律不記錯誤原文，原文可能帶收件人 email；絕不記錄連結）；寫出後件數歸零，三種都是 0 時不寫。所以外人無法以請求灌爆日誌。
  - 計數一律數 LoginMail 的列、只用建立時間的下界，不看 `tokenHash` 是否為空。
  - 服務停止時，依序：佇列裡的不寄、不算進彙總；正在寄的那件以關閉連線取消，算一次寄信失敗（種類 `canceled`），那一列照樣算進上限；背景工作結束；寫出剩下的彙總；關閉資料庫。成員再要一次即可。當機時記憶體裡的件數會遺失。
  - 信裡的連結是 `<網域>/login#t=<登入信連結>`，網域取自 `BETHERE_BASE_URL`，不取自請求的 `Host`。
- `login/confirm`：單一寫入交易：找到登入信連結、`usedAt` 為空、未過期、成員仍在 → 填 `usedAt` → 若他已有 20 條有效登入連結就刪掉最舊的 → 存入新登入連結的雜湊（`verified` 為 1）。兩個請求同時用同一條登入信連結時只有一個成功。
- **寄信的設定**（`tech-stack.md`「寄信」）：
  - `BETHERE_MAIL=smtp`：以 `BETHERE_MAIL_FROM`、`BETHERE_MAIL_PASSWORD` 經 `smtp.gmail.com:587`（STARTTLS）寄出；`BETHERE_BASE_URL` 必須是 `https://<主機>`，不帶路徑。
  - `BETHERE_MAIL=log`：不寄信，把收件人與連結寫進服務日誌（US-CO-1.14）；`BETHERE_BASE_URL` 必須是 `http://localhost` 或 `http://127.0.0.1`（可帶埠號）。
  - 未設定、其他值、缺任何一項或網域不合規則時，都當作沒有可用的寄信設定：以 `mail` 開頭記錄原因、照常啟動，`login` 回 503 `mail_unavailable`。寄信設定絕不讓服務起不來（同客服信箱）。
- **測試期間的連結**（US-CO-1.13）：MemberSession 與 ConsoleToken 各加一個可為空、**沒有預設值**的欄位 `verified`（ADR-0019），所以只需 email 的版本照它的寫法插入的列一定是空的（以測試固定：照 M4 的 INSERT 寫入的列被拒）。這個版本產生的一律填 1；解析時只接受 1，空的當作查無。所以換上這個版本時，之前的連結全部失效；退回只需 email 的版本期間產生的，換回後也不接受。每日刪除清掉 `verified` 為空的列。`bethere features` 列出 `mail-login`，部署與準備腳本據此拒絕只需 email 的版本（`tech-stack.md`「寄信」）。
- `members/remove`：對象不屬於本公司回 404；是自己回 422 `cannot_remove_self`；刪除成員（登入連結與主控台連結隨外鍵一併刪除）。
- `events/open`：活動不屬於本公司（含不存在、不屬於任何公司）回 404；這位成員對這場活動已有 10 條主控台連結時刪掉最舊的一條，再存入新的。活動已結束時照樣產生，開啟後照第 2.1 節回 `ended`。
- `POST /api/company/events` 的名稱與日期檢查同 `POST /api/events`（第 3 節），不合格回 422 `invalid_event`；交易內數這家公司建立時間晚於「現在減 24 小時」的活動（只用下界），達 20 場回 422 `too_many_events` → 建立活動（`organizationId` 為本公司）→ 操作紀錄 `create-event`（actor 為成員）。
- `POST /api/admin/organizations`：交易內以同樣的下界數建立的公司，達 5 家回 422 `too_many_organizations`。
- 以復原碼換發 Master 連結（第 3 節）在同一交易內刪除這場活動所有的主控台連結。
- **從備份還原**（US-O-12.5、US-O-12.6）：
  - 「成員連結待清除」標記是資料庫所在目錄下的 `member-reset.pending`（正式為 `/var/lib/bethere/member-reset.pending`；路徑由 `-db` 推得，測試服務各自獨立）。準備腳本在 `litestream restore` **之前**與 `maintenance.forced` 一起建立並 `sync`，所以還原中途當機也不會少了它；`maintenance.sh off` 與 `init-db.sh` 不碰它。
  - `serve` 開啟資料庫後、開始接受請求前讀一次這個標記（標記只會在服務停止時建立）。存在時，在單一交易內刪除所有登入連結與主控台連結，並清空所有 LoginMail 的登入信連結雜湊（列留著，計數不變），提交後刪掉標記；交易或刪標記失敗時以 `member-reset` 開頭記錄錯誤、照常啟動，這次執行視為「待清除」。交易提交後、刪標記前當機，下次啟動再清一次，結果相同。
  - 「待清除」時，所有路由之前的單一檢查（不論是否維護模式）讓本節的公司端點與查得到的主控台連結（不論 `verified`）的請求一律回 503 `maintenance`；查不到的連結照常回 `invalid`。
- 維護模式（第 8 節）：本節所有端點與 `/api/admin/` 在所有路由之前回 503 `maintenance`，不論標頭帶的是哪一種連結（登入不讀標頭，帶 Master 連結也不放行）；以主控台連結呼叫的 Master 端點照 Master 的規則放行。

### 10.3 錯誤（追加到第 6 節）

| 來源錯誤 | 回應 |
| --- | --- |
| email 格式不符（登入、加入成員、建立公司） | 422 `invalid_email` |
| 主機沒有可用的寄信設定（登入） | 503 `mail_unavailable` |
| email 已是成員 | 422 `email_taken` |
| 公司已有 50 位成員 | 422 `too_many_members` |
| 移除自己 | 422 `cannot_remove_self` |
| 公司 24 小時內已建立 20 場 | 422 `too_many_events` |
| 公司名稱不合格 | 422 `invalid_organization` |
| 系統後台 24 小時內已建立 5 家公司 | 422 `too_many_organizations` |
