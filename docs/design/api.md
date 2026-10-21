# API 規格 (API) — M1 範圍

> 狀態：草稿，待安全審查與 PO 核准。
> 範圍：`requirements/m1-acceptance.md` 的 US-M-1、US-M-3、US-M-4、US-ST-1、US-X-1；之後的里程碑在本文件追加端點。
> 依據：`access-control.md`（身分、驗證結果、權限表）、`sync-and-offline.md`（輪詢與快照）、`data-model.md`（狀態轉換）、`tech-stack.md`（T3b、T6、T7、T13、T14）。本文件的決定以 A 編號列在第 9 節。

---

## 1. 共通慣例

| 項目 | 規則 |
| --- | --- |
| 格式 | 請求與回應皆為 JSON（`Content-Type: application/json`），UTF-8 |
| Token | 只放在 `Authorization: Bearer <token>` 標頭（A1）。不接受查詢字串、Cookie 或本文中的 Token |
| Token 格式 | 16 位元組隨機值的 base64url（無補位），恰好 22 字元；伺服器以 SHA-256（對這 22 個字元的 ASCII 位元組）查詢（A2）。格式不符視為「格式錯誤」（400），**不是**「無效」 |
| 快取 | 所有 `/api/` 回應（含錯誤）一律 `Cache-Control: no-store`，由中介層統一設定 |
| 本文 | 上限 16 KB；只接受單一 JSON 物件；**未知欄位一律拒絕**（400） |
| 活動與角色 | Token 驗證的請求**不帶** `eventId` 或自己的 `roleId`；活動與角色一律取自 Token（A3） |
| 時間 | 一律 UTC，RFC 3339（含小數秒），例如 `2027-06-06T03:15:00.123Z` |
| 跨來源 | 不回任何 `Access-Control-*` 標頭、不使用 Cookie；Token 只在標頭，所以 CSRF 不適用 |
| 頻率限制 | 不對 Token 限頻（`access-control.md` P7） |
| 日誌 | 不記錄請求標頭、本文、Token、Token 雜湊；錯誤回應只回固定的錯誤代碼，不回內部錯誤訊息（T13） |
| 逾時 | 快照讀取的資料庫查詢帶 3 秒 context 逾時（T3b）；`http.Server` 設定讀取、寫入、閒置逾時 |

## 2. 回應的兩種形狀

### 2.1 驗證結果（帶 `result`）

**只有**以下三種回應帶 `result` 欄位，HTTP 狀態一律 **200**：

| `result` | 條件（依序判斷） | 內容 | 客戶端 |
| --- | --- | --- | --- |
| `invalid` | 儲存層查詢成功，且確實查無有效 Token（`store.ErrInvalidToken`） | 只有 `{"result":"invalid"}`；錯誤與已撤銷不區分 | 顯示「連結已失效」，停止輪詢，清除該 Token 的本機資料 |
| `ended` | Token 有效，但活動已過 `endsAt`（查詢時的 `store.ErrEventEnded`，或寫入時對**自己活動**的 `domain.ErrEnded`） | 只有 `{"result":"ended"}`，**不含任何活動資料** | 顯示「活動已結束」，清除該 Token 的本機資料 |
| `ok` | 其餘 | `{"result":"ok","snapshot":{…}}` | 正常顯示 |

客戶端**只有**在回應可解析、且 `result` 明確為 `invalid` 或 `ended` 時才清除本機資料（`access-control.md` 3.3）。

### 2.2 錯誤（不帶 `result`）

其他一切失敗都**不帶** `result`，以 `{"error":"<代碼>"}` 回應；客戶端一律視為「操作失敗」或「離線」，**絕不清除本機資料**。

| HTTP | `error` | 何時 |
| --- | --- | --- |
| 400 | `bad_request` | 缺少或重複的 `Authorization` 標頭、Token 格式不符、本文不是單一 JSON 物件、有未知欄位、超過 16 KB |
| 403 | `forbidden` | Token 有效，但角色類型沒有這個端點的權限（第 4 節） |
| 409 | `version_conflict` | 寫入帶的 `version` 不是目前版本；回應另附 `snapshot`（最新快照），客戶端更新畫面並提示「畫面已更新，請重新確認」 |
| 422 | `not_draft`、`not_live`、`not_event_day`、`nothing_to_advance`、`nothing_to_undo`、`undo_stale` | 領域規則不允許（第 6 節） |
| 500 | `internal` | 資料庫錯誤、逾時、`SQLITE_BUSY`、未預期的狀態（例如兩個進行中的 Cue）、任何未列出的錯誤 |

## 3. 端點（M1）

| 方法與路徑 | 憑證 | 權限（第 4 節） | 本文 | 成功回應 |
| --- | --- | --- | --- | --- |
| `GET /api/snapshot` | Token | 讀取快照 | — | `ok` + 依角色的快照 |
| `POST /api/event/start` | Token | Master | `{"version":n}` | `ok` + 新的 Master 快照 |
| `POST /api/cues/advance` | Token | Master | `{"version":n}` | `ok` + 新的 Master 快照 |
| `POST /api/cues/undo` | Token | Master | `{"version":n}` | `ok` + 新的 Master 快照 |
| `GET /api/ping` | 無 | — | — | `{"serverTime":…,"version":"<發行版>"}`；部署檢查用（A6） |

- Master 的寫入都要帶 `version`（S6）；寫入成功的回應直接附上新快照（S1），總召不必等下一次輪詢。
- **`POST /api/report` 移除**（A7）。

## 4. 權限

角色類型**只取自** Token 所屬角色（`TokenOwner.RoleType`），不信任請求內容。**未列出的組合一律拒絕**（403）。

| 端點 | Master | Stylist | Media | StageAV | Host | 其他類型 |
| --- | --- | --- | --- | --- | --- | --- |
| `GET /api/snapshot` | Master 快照 | 角色快照 | 角色快照 | 角色快照 | 彙總快照 | 403 |
| `POST /api/event/start` | ✔ | 403 | 403 | 403 | 403 | 403 |
| `POST /api/cues/advance` | ✔ | 403 | 403 | 403 | 403 | 403 |
| `POST /api/cues/undo` | ✔ | 403 | 403 | 403 | 403 | 403 |

- P1 角色類型（VenueLead、Reception、Cashier、Runner）**在 P0 不能建立**：`bethere add-role` 只接受 Stylist、Media、StageAV、Host（A4）；即使資料庫中出現，快照也回 403，不落入任何預設分支。
- 權限檢查在 API 層。store 只確認操作者的憑證仍有效且屬於同一活動，**不檢查角色類型**（`access-control.md` 第 5 節第 6 條），所以每個寫入端點都必須先通過本表。

## 5. 快照

### 5.1 共通欄位

| 欄位 | 說明 |
| --- | --- |
| `eventVersion` | 單調遞增整數 |
| `serverTime` | 產生快照時的伺服器時間 |
| `eventStatus` | `Draft` 或 `Live`（結束時回 `ended`，不會有快照） |
| `role` | `{"type":"Stylist","name":"新秘"}`：自己的角色 |

### 5.2 Master 快照（另含）

| 欄位 | 說明 |
| --- | --- |
| `event` | `{"name","date","timezone"}` |
| `cues[]` | 全部 Cue：`id, seq, title, scheduledStart, effectiveStart, durationMin, isFixed, offsetMin, status, actualStart, actualEnd, notePublic, roles[]`；`roles[]` 為 `{"roleId","name","roleNote"}` |
| `activeCueId` | 進行中的 Cue，沒有則為 `null` |
| `undo` | `{"available":bool,"description":"撤銷：推進『一進預備』"}`；描述由伺服器產生 |
| `roles[]` | `{"id","name","type"}`（不含 Token 或使用紀錄；`lastSeenAt` 於 M2 追加） |

### 5.3 角色快照（Stylist、Media、StageAV）

| 欄位 | 說明 |
| --- | --- |
| `cues[]` | **只有指派給自己的 Cue**：`id, seq, title, effectiveStart, durationMin, isFixed, offsetMin, status, notePublic, roleNote`（只有自己的 `roleNote`） |
| `current` | 目前進行中的 Cue：`{"title","assignedToMe"}`，沒有則為 `null`（A5） |

### 5.4 彙總快照（Host）

| 欄位 | 說明 |
| --- | --- |
| `offsetMin` | 目前進行中（或下一個待辦）Cue 的 `offsetMin`（`data-model.md` 4.3） |

就位率於 M2 追加。

### 5.5 一律不出現

Token 或其雜湊、復原碼雜湊、其他角色的 `roleNote`、未指派給自己的 Cue（Master 除外）、操作紀錄與內部 id（`action_logs`、`check_ins`）。各角色快照的**鍵集合**以測試固定（T17）。

### 5.6 Draft 階段

非 Master 角色在 Draft 階段也回完整的角色快照（A8），讓裝置在活動前就存好離線快照（`tech-stack.md` 3.2）；畫面依 `eventStatus` 顯示「活動尚未開始」。

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
| `store.ErrNoActor`、`store.ErrCrossEvent`、`store.ErrNotFound` | 500 `internal`（M1 的請求不帶任何 id，出現即為程式錯誤） |
| `domain.ErrMultipleInProgress`、`store.ErrUnknownOp`、資料庫錯誤、逾時 | 500 `internal` |
| 其他 | 500 `internal` |

**之後的里程碑**：一旦請求帶有 Cue 或角色的 id（M2 的略過、代打卡等），store 必須**先檢查操作者與目標是否同一活動（`ErrCrossEvent`），再檢查活動是否結束**；目前 `guardAs` 的順序相反（`internal/store/events.go:151-159`），會讓別的已結束活動的 id 回 `ended` 而清掉呼叫者的資料。修正與測試列為 M2 的前置工作；屬於別的活動的 id 一律回與「不存在」相同的 404，不帶 `result`。

## 7. 伺服器端規則

1. **每個請求**：解析標頭 → 查 Token（`store.LookupToken`）→ 依第 4 節檢查權限 → 執行。
2. **快照在單一讀取交易中產生**：查 Token 與組快照在同一個唯讀交易內完成（A9），撤銷或活動結束不會在兩者之間漏出一份快照。
3. **`lastSeenAt`**：只在 Token 驗證成功之後於記憶體更新（T7）；M3 加入維護模式後，改為通過維護檢查之後才更新。
4. **所有寫入**都帶 store 的 `Actor`，在同一交易內重新確認憑證並寫入操作紀錄。
5. **指令（`bethere` 子命令）**：
   - `serve`：`BETHERE_INIT` 只接受 `1`（建立新資料庫）或未設定／空字串；其他值一律拒絕啟動（A10）。
   - `create-event`、`add-role`、`import-rundown` 一律以「不建立新資料庫」的方式開啟；連結與復原碼只印到終端機（stdout），不經過結構化日誌（A11）。
   - `import-rundown` 在單一交易內寫入全部 Cue 與指派，任一列不合法就整份不寫入（US-M-0.2）。

## 8. 安全邊界相關的限制

- **維護模式**（T3d）在 M3 實作；在那之前**正式環境不放入真實活動資料**（`process/development-process.md`）。
- 第 2、4、6 節屬於「Token 權限與資料外洩」邊界：實作完成後由 `verifier` 依第 4 節權限表的**每一格**、第 6 節的**每一列**與第 5 節的鍵集合驗證。

## 9. 決定

| # | 決定 | 理由 |
| --- | --- | --- |
| A1 | Token 用 `Authorization: Bearer` 標頭 | Caddy 預設在存取日誌中遮蔽 `Authorization`；不要啟用 `log_credentials`（依鎖定的 Caddy 版本確認） |
| A2 | 雜湊的輸入為 22 字元的 Token 字串本身 | 指令產生連結與伺服器查詢用同一規則，不需先解碼 |
| A3 | Token 驗證的請求不帶活動或自己的角色 id | 杜絕跨活動與冒用他人角色 |
| A4 | P0 不能建立 P1 角色類型 | P0 權限表沒有定義它們；避免落入寬鬆的預設權限 |
| A5 | 角色快照帶目前進行中 Cue 的標題 | V2 的「目前環節」與 V2a 的「準備中」需要；只有標題，不含備註與指派 |
| A6 | 保留 `GET /api/ping`（不需憑證） | 部署後確認版本（`architecture/runtime-views.md`）；不含活動資料 |
| A7 | 移除 `POST /api/report` | 實機驗證原型已完成；它不需憑證、會寫入日誌並記錄 Token 前綴。M3 的 Android 驗證改以截圖與手動紀錄 |
| A8 | Draft 階段也回完整的角色快照 | 讓活動前一天開啟時就存好離線快照 |
| A9 | 查 Token 與組快照在同一個讀取交易 | 撤銷與結束立即生效，沒有毫秒級的空窗 |
| A10 | `BETHERE_INIT` 只接受 `1` | 避免 `BETHERE_INIT=0` 之類的值被當成「要建立」而在還原失敗時建出空庫（ADR-0012） |
| A11 | 子命令的秘密只印到終端機 | `access-control.md` 第 2 節；`tech-stack.md` T11 |
