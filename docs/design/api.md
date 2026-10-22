# API 規格 (API) — M1 範圍

> 狀態：PO 核准（2026-10-21）；已經安全審查（2026-10-21），無 P0／P1，發現均已處理；審查紀錄見 git 歷史。
> 範圍：`requirements/stories/m1.md` 的 US-M-1、US-M-3、US-M-4、US-ST-1、US-X-1；之後的里程碑在本文件追加端點。
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
| 422 | `not_draft`、`not_live`、`not_event_day`、`nothing_to_advance`、`nothing_to_undo`、`undo_stale` | 領域規則不允許（第 6 節） |
| 500 | `internal` | 資料庫錯誤、逾時、`SQLITE_BUSY`、未預期的狀態（例如兩個進行中的 Cue）、任何未列出的錯誤 |

## 3. 端點（M1）

| 方法與路徑 | 憑證 | 權限（第 4 節） | 本文 | 成功回應 |
| --- | --- | --- | --- | --- |
| `GET /api/snapshot` | Token | 讀取快照 | — | `ok` + 依角色的快照 |
| `POST /api/event/start` | Token | Master | `{"version":n}` | `ok` + 新的 Master 快照 |
| `POST /api/cues/advance` | Token | Master | `{"version":n}` | `ok` + 新的 Master 快照 |
| `POST /api/cues/undo` | Token | Master | `{"version":n}` | `ok` + 新的 Master 快照 |
| `GET /api/ping` | 無 | — | — | `{"serverTime":…,"version":"<發行版>"}`；部署檢查用；不含活動資料 |

- Master 的寫入都要帶 `version`（`sync-and-offline.md` 第 3 節）；寫入成功的回應直接附上新快照（`sync-and-offline.md` 第 1 節），總召不必等下一次輪詢。
- **`POST /api/report` 移除**：實機驗證原型已完成；它不需憑證、會寫入日誌並記錄 Token 前綴。M3 的 Android 驗證改以截圖與手動紀錄。

## 4. 權限

角色類型**只取自** Token 所屬角色（`TokenOwner.RoleType`），不信任請求內容。**未列出的組合一律拒絕**（403）。

| 端點 | Master | Stylist | Media | StageAV | Host | 其他類型 |
| --- | --- | --- | --- | --- | --- | --- |
| `GET /api/snapshot` | Master 快照 | 角色快照 | M2 起：角色快照 | M2 起：角色快照 | M2 起：彙總快照 | 403 |
| `POST /api/event/start` | ✔ | 403 | 403 | 403 | 403 | 403 |
| `POST /api/cues/advance` | ✔ | 403 | 403 | 403 | 403 | 403 |
| `POST /api/cues/undo` | ✔ | 403 | 403 | 403 | 403 | 403 |

- **M1 只開放 Master 與 Stylist**：`bethere add-role` 在 M1 只接受 Stylist，Media、StageAV、Host 於 M2 開放（M1 的畫面只有這兩種，未定義的類型不能落入寬鬆的預設權限）。P1 角色類型（VenueLead、Reception、Cashier、Runner）在 P0 不能建立。資料庫中出現任何未開放的類型時，每個端點都回 403，不落入任何預設分支。
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
| `undo` | `{"available":bool,"description":"撤銷：推進『一進預備』"}`；描述由伺服器產生，沒有可復原的操作時為 `null`。推進的描述以「開始的那個 Cue」命名；推進的是最後一個 Cue（沒有開始新的）時，以「完成的那個 Cue」命名（PO 於 2026-10-22 確認） |
| `roles[]` | `{"id","name","type"}`（不含 Token 或使用紀錄；`lastSeenAt` 於 M2 追加） |

### 5.3 角色快照（Stylist、Media、StageAV）

| 欄位 | 說明 |
| --- | --- |
| `cues[]` | **只有指派給自己的 Cue**：`id, seq, title, effectiveStart, durationMin, isFixed, offsetMin, status, notePublic, roleNote`（只有自己的 `roleNote`） |
| （無 `current`） | 目前進行中的 Cue 只有在指派給自己時才會出現在 `cues[]`（狀態 `InProgress`）；未指派的進行中 Cue 不揭露任何內容。新秘模式在未指派時本來就只顯示「準備中」；M2b 的攝影、音控視圖若需要「目前環節」，屆時由 PO 決定 |

### 5.4 彙總快照（Host）

| 欄位 | 說明 |
| --- | --- |
| `offsetMin` | 目前進行中（或下一個待辦）Cue 的 `offsetMin`（`data-model.md` 4.3） |

就位率於 M2 追加。

### 5.5 一律不出現

Token 或其雜湊、復原碼雜湊、其他角色的 `roleNote`、未指派給自己的 Cue（Master 除外）、操作紀錄與內部 id（`action_logs`、`check_ins`）。各角色快照的**鍵集合**以測試固定（`tech-stack.md` 2.2「型別」）。

### 5.6 Draft 階段

非 Master 角色在 Draft 階段也回完整的角色快照，讓裝置在活動前就存好離線快照（`operations/runbook.md` 第 3 節）；畫面依 `eventStatus` 顯示「活動尚未開始」。

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
2. **快照只有一個產生函式**：`GET`、寫入成功、409 三處都呼叫同一個函式，它在**單一唯讀交易**內查 Token 並組出**呼叫者自己角色**的快照，結果依第 6 節對照。在同一交易內查 Token 與組快照，撤銷與結束才會立即生效、沒有毫秒級的空窗；只有一個產生函式，避免 409 或寫入回應意外帶出 Master 快照。store 需新增這個函式（現有的 `LookupToken` 自己開關交易，分兩次呼叫會留下空窗）；以「兩步之間撤銷或結束」的測試驗證。
3. **處理順序**：解析標頭 → 查 Token → 權限（403）→ 解析本文（400）→ 寫入 → 重新讀取快照。409 只會在通過權限檢查後出現。
4. **寫入成功後重新讀取的結果**：讀到 `ok` 回 200 與新快照；讀到 `invalid` 或 `ended`（例如寫入與讀取之間被撤銷）就回那個結果；資料庫錯誤回 500（寫入已生效，客戶端下一次輪詢會看到）。一個回應**絕不同時**帶 `error` 與 `result`；409 附的快照同樣依此處理，讀不到 `ok` 就改回對應的 `result` 或 500。
5. **`lastSeenAt`**：只在 Token 驗證成功之後於記憶體更新（`tech-stack.md` 2.1「lastSeenAt」）；M3 加入維護模式後，改為通過維護檢查之後才更新。
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
