# API 規格 (API)

> 狀態：M1～M2c 的端點都已實作並經 PO 核准與安全審查；審查紀錄見 git 歷史。
> 範圍：`requirements/stories/m1.md`、`m2a.md`、`m2b.md`、`m2c.md`；之後的里程碑在本文件追加端點。
> 依據：`access-control.md`（身分、驗證結果、權限表）、`sync-and-offline.md`（輪詢與快照）、`data-model.md`（狀態轉換）、`tech-stack.md` 2.1（逾時與錯誤、Token 與復原碼、lastSeenAt、日誌、安全標頭）。

---

## 1. 共通慣例

| 項目 | 規則 |
| --- | --- |
| 格式 | 請求與回應皆為 JSON（`Content-Type: application/json`），UTF-8 |
| Token | 只放在 `Authorization: Bearer <token>` 標頭（Caddy 預設在存取日誌中遮蔽 `Authorization`，不要啟用 `log_credentials`）。不接受查詢字串、Cookie 或本文中的 Token |
| Token 格式 | 16 位元組隨機值的 base64url（無補位），恰好 22 字元；伺服器以 SHA-256（對這 22 個字元的 ASCII 位元組）查詢（指令產生連結與伺服器查詢用同一規則，不需先解碼）。格式不符視為「格式錯誤」（400），**不是**「無效」 |
| 快取 | 所有 `/api/` 回應（含錯誤）一律 `Cache-Control: no-store`，由中介層統一設定 |
| 本文 | 上限 16 KB（M2c：`/api/rundown/import` 與 `import/preview` 上限 256 KB，只有這兩個端點；其中 `text` 的 100 KB 以解碼後的 UTF-8 位元組計算，JSON 跳脫後仍在 256 KB 內）；只接受單一 JSON 物件；**未知欄位一律拒絕**（400）；`version` 缺少、為 `null`、不是整數或小於 1 都回 400 |
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
| 404 | `not_found` | 本文中的 Cue id 或角色 id 不在呼叫者的活動裡（不存在或屬於別的活動，兩者不區分；第 6 節） |
| 422 | `not_draft`、`not_live`、`not_event_day`、`nothing_to_advance`、`nothing_to_undo`、`undo_stale`、`shift_too_early`、`invalid_delta`；M2b 追加 `not_pending`、`invalid_position`、`invalid_cue`、`invalid_assignment`、`not_checkin_role`、`master_role`；M2c 追加 `invalid_role`、`duplicate_name`、`too_many_roles`、`invalid_import`、`locked_on_event_day` | 領域規則不允許（第 6 節） |
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
| `POST /api/roles/reissue` | Token | Master | `{"roleId":n}`；M2c 起可另帶 `"origin":"https://…"` | `{"result":"ok","snapshot":{…},"token":"<22 字元>"}`（M2b，US-M-7.1）；帶 `origin` 時另附 `"qr"`（M2c，US-M-13.1） |
| `POST /api/roles/revoke` | Token | Master | `{"roleId":n}` | `ok` + 新的 Master 快照（M2b，US-M-7.2） |
| `POST /api/cues/update` | Token | Master | `{"version":n,"cueId":n,"start":"<RFC 3339>","durationMin":n,"isFixed":bool,"title":"…","notePublic":"…"或null}` | `ok` + 新的 Master 快照；只限 Draft（M2c，US-M-10.2） |
| `POST /api/cues/move` | Token | Master | `{"version":n,"cueId":n,"afterCueId":n或null}` | `ok` + 新的 Master 快照；`null` 表示移到第一個；只限 Draft（M2c，US-M-10.3） |
| `POST /api/cues/delete` | Token | Master | `{"version":n,"cueId":n}` | `ok` + 新的 Master 快照；只限 Draft（M2c，US-M-10.4） |
| `POST /api/rundown/import/preview` | Token | Master | `{"text":"…"}` | `{"result":"ok","preview":{"fileError":"…"或null,"rows":[…],"missingRoles":["…"],"tooManyRoles":bool,"replaces":n}}`；不寫入；只限 Draft（M2c，US-M-12.2） |
| `POST /api/rundown/import` | Token | Master | `{"version":n,"text":"…","newRoles":[{"name":"…","view":"HandsFree"或"Glance","checksIn":bool}…]}` | `ok` + 新的 Master 快照；取代整份；只限 Draft（M2c，US-M-12.3、12.4、12.6） |
| `POST /api/roles/add` | Token | Master | `{"name":"…","view":"HandsFree"或"Glance"或"Dashboard","checksIn":bool,"origin":"https://…"}` | `{"result":"ok","snapshot":{…},"roleId":n,"token":"<22 字元>","qr":"data:image/png;base64,…"}`（M2c，US-M-11.1、11.2、US-M-13） |
| `POST /api/roles/rename` | Token | Master | `{"roleId":n,"name":"…"}` | `ok` + 新的 Master 快照（M2c，US-M-11.4） |
| `POST /api/roles/delete` | Token | Master | `{"version":n,"roleId":n}` | `ok` + 新的 Master 快照；只限 Draft（M2c，US-M-11.5） |
| `POST /api/roles/preview` | Token | Master | `{"roleId":n}` | `{"result":"ok","preview":{…}}`：`preview` 是目標角色自己會拿到的快照（M2c，US-M-14） |
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
- **M2c 寫入的共通規則**（Rundown 編輯器、角色管理、匯入）：
  - **只限 Draft 的結構性修改**：`cues/update`、`cues/move`、`cues/delete`、`rundown/import`、`rundown/import/preview`、`roles/delete` 在 Live 回 422 `not_draft`，由 store 在交易內檢查，不只靠畫面（US-M-10.7）。Live 時的編輯仍只有 M2b 的有限編輯（`access-control.md` 第 4 節）。
  - **活動日當天鎖住兩項**（ADR-0032）：從活動日 00:00（活動時區）起，`rundown/import` 與 `roles/delete` 回 422 `locked_on_event_day`（`rundown/import/preview` 照常回應，讓畫面說明原因）；在交易內以伺服器時鐘判斷。當天仍可以逐一 `cues/update`、`move`、`delete`，以及新增、改名角色。判斷順序：歸屬（404）→ `not_draft` → `locked_on_event_day` → 其他檢查。
  - **版本**：改到 Cue 或指派的寫入（`cues/update`、`move`、`delete`、`rundown/import`、`roles/delete`）帶 `version`，成功後 `eventVersion` + 1；`roles/add`、`roles/rename` 不帶 `version`、不改 `eventVersion`（與重發相同，不影響任何人的 Cue；ADR-0023）。
  - **操作紀錄**：型別為 `rundown.update`、`rundown.move`、`rundown.delete`、`rundown.import`、`role.add`、`role.rename`、`role.delete`，都不以 `cue.` 開頭，不是「復原上一步」的步驟（ADR-0021）。被取代或刪除的內容**完整記錄**（ADR-0032）；`rundown.update` 另記改之前的欄位（`before`），活動日當天外流的 Master 連結逐一覆寫也查得出原本的內容：`rundown.delete` 記被刪 Cue 的欄位、指派與角色備註；`rundown.import` 記被取代的每個 Cue 的欄位、指派與角色備註，以及新建的角色；`role.delete` 記角色的名稱、類型、畫面、是否打卡與它的指派、角色備註；`role.add`、`role.rename` 記角色 id 與名稱。操作紀錄不進任何快照，隨活動一起刪除。**所有 M2c 寫入的紀錄都另記操作者的 `byTokenId`**（`access_tokens.id`，不是雜湊；同 M2b 的重發與撤銷），以復原碼換發 Master 後，才查得出外流的舊連結建立、刪除或匯入了什麼（`access-control.md` 第 1 節、3.4；安全審查 P1）。都不含 Token、雜湊或 `origin`。
  - **歸屬先於一切檢查**與**目標的 Token 不影響驗證結果**（M2b 的兩條）同樣適用於 M2c 帶 id 的端點：`cueId`、`afterCueId`、`roleId` 只在呼叫者的活動中尋找，查不到回 404，排在 `master_role`、`not_draft`、`invalid_*`、`duplicate_name`、`too_many_roles` 等檢查之前；重名與上限只以**呼叫者的活動**為範圍查詢，不得以目標 id 反查活動（否則 `duplicate_name` 會成為探測別的活動角色名稱的工具）；刪除、改名、預覽一個沒有有效 Token 的角色照常成功。
- **編輯 Cue**（`cues/update`）：整筆取代該 Cue 的開始時間、長度、固定、標題、公開備註；限制與新增相同（`start` 落在活動日 00:00 到 `endsAt` 之間、`durationMin` 0～1440、標題 1～100 字、備註最多 500 字，否則 `invalid_cue`）。`start` 直接寫入 `scheduledStart`：平移只在 Live（`opLiveOnly`），所以 Draft 的 `offsetMin` 一定是 0；store 仍在交易內確認 `offsetMin = 0`，不是 0 時回 500（程式錯誤，不該發生）。Pending 與 Skipped 的 Cue 都可以改（Draft 只有這兩種狀態）。指派與角色備註沿用 `POST /api/cues/roles`。
- **調整順序**（`cues/move`）：把 Cue 移到 `afterCueId` 之後，`null` 為移到第一個；`afterCueId` 等於 `cueId` 回 `invalid_position`。在單一交易內重排 `seq`，維持唯一、連續（`UNIQUE (event_id, seq)` 需兩段式更新，同新增）。只改順序，不改時間；順序與時間不一致時由 `outOfOrder[]` 提示（US-M-10.6）。移動與刪除後整份重新編號為 1..n，所以 M1 匯入時留下的間隔（例如 10、20、30）也會被整理。
- **刪除 Cue**（`cues/delete`）：刪除該 Cue 與其指派（外鍵 `ON DELETE CASCADE`），後面的 `seq` 依序遞補。不留墓碑；Live 時的「刪除」仍是略過。
- **新增 Cue 在 Draft 的放寬**：M2c 起，Draft 階段的 `POST /api/cues/insert` 可帶 `"afterCueId":null`，插在第一個；流程表是空的時只能帶 `null`。Live 帶 `null` 回 `invalid_position`，在 store 的交易內判斷活動階段（M2b 規則不變）；本文需要新的欄位種類「正整數或 null」。
- **復原上一步與 M2c 的修改**：M2c 的修改不是步驟，但可能讓上一個 Cue 操作無法復原（例如被略過的 Cue 已刪除、或整份已被匯入取代）；此時 `POST /api/cues/undo` 回 `undo_stale`（`domain.Undo` 找不到 Cue 就回 `ErrUndoStale`，既有行為）。Master 快照的 `undo` 改為：上一個 Cue 操作已無法復原時，與「沒有可復原的操作」相同，回 `{"available":false,"description":null}`（鍵集合不變），不顯示一個按了必定失敗的按鈕；判斷方式是對目前的 Cue 試跑 `domain.Undo`，回 `ErrUndoStale` 即為無法復原。
- **貼上匯入**：
  - `text` 為整份流程表文字，欄位與 `bethere import-rundown` 的 CSV 相同（`stories/m1.md` US-M-0.2，含標題列）；第一列含 Tab 時以 Tab 分隔（從試算表複製），否則以逗號分隔。最多 200 列資料、100 KB；超過回 `invalid_import`。驗證與 `import-rundown` 共用同一個解析與驗證函式；這個函式補上與新增 Cue 相同的限制（標題 1～100 字、`notePublic` 與 `roleNote` 最多 500 字），讓匯入的 Cue 在編輯器原樣儲存不會失敗。寫入時依 CSV 的 `seq` 排序，重新編號為 1..n。
  - 預覽回 `rows[]`：`{"line","seq","title","start","durationMin","isFixed","roles":["…"],"roleNote","notePublic","errors":["…"]}`，`errors` 為人看得懂的中文原因；`missingRoles[]` 為文字中提到、但活動裡沒有的角色名稱（依出現順序、不重複）；`replaces` 為目前的 Cue 數量（畫面據此提示「現有的 N 個環節與指派將被取代」）。有任何一列錯誤時畫面不能確認。整份文字讀不懂時（缺少必要欄位、不認得的欄位、超過 100 KB 或 200 列，或只有標題列、沒有任何環節列），`fileError` 為原因、`rows` 為空陣列；匯入同樣的文字回 `invalid_import`，但排在版本、`not_draft`、`locked_on_event_day` 之後（實作後驗證 P1：只有標題列不得把流程表換成空的）。
  - **角色比對**（安全審查 P1）：以該活動的**所有**角色（含 Master）比對名稱。對到 Master、Host 或不開放的類型就是錯誤列（與指派規則相同）；**只有完全對不到的名稱**才列入 `missingRoles`。缺少的名稱也要通過新增角色的名稱規則（不合格時該列標為錯誤）；建立後會超過 50 個角色時，預覽另回 `"tooManyRoles":true`，畫面不能確認。
  - 匯入在單一交易內：確認 `newRoles` 的名稱集合**恰好等於**此刻的 `missingRoles`（不多不少，否則 `invalid_import`，因為兩次請求之間別人可能已新增角色）→ `newRoles` 的名稱不可重複（400）、每一筆經過與 `roles/add` 相同的名稱檢查函式與上限檢查（`invalid_role`、`too_many_roles`）→ 建立 `newRoles`（不產生 Token，US-M-12.6）→ 刪除現有全部 Cue 與指派 → 寫入新的 Cue 與指派 → 一筆 `rundown.import` 紀錄 → `eventVersion` + 1。任一步失敗整份不寫入。`newRoles` 的 `view` 只能是 `HandsFree` 或 `Glance`（匯入的角色會被指派 Cue，平安儀表板不看 Cue）。
  - `bethere import-rundown` 維持「活動中尚無 Cue 才能匯入」（US-M-0.2a），不提供取代。
- **新增角色**：
  - `name` 去除前後空白後 1～50 字，不得含 `;`（CSV 以它分隔角色）、控制字元或零寬字元（否則 `invalid_role`），與同一活動的其他角色（含 Master）不可同名（`duplicate_name`）；一場活動最多 50 個角色（含 Master，`too_many_roles`）。改名的名稱規則相同。
  - **類型由伺服器依 `view` 決定**，總召不指定權限（ADR-0031）：`HandsFree` → Stylist、`Glance` → Media、`Dashboard` → Host。`Dashboard` 帶 `checksIn: true` 回 `invalid_role`。StageAV 只能以指令建立（快速瞄畫面以等寬字顯示備註的差異，見 `screen-design.md` 第 4 節）。
  - **`checksIn`** 存在新欄位 `roles.checks_in`（可為空，ADR-0019；遷移 0003）：`NULL` 表示依類型（Stylist、Media、StageAV 打卡，Host 不打卡；既有角色與 `add-role` 建立的角色都是 `NULL`），`0` 表示不打卡；只寫入 `NULL` 或 `0`（`checksIn: true` 存成 `NULL`），遷移 0003 為 `ALTER TABLE roles ADD COLUMN checks_in INTEGER CHECK (checks_in IS NULL OR checks_in = 0)`，不帶 `bethere:foreign_keys=off`，只新增（ADR-0019）；解析時 Master、Host 一律不打卡，不理會這個欄位。打卡在同一交易內讀取這個欄位。退回 M2b 之前的版本時，舊程式不認得這個欄位，`checksIn: false` 的角色又可以打卡（接受）。`checksIn: false` 的角色：本人打卡與代打卡都回 `not_checkin_role`，Master 快照的 `checkedIn` 為 `false`；仍然可以被指派 Cue。
  - 新增在單一交易內建立角色與 Token，回應附 `token` 與 `qr`，明文只出現這一次（同重發）。Token 在寫入前產生，QR Code 也在**寫入前**產生，產生失敗就不寫入、回 500；QR 函式庫的錯誤不得包含編碼內容（記錄的 err 裡不能有 Token）。新增已寫入、但重新讀取快照失敗而回 500 時，Token 明文遺失，總召在角色清單按「產生連結」即可（同重發）。Master 角色不能經此建立（`view` 不接受 `Console`）。
- **QR Code**：`origin` 是客戶端的 `location.origin`，最多 200 字元，以 `url.Parse` 解析後必須：scheme 為小寫的 `https`（`http` 只限主機為 `localhost` 或 `127.0.0.1`）；`User`、`Path`、`RawQuery`、`Fragment`、`Opaque` 全部為空；主機只含 `[a-z0-9.-]`；連接埠若有則為 1～65535；重新序列化後與輸入完全相同。`origin` 欄位一旦出現就必須通過檢查，空字串同樣 400，只有重發可以完全不帶（實作後驗證 P1）。不符一律 400（安全審查 P2：擋下 `https://正式網域@evil.example` 這類讓 QR 指向別處、把 Token 交給攻擊者的值）；QR Code 的內容為 `origin + "/#t=" + token`，與畫面上的文字連結完全相同。以鎖定版本的函式庫在記憶體產生 PNG，以 data URI 回傳，不寫入磁碟、日誌或資料庫；客戶端只顯示，不存入本機快照或任何儲存（US-M-13.2）。`origin` 只用於這一次回應。
- **刪除角色**：只限 Draft；Master 回 `master_role`。同一交易內刪除角色列，其 Token、指派、打卡隨外鍵一併刪除，持有該連結的裝置下一次輪詢收到 `invalid`（查無 Token）。
- **改名**：Draft 與 Live 都可以；Master 可以改名。
- **角色預覽**：
  - 回應的鍵是 `preview`，**不是** `snapshot`：客戶端只把 `snapshot` 存入本機快照，`preview` 只在記憶體顯示，絕不寫入本機儲存、不影響 ETag 與輪詢（否則總召的裝置會把別人的快照當成自己的）。
  - `preview` 由第 7 節第 2 條的同一個快照產生函式，以**目標角色**的身分組出，內容與目標角色自己 `GET /api/snapshot` 拿到的相同（`serverTime` 除外），以測試比對；目標是 Host 時為彙總快照。目標為 Master 回 `master_role`。
  - 預覽只讀：不寫入任何資料與操作紀錄，不更新**目標**的 `lastSeenAt`（呼叫者自己的照常更新），不需要也不讀取目標的 Token。
  - 實作上，store 的預覽函式分開回傳「呼叫者的 `TokenOwner`」與「以目標組出的快照資料」；目標那一份的 `TokenID` 為 0、`Actor` 為零值（不帶任何憑證），API 層只對呼叫者呼叫 `seen`。目標是不開放的類型時回 404（與不存在相同），不回 500。前端預覽時不處理 `notice`，不寫入任何「已看過的提示」標記。
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
| M2c：`POST /api/cues/update`、`move`、`delete`，`/api/rundown/import`、`import/preview`，`/api/roles/add`、`rename`、`delete`、`preview` | ✔ | 403 | 403 | 403 | 403 | 403 |

- **M2a 開放 Master、Stylist 與 Host**：`bethere add-role` 接受 Stylist 與 Host，Media、StageAV 於 M2b 開放（未定義的類型不能落入寬鬆的預設權限）；開放前，資料庫中的 Media、StageAV 同樣適用下一句。暫停的角色類型（VenueLead、Reception、Cashier、Runner，ADR-0033）不能建立。資料庫中出現任何未開放的類型時，每個端點都回 403，不落入任何預設分支。
- 權限檢查在 API 層。store 只確認操作者的憑證仍有效且屬於同一活動，**不檢查角色類型**（`access-control.md` 第 5 節第 6 條），所以每個寫入端點都必須先通過本表。

## 5. 快照

### 5.1 共通欄位

| 欄位 | 說明 |
| --- | --- |
| `eventVersion` | 單調遞增整數 |
| `serverTime` | 產生快照時的伺服器時間 |
| `eventStatus` | `Draft` 或 `Live`（結束時回 `ended`，不會有快照） |
| `role` | `{"type":"Stylist","name":"新秘","view":"HandsFree","checksIn":true}`：自己的角色；`view` 為解析過預設值後的畫面（ADR-0025，M2a 追加）；`checksIn` 為是否要就位打卡，解析過 `roles.checks_in` 的預設值（M2c 追加；Master、Host 一律 `false`） |
| `eventDay` | M2a 追加：活動日期與時區 `{"date","timezone"}`，供 Draft 階段判斷「是否為活動日」與顯示日期（`screen-design.md` 4.5，ADR-0020）；Host 同樣帶 |

### 5.2 Master 快照（另含）

| 欄位 | 說明 |
| --- | --- |
| `event` | `{"name","date","timezone"}` |
| `cues[]` | 全部 Cue：`id, seq, title, scheduledStart, effectiveStart, durationMin, isFixed, offsetMin, status, actualStart, actualEnd, notePublic, roles[]`；`roles[]` 為 `{"roleId","name","roleNote"}` |
| `activeCueId` | 進行中的 Cue，沒有則為 `null` |
| `undo` | `{"available":bool,"description":"撤銷：推進『一進預備』"}`；描述由伺服器產生，沒有可復原的操作時 `available` 為 false、`description` 為 `null`；M2c 起，上一個 Cue 操作已無法復原時也一樣（第 3 節「復原上一步與 M2c 的修改」）。推進的描述以「開始的那個 Cue」命名；推進的是最後一個 Cue（沒有開始新的）時，以「完成的那個 Cue」命名（PO 於 2026-10-22 確認） |
| `roles[]` | `{"id","name","type","view","checksIn","checkedIn","lastSeenAt","hasLink"}`（M2b 追加 `checkedIn`、`lastSeenAt`、`hasLink`；M2c 追加 `view`、`checksIn`）：`checkedIn` 為是否就位（Master 與 Host 一律 `false`）；`lastSeenAt` 為該角色連結最近一次驗證成功的時間，**截到分鐘**，沒有時為 null；`hasLink` 為是否有有效 Token。不含 Token 或其雜湊 |
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
| M2c：刪除或預覽的目標是 Master | 422 `master_role` |
| M2c：Live 時的結構性修改（`cues/update`、`move`、`delete`、匯入、刪除角色） | 422 `not_draft` |
| M2c：`cues/move` 移到自己之後 | 422 `invalid_position` |
| M2c：角色名稱空白或超過 50 字、`Dashboard` 要求打卡、`view` 不在允許的值 | 422 `invalid_role` |
| M2c：角色名稱與同活動的角色重複 | 422 `duplicate_name` |
| M2c：角色已達 50 個 | 422 `too_many_roles` |
| M2c：匯入文字超過上限、任一列不合法、`newRoles` 與缺少的角色不完全相同 | 422 `invalid_import` |
| M2c：打卡的角色 `checksIn` 為 false | 422 `not_checkin_role` |
| M2c：活動日當天的匯入取代或刪除角色 | 422 `locked_on_event_day`（ADR-0032） |
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
- 第 2、4、6 節屬於「Token 權限與資料外洩」邊界：實作完成後由獨立審查依第 4 節權限表的**每一格**、第 6 節的**每一列**與第 5 節的鍵集合驗證。
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
  - M2c：
    - 第 4 節新增的每一格（含 Host、未開放類型呼叫每個新端點都回 403）。
    - Live 時 `cues/update`、`move`、`delete`、匯入、預覽匯入、刪除角色都回 `not_draft`，資料不變。
    - 帶別的活動的 Cue id、角色 id（含別的活動的 Master、別的**已結束**活動的角色）回 404 且不帶 `result`，不回 `master_role`、`not_draft`、`invalid_position`。
    - 刪除、改名、預覽 Token 已撤銷的角色回 200，總召不會收到 `invalid`；刪除角色後，該角色原本的 Token 回 `invalid`。
    - 預覽：回應沒有 `snapshot` 鍵；`preview` 與目標角色自己的快照（`serverTime` 除外）相同，Stylist、Media、StageAV、Host 各一；預覽後目標的 `lastSeenAt` 不變、操作紀錄筆數不變；前端：`preview` 不進本機儲存。
    - 新增角色與帶 `origin` 的重發：`token`、`qr` 只出現在這一次回應；QR Code 解碼後等於 `origin + "/#t=" + token`；`origin` 帶路徑、查詢、片段、`javascript:` 或超過 200 字元回 400；操作紀錄不含 Token、雜湊或 `origin`；前端：`token` 與 `qr` 不進本機儲存。
    - 匯入：任一列錯誤、`newRoles` 多一個或少一個時整份不寫入，現有 Cue 與角色不變；成功時舊 Cue 全部消失；匯入後原本可復原的略過變成 `undo` 為 `null`，`POST /api/cues/undo` 回 `undo_stale`。
    - `checksIn: false` 的角色本人打卡、被代打卡都回 `not_checkin_role`。
    - `cues/move` 後 `seq` 唯一且從 1 連續；`cues/delete` 後同樣。
    - Draft 的 `offsetMin` 不為 0 時，`cues/update` 回 500 而不是寫入（以直接改資料庫模擬）。
    - 所有 M2c 寫入的操作紀錄都有操作者的 `byTokenId`。
    - 活動日當天（以注入的時鐘）`rundown/import` 與 `roles/delete` 回 `locked_on_event_day` 且資料不變；前一天 23:59 可以。
    - `rundown.import`、`rundown.delete`、`role.delete` 的紀錄足以重建被取代或刪除的 Cue、指派與角色（以紀錄重建後比對原資料的測試固定），且不含 Token 或雜湊。
    - 匯入：CSV 寫到 Master 的名稱是錯誤列，不進 `missingRoles`；`newRoles` 有重複名稱回 400；缺少的名稱含 `;` 或超過 50 字時該列為錯誤；從有間隔的 `seq` 匯入後為 1..n。
    - 改名別的活動的角色 id、並帶上與該活動某角色相同的名稱，回 404 而不是 `duplicate_name`。
    - 預覽從未發過連結的角色（匯入時建立的）回 200；預覽不開放類型的角色回 404。
    - `origin` 為 `https://a@b.example`、含反斜線、大寫 scheme、`http://` 加公開主機時回 400。
