# 術語與代號 (Glossary)

> 依據：`adr/0017-documentation-structure-and-id-scheme.md`。
> 新增任何代號前綴之前，先登錄在第 2 節。

---

## 1. 術語

| 術語 | 意思 |
| --- | --- |
| 活動（Event） | 一場婚宴。狀態儲存為 `Draft`（準備中）或 `Live`（進行中）；`Ended`（已結束）不儲存，由活動日推算 |
| Rundown | 一場活動的完整流程表，由多個 Cue 組成 |
| Cue | 流程中的一個環節，例如「一進預備」。狀態為 `Pending`（待辦）、`InProgress`（進行中）、`Completed`（完成）、`Skipped`（略過） |
| 固定 Cue（Fixed） | 時間不能挪動的環節，例如飯店開席、吉時；平移時跳過 |
| 表定開始時間（`scheduledStart`） | 建立 Rundown 時排定的時間，平移不改它 |
| 偏移（`offsetMin`） | 每個 Cue 累計被平移的分鐘數，可為負 |
| 有效開始時間（`effectiveStart`） | 表定開始時間＋偏移，畫面與倒數都以它為準 |
| 推進 | 總召把目前進行中的 Cue 標為完成，並讓下一個待辦的 Cue 開始 |
| 時間平移 | 從指定的 Cue 起，把之後未完成、非固定的 Cue 一起延後或提早 N 分鐘 |
| 復原上一步 | 撤銷最近一次 Cue 操作（推進、平移、略過），只能一次 |
| 角色（Role） | 活動中的一個位子，例如總召、新秘；一個角色一條連結，可多支手機共用 |
| 總召（Master） | 現場指揮，唯一能改動流程的角色 |
| 新秘（Stylist） | 新娘秘書；畫面只有極大字體的倒數 |
| 新人（Host） | 新郎新娘；畫面只顯示一切順利或延誤 N 分 |
| Token／角色連結 | 角色的身分憑證，放在網址 `#t=` 之後；拿到連結就是該角色，不需登入 |
| 復原碼 | 建立活動時產生的救援碼，用來換發 Master 連結；與 Master 連結分開保存（誰保管見 `design/access-control.md` 第 2 節） |
| 快照 | 伺服器回給某個角色、該角色看得到的完整狀態；客戶端每 2 秒輪詢一次 |
| 備援操作者 | 活動當天負責伺服器維運的人（開發者本人是新人，無法操作） |
| 決定日 | 2027-04-15；未通過完整彩排就改用紙本＋LINE 群組 |
| 功能條件／情境條件 | 驗收條件的兩種：前者以自動測試驗證，後者由真人在真機上判定 |

## 2. 代號規則

| 代號 | 意思 | 正本 |
| --- | --- | --- |
| `M1`、`M2a`… | 里程碑 | `process/development-process.md` |
| `P0`、`P1`、`P2` | 功能優先層級，只用於範圍 | `requirements/scope.md` |
| `Sev0`、`Sev1`、`Sev2` | 問題嚴重度 | `process/development-process.md` |
| `V1`～`V5` | 產品原則 | `product/vision.md` |
| `US-<角色>-<n>.<m>` | 故事的功能條件；角色為 `M` 總召、`ST` 新秘、`X` 所有角色 | `requirements/stories/` |
| `US-<角色>-<n>.C<m>` | 故事的情境條件 | `requirements/stories/` |
| `NFR-<n>` | 非功能需求與品質目標 | `requirements/scope.md` 第 4 節 |
| `ADR-<NNNN>` | 決定（唯一的決定編號） | `adr/` |
| `RISK-<n>` | 風險與待驗證項目 | `docs/README.md` 狀態節 |

- 只有會被跨文件引用的東西才編號；其他用名稱稱呼。
- 測試名稱帶驗收條件編號，例如 `TestUS_M_3_2_...`。
- 第 3 節的舊代號已全部退役，只在以下地方保留：
  - `archive/`：歸檔紀錄原樣保存；引用歸檔內容時可附上舊代號，例如「`archive/clarification.md` C5」。
  - `internal/store/migrations/0001_init.sql` 的註解：已發行的遷移檔不能修改（ADR-0019）。
  - 本文件第 3 節的對照表。
- 「C4 模型」指軟體架構的 C4 圖示法，不是代號。

## 3. 舊代號對照表

ADR-0017 之前使用的代號，全部退役。同一字母在不同文件意思不同，所以依出處分組。

「去向」已於 2026-10-22 由 PO 逐條審閱（ADR-0017 步驟 2），文件與程式碼註解已依此改寫（步驟 5）。取值為：

- **規則 → `檔名` 章節**：寫進設計文件正文。
- **ADR-NNNN**：成為（或併入）該 ADR。
- **需求 → NFR-n／`檔名`**：寫進需求文件。
- **RISK-n**：風險清單。
- **刪除**：已過時或已被其他項目涵蓋，附理由。

另外兩組代號**不退役**，維持原意：里程碑 `M0`～`M3`（含 `M2a`、`M2b`），以及功能優先層級 `P0`～`P2`（`requirements/scope.md` 第 1 節）。

下表的檔名是代號**原本**所在的位置，不隨步驟 3 的搬移更新：`requirements/clarification.md` 現在是 `archive/clarification.md`，`architecture/architecture-overview.md` 現在是 `architecture/overview.md`，`requirements/m1-acceptance.md` 現在是 `requirements/stories/m1.md`。

### `requirements/clarification.md` 的 A：需求釐清：規格模糊之處

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| A1 | 時間平移（+N 分鐘）的範圍：所有後續 Cue，還是只有「關聯」Cue？：從「指定的 Cue」起，平移其後所有未完成 Cue；總召可在平移前取消勾選個別 Cue | 規則 → `design/data-model.md` 4.2 |
| A2 | 是否有「固定時間 Cue」不隨平移變動（如飯店預訂開席、儀式吉時）？：有。Cue 可標記為 Fixed，平移時跳過並在畫面上提示「與前後 Cue 產生壓縮或衝突」 | 規則 → `design/data-model.md` 4.2 |
| A3 | 平移是否可為負值（提早）？：可以，支援 −N 分鐘，但不得讓 Cue 早於目前時間（2026-10-20 修改：只限制負值平移，檢查規則見 data-model.md 4.2） | 規則 → `design/data-model.md` 4.2 |
| A4 | 廠商「就位打卡」由誰操作？：廠商自己在視圖上點「我已就位」，總召可代為打卡 | 規則 → `design/data-model.md` CheckIn、`design/screen-design.md` 角色視圖 |
| A5 | 「賓客到場率」資料來源？：迎賓在查詢結果上點「已報到」；到場率 = 已報到 ÷ 名單總數 | 需求 → `requirements/scope.md` P2 待辦（含公式） |
| A6 | Cue 與角色的關聯由誰決定？：婚前建立 Rundown 時指定；現場只有總召可調整 | 規則 → `design/data-model.md` CueRole、`design/access-control.md` 權限表；PO 決定：Live 後不能改指派（與 P8 一致） |
| A7 | 「完成」與「進行中」狀態可否回退（誤按）？：可以，總召有「復原上一步」，並記錄操作紀錄 | 規則 → `design/data-model.md` 4.1；理由見 ADR-0006、ADR-0015 |

### `requirements/clarification.md` 的 B：需求釐清：婚前準備

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| B1 | 誰建立與編輯 Rundown？：總召（或婚顧）一人為主要編輯者，可授權一位協作者 | 刪除：與 ADR-0003（只有 Master）及 D-2（第一場以指令建立）衝突，協作者從未設計；之後由 ADR-0016 的擴充方向處理 |
| B2 | Rundown 如何匯入？：MVP：手動建立 + 貼上 CSV；Excel/PDF 匯入列為後續 | 需求 → `requirements/scope.md`：指令匯入 P0（M1 完成）、貼上 CSV 介面 P1、Excel／PDF 未排入 |
| B3 | 賓客名單與桌位如何匯入？：CSV 匯入（姓名、桌號、備註）；MVP 之後再考慮 | 需求 → `requirements/scope.md` P2 待辦 |
| B4 | Token 連結如何發給各角色？：主控台產生連結與 QR Code，由總召自行以 LINE 傳送，系統不代發 | 規則 → `design/access-control.md` 第 6 節；連結管理介面 → `requirements/scope.md` P1 |
| B5 | 婚前能否預覽「各角色看到的畫面」？：能。總召可用「以某角色身分預覽」 | 需求 → `requirements/scope.md` P1；畫面細節留在 `design/screen-design.md` Rundown 編輯器 |
| B6 | 現場前是否要鎖定 Rundown？：開場時按「開始活動」後，只能由總召做現場異動 | 規則 → `design/data-model.md` 4.1 |

### `requirements/clarification.md` 的 C：需求釐清：現場與事後

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| C1 | 現場臨時新增、刪除、調整 Cue 順序？：總召可新增與編輯；刪除改為「略過」以保留紀錄；順序調整列為後續 | 規則 → `design/access-control.md` 第 4 節；調整順序 → `requirements/scope.md` 未排入 |
| C2 | 平移或變更發生時，各角色如何被通知？：畫面色彩與數字變化，加上一則「時程已更新 +N 分」的短暫提示，不強制確認 | 規則 → `design/screen-design.md` 4.4 |
| C3 | 是否保留實際時間紀錄供事後檢討？：保留（每個 Cue 的表定、實際、操作者與時間），可匯出 CSV | 規則 → `design/data-model.md` ActionLog、`design/access-control.md` 第 2 節；PO 決定：匯出列為 P1（第一場由開發者以指令匯出） |
| C4 | 一個系統同時管理幾場活動？：支援多場活動、資料互相隔離；使用者為單一總召，不做婚顧公司的多租戶 | ADR-0016 |
| C5 | 活動結束後資料與連結如何處理？：活動結束後 Token 自動失效；資料保留 90 天後刪除（2026-10-20 修改：資料庫於結束後 80 天刪除，伺服器端所有副本含備份於 90… | ADR-0011（原 90 天已修訂為資料庫第 80 天刪除、所有副本 90 天內消失） |
| C6 | 介面語言？：僅繁體中文 | 需求 → `requirements/scope.md` 限制；`design/screen-design.md` 第 2 節的同一條改為引用 |

### `requirements/clarification.md` 的 D1、D2：需求釐清：MVP 邊界的追問

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| D1 | P2 中是否有你一定要放進第一版的？ | 需求 → `requirements/scope.md` 的 P2 定義（第一版不做） |
| D2 | 第一個實際使用的場合是哪一場？日期是什麼？（會影響 MVP 要多小） | 刪除：已在 `product-vision.md` 第 1 節 |

### `requirements/clarification.md` 的 E：非功能需求

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| E1 | 單場同時在線人數：上限 50 人，一般 10~30 人 | NFR-1 |
| E2 | 「零延遲」的實際標準：總召操作後，95% 的終端在 3 秒內更新 | NFR-2（之後併入 architecture-overview Q1） |
| E3 | 離線時的行為：所有人可讀取最後同步的時間軸；總召的操作在離線時不允許，並明確提示「離線中」 | NFR-3（之後併入 architecture-overview Q2）；做法 → `design/sync-and-offline.md` 第 6 節 |
| E4 | 斷線重連：重連後自動拉取最新狀態，並以伺服器時間為準 | 規則 → `design/sync-and-offline.md` 第 5 節 |
| E5 | 裝置與瀏覽器支援：iOS Safari 與 Android Chrome 近兩個主要版本；平板為 iPad 與 Android 10~11 吋 | NFR-4；PO 決定：補上 iOS／Android 的 LINE 內建瀏覽器（連線時完整可用；離線不保證，見 ADR-0010），依 `product-vision.md` V2 |
| E6 | 是否接受要求使用者「加到主畫面」才能使用推播？：接受但不強制；核心功能不依賴推播 | 需求 → `requirements/scope.md` 限制（核心功能不依賴推播、不要求加到主畫面）；推播列 P2 |
| E7 | 時間基準：以伺服器時間為準，客戶端顯示倒數時校正時鐘偏差 | 規則 → `design/sync-and-offline.md` 第 4 節；理由見 ADR-0004、ADR-0007 |

### `requirements/clarification.md` 的 F：安全與隱私

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| F1 | Token 外流怎麼辦？：總召可一鍵撤銷並重新產生單一角色的連結 | 規則 → `design/access-control.md` 3.4 |
| F2 | Token 的權限範圍：唯讀加上該角色的有限動作（打卡、確認、認領）；只有總召連結可修改時程 | 規則 → `design/access-control.md` 第 4 節 |
| F3 | 新人視圖的雜訊屏蔽怎麼保證？：伺服器端就不下發求助與除錯訊息，不是前端隱藏 | ADR-0007；做法 → `design/api.md` 第 5 節 |
| F4 | 賓客名單的存取權限：只有迎賓與總召角色能存取名單；其他角色視圖不含名單資料 | 需求 → `requirements/scope.md` P2 待辦（隨賓客功能） |
| F5 | 是否需要符合個資法相關規範？：需要。僅蒐集姓名與桌號，不蒐集電話等其他個資，並有明確刪除機制 | NFR-5（之後併入 architecture-overview Q6）；刪除做法見 ADR-0011；「只收姓名與桌號」隨 F4 進 P2 待辦 |

### `design/data-model.md` 的 M：資料模型的設計決定（第 6 節）

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| M1 | 偏移存在每個 Cue 上（offsetMin），而不是只存一個全域值：是，原因：有 Fixed Cue、排除勾選、略過時，全域值無法表達 | 規則 → `design/data-model.md` Cue `offsetMin`；理由 ADR-0006 |
| M2 | Cue 有 durationMin，用來偵測 Fixed 衝突：是 | 規則 → `design/data-model.md` Cue `durationMin` |
| M3 | 同一時間只有一個 InProgress Cue：是 | 規則 → `design/data-model.md` 4.1 |
| M4 | 一個 Role 是一個位子，可多支手機共用同一個 Token：是 | 規則 → `design/data-model.md` Role |
| M5 | InProgress 的 Cue 不被平移：是 | 規則 → `design/data-model.md` 4.2；理由 ADR-0006 |
| M6 | 平移造成 Fixed 衝突時：警告而不阻擋：是 | 規則 → `design/data-model.md` 4.2；理由 ADR-0006 |
| M7 | 「復原」只回退最近一步：是 | 規則 → `design/data-model.md` 4.1；理由 ADR-0015 |
| M8 | 平移的「不得早於現在」檢查：只在負值平移時檢查，已逾時的 Cue 自動排除（4.2） | 規則 → `design/data-model.md` 4.2；理由 ADR-0006 |
| M9 | 活動結束時間：不儲存，由 eventDate 與 timezone 即時算出 endsAt；Token 不另存到期時間 | 規則 → `design/data-model.md` Event 衍生值；理由 ADR-0011 |
| M10 | 略過能否復原：可以，以「復原上一步」回到 Pending（4.1） | 規則 → `design/data-model.md` 4.1；理由 ADR-0015 |

### `design/access-control.md` 的 P：權限的設計決定（第 7 節）

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| P1 | 管理身分：只有 Master 一種管理身分（原 Owner 併入）；復原碼由系統產生，用途見第 2 節 | 規則 → `design/access-control.md` 第 2 節；理由 ADR-0003；PO 決定：復原碼改由備援操作者保管（ADR-0018） |
| P2 | Token 放在 URL 片段（#t=），不放路徑或查詢字串：是，而且一直留在網址上不清除；外流以撤銷重發處理 | 規則 → `design/access-control.md` 3.2；理由 ADR-0002 |
| P3 | 連結生效時間：取消 notBefore，連結產生即可使用；活動前顯示「活動尚未開始」 | 規則 → `design/access-control.md` 3.3；理由 ADR-0002 |
| P4 | 活動結束後 Token 失效：活動日隔天 04:00 自動結束並失效，不需手動結束；結束時間由活動日期即時算出 | 規則 → `design/access-control.md` 3.3；理由 ADR-0011 |
| P5 | Master Token 可重發一般角色的連結，但不能動 Master 自己的：是；Master 連結只能以復原碼換發 | 規則 → `design/access-control.md` 第 2 節；理由 ADR-0003 |
| P6 | 撤銷的生效方式：下一次輪詢即回應「無效」；本機資料的清除規則見 3.3 | 規則 → `design/access-control.md` 3.4 |
| P7 | 驗證失敗的回應：分「無效／已結束／有效」三種結構化回應；不對 Token 與復原碼限頻 | 規則 → `design/access-control.md` 3.3（理由在正文） |
| P8 | Live 階段的編輯限縮為：改標題與備註、新增 Cue、略過：是 | 規則 → `design/access-control.md` 第 4 節，補上「Live 後不能改指派」（A6） |

### `design/sync-and-offline.md` 的 S：同步與離線的決定

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| S1 | 客戶端以固定間隔每 2 秒輪詢，伺服器每次都回該角色的完整快照；不使用 SSE 或 WebSocket。總召操作走一般 API 請求，回應直接附上新快照（2026-10-20 修改… | 規則 → `design/sync-and-offline.md` 第 1 節；理由 ADR-0004 |
| S2 | 每次回應「該角色可見的完整狀態快照」，不回差異 | 規則 → `design/sync-and-offline.md` 第 2 節；理由 ADR-0004 |
| S3 | 伺服器送 effectiveStart 與伺服器時間；客戶端用本機時鐘加校正值自行倒數 | 規則 → `design/sync-and-offline.md` 第 4 節；理由 ADR-0004 |
| S4 | 離線快取最後一次快照，唯讀顯示並標示「離線中，資料更新於 HH:MM」，倒數照跑 | 規則 → `design/sync-and-offline.md` 第 6 節；理由 ADR-0010 |
| S5 | 總召離線時禁止操作並提示，不做離線佇列 | 規則 → `design/sync-and-offline.md` 第 6 節；理由 ADR-0005 |
| S6 | 多台總召裝置可同時操作；每個操作帶版本號，過期就拒絕並更新畫面，不做合併 | 規則 → `design/sync-and-offline.md` 第 3 節；理由 ADR-0005 |
| S7 | 部署與託管：Google Cloud Compute Engine 台灣（asia-east1）單一 VM + SQLite + Litestream 備份到 GCS，詳見 tec… | 規則 → `design/tech-stack.md` 第 3 節（步驟 3 移到 `operations/runbook.md`）；理由 ADR-0009 |

### `design/screen-design.md` 的 V：畫面編號（第 1 節）

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| V1 | 總召主控台 | 改稱「總召主控台」 |
| V2 | 角色視圖（通用） | 改稱「角色視圖」 |
| V2a | 新秘模式（V2 的變體） | 改稱「新秘模式」 |
| V3 | Rundown 編輯器 | 改稱「Rundown 編輯器」 |
| V4 | 角色與連結管理 | 改稱「角色與連結管理」 |
| V5 | 備援 Rundown 匯出 | 改稱「備援 Rundown（列印頁）」 |
| V6 | 共通狀態層 | 改稱「共通狀態層」 |

### `design/screen-design.md` 的 U：畫面細節的決定（第 10 節）

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| U1 | 推進按鈕是否需要二次確認：不需要，靠「復原上一步」 | 規則 → `design/screen-design.md` 3.3 |
| U2 | 平移是否一律先經預覽面板：是 | 規則 → `design/screen-design.md` 3.3 |
| U3 | 新秘模式的就位打卡放在進入畫面前，不放在倒數畫面：是；另外，未取得常亮時以全螢幕提示層請使用者點一下（實機驗證 iOS 重開頁面後需點擊才能常亮） | 規則 → `design/screen-design.md` 第 5 節 |
| U4 | 預設暗色主題：是，可切換亮色 | 規則 → `design/screen-design.md` 第 2 節；`requirements/scope.md` 的 P1 清單移除「暗色主題」 |
| U5 | 平移預覽的起點預設為「下一個 Pending Cue」，可手動改：是 | 規則 → `design/screen-design.md` 3.3 |
| U6 | 已完成的 Cue 在時間軸中淡化並可收合：是 | 規則 → `design/screen-design.md` 3.5 |
| U7 | 倒數背景色門檻：>15 平靜、5～15 琥珀、<5 紅：是，之後可調 | 規則 → `design/screen-design.md` 第 5 節；驗收 US-ST-1.3 |
| U8 | 新秘模式 V2a 是否與一般 V2 分開，由角色類型決定（非使用者切換）：是 | 規則 → `design/screen-design.md` 第 5 節 |
| U9 | 新秘倒數的目標：下一個指派給新秘的 Pending Cue 的 effectiveStart，與 V2 相同；沒有 Pending 也沒有進行中的 Cue 時顯示「今日任務已完成」 | 規則 → `design/screen-design.md` 第 5 節；驗收 US-ST-1.1、US-ST-1.4 |

### `design/tech-stack.md` 的 T：技術選型細節（第 2 節）

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| T1 | 語言：Go（最新穩定版） | 規則 → `design/tech-stack.md` 2.1「語言」；理由 ADR-0008 |
| T2 | HTTP：標準庫 net/http（含方法與路徑樣式路由），不用框架 | 規則 → `design/tech-stack.md` 2.1「HTTP」；理由 ADR-0008 |
| T3 | 資料庫：SQLite，WAL 模式；驅動 modernc.org/sqlite（純 Go，免 cgo） | 規則 → `design/tech-stack.md` 2.1「資料庫」；理由 ADR-0009 |
| T3a | 連線：開兩個 sql.DB。寫入專用：MaxOpenConns = 1、_txlock=immediate，DSN 帶 journal_mode(WAL)、busy_timeout… | 規則 → `design/tech-stack.md` 2.1「連線」 |
| T3b | 逾時與錯誤：輪詢的資料庫查詢帶 3 秒 context 逾時；資料庫錯誤（含逾時、SQLITE_BUSY）一律回伺服器錯誤，絕不回「無效」 | 規則 → `design/tech-stack.md` 2.1「逾時與錯誤」；理由 ADR-0012 |
| T3c | 不自動建庫：bethere serve 找不到資料庫檔時拒絕啟動。只有第一次部署時設定 BETHERE_INIT=1 才建立新資料庫；BETHERE_INIT=1 而資料庫檔已存在… | 規則 → `design/tech-stack.md` 2.1「不自動建庫」；理由 ADR-0012 |
| T3d | 維護模式：VM 的 metadata bethere-maintenance=1 時（服務每次啟動前向 metadata server 讀取；鍵不存在視為 0；讀取失敗就拒絕啟動，… | 規則 → `design/tech-stack.md` 2.1「維護模式」；操作步驟 → `operations/runbook.md`；理由 ADR-0012 |
| T4 | SQL 與遷移：database/sql + 手寫參數化 SQL；遷移為內嵌編號 SQL 檔，以 PRAGMA user_version 記錄；遷移只做向後相容的新增（加表、加可為… | 規則 → `design/tech-stack.md` 2.1「SQL 與遷移」；PO 決定：理由與例外寫成 ADR-0019 |
| T5 | 平移預覽：伺服器提供「試算」端點（同一套演算法、不寫入），預覽面板每次勾選變更都呼叫它 | 規則 → `design/tech-stack.md` 2.1「平移預覽」；理由 ADR-0007 |
| T6 | Token 與復原碼：crypto/rand 產生；Token 16 位元組以 base64url 編碼；復原碼 16 位元組以 Crockford Base32 分組；兩者以 S… | 規則 → `design/tech-stack.md` 2.1「Token 與復原碼」；理由 ADR-0002 |
| T7 | lastSeenAt：只存在記憶體（map[tokenHash]time.Time + mutex），不寫資料庫；只在 Token 驗證成功、且未被維護模式擋下之後才更新；V4 直… | 規則 → `design/tech-stack.md` 2.1「lastSeenAt」（P1） |
| T8 | QR Code：伺服器在產生 Token 的同一個回應中附上 PNG data URI；函式庫鎖定版本（候選 skip2/go-qrcode，QR 規格穩定） | 規則 → `design/tech-stack.md` 2.1「QR Code」（P1） |
| T9 | CSV：匯入（B2）與事後紀錄匯出（C3）都在伺服器以 encoding/csv 處理；匯入先回傳驗證預覽；匯出加 UTF-8 BOM | 規則 → `design/tech-stack.md` 2.1「CSV」；匯入目前走指令（M1 完成），匯入預覽介面與匯出為 P1 |
| T10 | 資料刪除（C5 修訂）：每日排程刪除 endsAt 已超過 80 天的活動，secure_delete 讓內容被覆寫，checkpoint 交給 Litestream 管理，應用程… | 規則 → `design/tech-stack.md` 2.1「資料刪除」；驗收步驟 → `operations/runbook.md`；理由 ADR-0011 |
| T11 | 伺服器端指令：同一執行檔的子命令：bethere create-event、bethere reset-recovery-code；以 gcloud compute ssh --t… | 規則 → `design/tech-stack.md` 2.1「伺服器端指令」，已改為實際指令清單；PO 決定：`reset-recovery-code` 排在 M3 |
| T12 | 時間：伺服器一律 UTC；時區資料以 time/tzdata 內嵌；活動時區只接受內嵌資料逐字收錄的名稱清單（domain.SupportedTimeZone，目前只有 Asia/… | 規則 → `design/tech-stack.md` 2.1「時間」 |
| T13 | 日誌：log/slog 輸出到 stdout；不記錄請求標頭與本文 | 規則 → `design/tech-stack.md` 2.1「日誌」；正本 `design/access-control.md` 第 2 節 |
| T14 | 安全標頭：Go 中介層統一加 CSP：default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-… | 規則 → `design/tech-stack.md` 2.1「安全標頭」 |
| T15 | 框架：TypeScript + Preact，Vite 打包；不使用動態 import，輸出單一 JS bundle | 規則 → `design/tech-stack.md` 2.2「框架」；理由 ADR-0008 |
| T16 | 路由：路徑決定入口，不用路由函式庫：/（依快照角色類型顯示 V1／V2／V2a）、/print（V5 列印頁）、/recover（復原碼入口，不需 Token）。Master 的 … | 規則 → `design/tech-stack.md` 2.2「路由」（畫面代號改名稱） |
| T17 | 型別：手寫 web/src/api.ts；後端以測試比對快照 JSON 的鍵集合，防止前後端欄位漂移 | 規則 → `design/tech-stack.md` 2.2「型別」 |
| T18 | 離線外殼：vite-plugin-pwa（Workbox generateSW）預先快取打包產物（含三個入口）；/api/ 一律不攔截；skipWaiting + clientsC… | 規則 → `design/tech-stack.md` 2.2「離線外殼」；理由 ADR-0010 |
| T19 | Web App manifest：P0 不提供；依 R1 實機結果再決定 | 規則 → `design/tech-stack.md` 2.2「Web App manifest」 |
| T20 | 本機快照：localStorage，鍵為 snap: + SHA-256（"bethere-local:" + Token）（crypto.subtle，HTTPS 下必定可用），… | 規則 → `design/tech-stack.md` 2.2「本機快照」；理由 ADR-0010 |
| T21 | 備援 Rundown：列印專用網頁 /print（@media print），由瀏覽器列印或另存 PDF；資料來自目前快照（離線時用本機快照）。Master 的快照帶完整 CueR… | 規則 → `design/tech-stack.md` 2.2「備援 Rundown」；理由 ADR-0014 |
| T22 | LINE 內建瀏覽器：視為受限環境：預設 Service Worker 不可用（iOS 版 LINE 為 WKWebView）、列印可能無效。偵測到 LINE 時在畫面上引導「以 … | 規則 → `design/tech-stack.md` 2.2「LINE 內建瀏覽器」；理由 ADR-0010；PO 決定：連線時照常可用，只顯示非強制提示（依 V2；程式已如此實作） |

### `design/tech-stack.md` 的 R：實機驗證項目（第 5 節）

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| R1 | #t= 在「LINE → 以 Safari 開啟」、加入書籤、加到主畫面時是否保留 | 驗證紀錄留在 `design/tech-stack.md` 5.1；Android 未完成 → RISK-1 |
| R2 | 外殼能否離線開啟（LINE 預設不能，見 T22） | 驗證紀錄留在 `design/tech-stack.md` 5.1；Android 未完成 → RISK-1 |
| R3 | Wake Lock（新秘 V2a 是 P0 核心） | 驗證紀錄留在 `design/tech-stack.md` 5.1；Android 未完成 → RISK-1 |
| R4 | 台灣（含行動網路）到 asia-east1 的輪詢往返在 600 ms 內 | 驗證紀錄留在 `design/tech-stack.md` 5.1；Android 未完成 → RISK-1；負載測試部分隨 architecture-overview K2 歸位 |
| R5 | 列印與另存 PDF | 驗證紀錄留在 `design/tech-stack.md` 5.1；Android 未完成 → RISK-1 |

### `design/tech-stack.md` 的 T-A～T-F：技術選型的決定紀錄（第 6 節）

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| T-A | 後端 Go、前端 TypeScript + Preact | 刪除：ADR-0008 |
| T-B | GCE 台灣（asia-east1）單一 e2-micro VM + Caddy + SQLite + Litestream→GCS，自訂網域與靜態 IP（S7）；不準備跨區域備援… | 刪除：ADR-0009 |
| T-C | 備援 Rundown 用列印專用網頁 | 刪除：ADR-0014 |
| T-D | 平移預覽由伺服器試算 | 刪除：ADR-0007 |
| T-E | 當天維運由指定的備援操作者負責 | 刪除：ADR-0013 |
| T-F | 資料庫於 endsAt 後 80 天刪除，伺服器端所有副本 90 天內消失（修訂 C5） | 刪除：ADR-0011 |

### `design/api.md` 的 A：API 的決定（第 9 節）

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| A1 | Token 用 Authorization: Bearer 標頭 | 規則 → `design/api.md` 第 1 節 |
| A2 | 雜湊的輸入為 22 字元的 Token 字串本身 | 規則 → `design/api.md` 第 1 節 |
| A3 | Token 驗證的請求不帶活動或自己的角色 id | 規則 → `design/api.md` 第 1 節 |
| A4 | M1 只開放 Master 與 Stylist；P0 不能建立 P1 角色類型 | 規則 → `design/api.md` 第 4 節 |
| A5 | 角色快照不揭露未指派給自己的進行中 Cue | 規則 → `design/api.md` 5.3、5.5；攝影、音控是否顯示目前環節留到 M2b 決定 |
| A6 | 保留 GET /api/ping（不需憑證） | 規則 → `design/api.md` 第 3 節 |
| A7 | 移除 POST /api/report | 刪除：M1 已移除，有測試確認 |
| A8 | Draft 階段也回完整的角色快照 | 規則 → `design/api.md` 5.6 |
| A9 | 查 Token 與組快照在同一個讀取交易 | 規則 → `design/api.md` 第 7 節 |
| A10 | BETHERE_INIT 只接受 1 | 規則 → `design/api.md` 第 7 節；理由 ADR-0012 |
| A11 | 子命令的秘密只印到終端機 | 規則 → `design/api.md` 第 7 節 |
| A12 | 寫入型子命令從標準輸入讀取 Master 連結 | 規則 → `design/api.md` 第 7 節 |
| A13 | serve 只用系統時鐘 | 規則 → `design/api.md` 第 1 節 |
| A14 | 所有快照只由一個函式產生，且一律是呼叫者自己角色的快照 | 規則 → `design/api.md` 第 7 節 |
| A15 | 客戶端清除本機資料前，另外確認 HTTP 200 與 JSON | 規則 → `design/api.md` 第 7 節、2.1 |
| A16 | 瀏覽器本機快照的鍵改為 SHA-256（"bethere-local:" + Token），不等於伺服器的 Token 雜湊 | 刪除：修訂紀錄；規則在 `design/tech-stack.md` 2.2「本機快照」 |
| A17 | 復原描述的命名規則（見 5.2 undo） | 規則 → `design/api.md` 5.2 |

### `design/api.md` 的 F：安全審查發現（第 10 節）

嚴重度在這裡用到 P2～P4，比 `process/development-process.md` 第 5 節的定義多兩級。

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| F1 | 未指派的進行中 Cue 標題外洩，與 US-X-1.2 衝突（嚴重度 P2；處理：修正（A5）） | 刪除（審查紀錄，PO 決定不保留，見 git 紀錄）；修正落在 `design/api.md` 5.3 |
| F2 | 子命令以什麼身分寫入未定義（嚴重度 P2；處理：修正（A12）） | 刪除（審查紀錄，PO 決定不保留，見 git 紀錄）；修正落在 `design/api.md` 第 7 節 |
| F3 | 寫入回應與 409 的快照來源與對照未定義（嚴重度 P3；處理：修正（第 7 節、A14）） | 刪除（審查紀錄，PO 決定不保留，見 git 紀錄）；修正落在 `design/api.md` 第 7 節 |
| F4 | 單一交易需要新的 store 函式（嚴重度 P3；處理：修正（第 7 節）） | 刪除（審查紀錄，PO 決定不保留，見 git 紀錄）；修正落在 `design/api.md` 第 7 節 |
| F5 | 雜湊規則不一致會清掉所有裝置（嚴重度 P3；處理：修正（測試向量與端到端測試）） | 刪除（審查紀錄，PO 決定不保留，見 git 紀錄）；修正落在 測試向量與端到端測試 |
| F6 | 逾時或取消可能被誤判為查無 Token（未證實）（嚴重度 P3；處理：以測試確認） | 刪除（審查紀錄，PO 決定不保留，見 git 紀錄）；修正落在 測試（逾時、取消回 500） |
| F7 | 客戶端清除條件未檢查 HTTP 狀態（嚴重度 P3；處理：修正（A15）） | 刪除（審查紀錄，PO 決定不保留，見 git 紀錄）；修正落在 `design/api.md` 第 7 節 |
| F8 | 覆寫時鐘的風險（嚴重度 P3；處理：修正（A13）） | 刪除（審查紀錄，PO 決定不保留，見 git 紀錄）；修正落在 `design/api.md` 第 1 節 |
| F9 | 錯誤回應的 no-store（嚴重度 P4；處理：以測試確認） | 刪除（審查紀錄，PO 決定不保留，見 git 紀錄）；修正落在 測試（每個 API 回應檢查 no-store） |
| F10 | 瀏覽器的鍵等於伺服器的雜湊（嚴重度 P4；處理：修正（A16）） | 刪除（審查紀錄，PO 決定不保留，見 git 紀錄）；修正落在 `design/tech-stack.md` 2.2「本機快照」 |
| F11 | version 不合法的處理（嚴重度 P4；處理：修正（第 1 節）） | 刪除（審查紀錄，PO 決定不保留，見 git 紀錄）；修正落在 `design/api.md` 第 1 節 |
| F12 | 規格涵蓋 M1 以外的角色（嚴重度 P4；處理：修正（A4）） | 刪除（審查紀錄，PO 決定不保留，見 git 紀錄）；修正落在 `design/api.md` 第 4 節 |
| F13 | /api/report 仍在程式中（嚴重度 P4；處理：M1 實作時移除並測試） | 刪除（審查紀錄，PO 決定不保留，見 git 紀錄）；修正落在 M1 已移除，有測試 |
| F14 | ping 揭露版本號、Cue id 遞增（嚴重度 P4；處理：接受） | RISK-8 |
| （未編號） | 維護模式延到 M3；M3 前正式環境不放真實資料（嚴重度 P4；處理：接受） | RISK-9 |

### `architecture/architecture-overview.md` 的 Q：品質目標（第 2 節）

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| Q1 | 即時性：95% 的終端 3 秒內更新（E2） | NFR-2（與 E2 合併） |
| Q2 | 可用性（降級）：已開過頁面的裝置仍能唯讀顯示時程、倒數照跑；另有紙本 | NFR-3（與 E3 合併） |
| Q3 | 資料安全（失效安全）：任何失敗都表現為「離線」，不會讓裝置清掉快照 | NFR-6 |
| Q4 | 易用性：點一下就能用，不需註冊；新秘雙手不空也看得到倒數 | 刪除：與 `product-vision.md` V2、US-X-1、US-ST-1 的情境條件重複 |
| Q5 | 正確性：結果與 data-model.md 4.2 一致，可精確復原 | NFR-7 |
| Q6 | 隱私：連結立即失效；伺服器端所有副本 90 天內消失 | NFR-5（與 F5 合併） |
| Q7 | 可恢復性：備援操作者 15 分鐘內恢復 | NFR-8 |

### `architecture/architecture-overview.md` 的 K：風險與技術債（第 8 節）

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| K1 | Android 上 #t= 是否保留（R1） | RISK-1（與 R1～R5 合併） |
| K2 | 延遲預算沒有緩衝（Q1） | RISK-2 |
| K3 | 單一 VM 單點故障 | RISK-3（刻意接受） |
| K4 | 當天維運手冊仍是要求草案 | RISK-4 |
| K5 | 尚未找到備援操作者 | RISK-5（與 D-3、ADR-0018 合併） |
| K6 | 尚未購買正式網域 | RISK-6 |
| K7 | iOS 重開頁面後需點擊才能常亮 | 刪除：已由新秘模式的常亮提示層解決（`design/screen-design.md` 第 5 節） |
| K8 | iOS 對 7 天未互動網站清除資料 | RISK-7 |

### `requirements/m1-acceptance.md` 的 Q：M1 的 PO 決定（第 2 節）

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| Q1 | 「復原上一步」是否放進 M1：放進 M1（US-M-4） | 刪除：已反映為 US-M-4 |
| Q2 | Rundown 檔格式：CSV（格式見 US-M-0） | 刪除：格式在 US-M-0 |
| Q3 | M1 彩排由誰扮演總召與新秘：PO 找兩位親友，各用自己的 iPad／iPhone | 刪除：已過期，真人彩排移到 M2a，人選歸入 RISK-5 |
| Q4 | 情境條件的數字：先照本文件，彩排後依結果調整 | 規則 → `process/development-process.md` 第 4 節（通則） |

### `process/development-process.md` 的 P0～P2：嚴重度（第 5 節）

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| P0 | 活動當天全場無法使用、資料外洩給不該看的人、資料遺失 | Sev0 |
| P1 | P0 故事的驗收條件不成立，或安全保證被破壞 | Sev1 |
| P2 以下 | 有替代方式或影響有限 | Sev2 |

### `process/development-process.md` 的 D-1～D-4：流程的 PO 決定（第 7 節）

| 舊代號 | 內容 | 去向 |
| --- | --- | --- |
| D-1 | 2027-04-15 為決定日：未通過完整彩排就改用紙本 + LINE 群組：採用 | 規則 → `process/development-process.md` 第 3 節「決定日」列；`product-vision.md` V4 |
| D-2 | P0 範圍縮小：CSV 匯入、以角色預覽、連結管理介面改由開發者以指令操作，或延到 P1：縮小；第一場的婚前操作由開發者以指令完成（clarification.md 已確認事項） | 需求 → `requirements/scope.md`（B2、B4、B5 已逐項反映） |
| D-3 | 當天的總召與備援操作者是誰：未定。M1 彩排前若仍未定，由 PO 找一位親友代為扮演總召；此項是時程風險，每個里程碑檢查一次 | RISK-5 |
| D-4 | agent 可自行 push 功能分支（不含 main），讓 CI 每次都執行：授權；main 的合併與 push 仍由 PO 決定 | 規則 → `process/development-process.md` 第 6 節 |
