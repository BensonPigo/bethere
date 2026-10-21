# 技術選型 (Tech Stack) — P0 範圍

> 依據：`data-model.md`、`sync-and-offline.md`、`access-control.md`、`screen-design.md`。本文件決定 S7（部署與託管），以及各設計文件留給技術選型的項目。
> 前提：一人開發；第一場使用為 2027/06/06，**開發者本人就是當天的新人，無法操作任何維運**；單場同時在線 ≤ 50 人（E1），約每秒 25 個輪詢請求。

---

## 1. 總覽

```
┌──────────── 手機／平板瀏覽器 ────────────┐
│ Preact + TypeScript（Vite 打包成單一 bundle）│
│ Service Worker：只快取靜態外殼            │
│ localStorage：依 Token 雜湊存最後一份快照  │
└──────────────┬───────────────────────────┘
               │ HTTPS（自訂網域），每 2 秒 GET 快照；寫入走 POST
┌──────────────▼───────────────────────────┐
│ GCE 台灣（asia-east1），一台 e2-micro VM    │
│ Caddy（TLS）──► Go 執行檔                   │
│   Go：net/http + 內嵌前端檔案 + 子命令      │
│   由 Litestream 監管；SQLite（WAL）於開機磁碟│
│ Litestream ──► GCS bucket（持續備份）       │
└──────────────────────────────────────────┘
```

不使用容器。部署單位是一個「發行版」：Go 執行檔（內嵌前端）加上鎖定版本的 Litestream 與 Caddy，存放在 GCS 的發行版 bucket。API、靜態檔案、伺服器端指令與定期清理都在 Go 執行檔裡。

## 2. 決定

### 2.1 後端

| # | 項目 | 選擇 | 理由 |
| --- | --- | --- | --- |
| T1 | 語言 | Go（最新穩定版） | 單一執行檔、記憶體小、標準庫足夠；設計把邏輯集中在伺服器，前後端幾乎無共用程式碼 |
| T2 | HTTP | 標準庫 `net/http`（含方法與路徑樣式路由），不用框架 | API 只有十幾個端點 |
| T3 | 資料庫 | SQLite，WAL 模式；驅動 `modernc.org/sqlite`（純 Go，免 cgo） | 單場資料量極小；`data-model.md` 4.2 的「單一交易」對應一筆寫入交易 |
| T3a | 連線 | 開兩個 `*sql.DB`。**寫入專用**：`MaxOpenConns = 1`、`_txlock=immediate`，DSN 帶 `journal_mode(WAL)`、`busy_timeout(5000)`、`secure_delete(ON)`；啟動時**先開寫入連線並完成遷移**，再開**唯讀連線池**（DSN 帶 `busy_timeout(5000)`）。PRAGMA 一律寫在 DSN，每條連線都會套用 | WAL 下讀不擋寫；讀寫分開後，輪詢不會排在慢寫入（清理、等待子命令的鎖）後面 |
| T3b | 逾時與錯誤 | 輪詢的資料庫查詢帶 3 秒 context 逾時；資料庫錯誤（含逾時、`SQLITE_BUSY`）一律回伺服器錯誤，絕不回「無效」 | `access-control.md` 3.3：只有確實查無有效 Token 才回「無效」 |
| T3c | 不自動建庫 | `bethere serve` 找不到資料庫檔時**拒絕啟動**。只有第一次部署時設定 `BETHERE_INIT=1` 才建立新資料庫；`BETHERE_INIT=1` 而資料庫檔已存在時同樣**拒絕啟動**，所以忘記移除會在下一次開機就暴露 | 避免還原失敗時悄悄起一個空庫，讓所有裝置收到「無效」而清掉離線快取 |
| T3d | 維護模式 | VM 的 metadata `bethere-maintenance=1` 時（服務**每次啟動**前向 metadata server 讀取；鍵不存在視為 0；讀取失敗就拒絕啟動，不預設為開放），**只限制 `/api/`**：Master Token 的請求正常處理，其他 `/api/` 請求回伺服器錯誤（客戶端視為離線、保留快照；復原碼只放行「換發 Master」，Master 連結遺失時仍可換發，換發後即可用 Master 匯出；其他復原碼用途不放行）。`/healthz` 與靜態檔照常回應。Master 的快照帶維護旗標，在 V6 共通狀態層對 Master 顯示醒目橫幅「維護中，其他角色無法連線」 | 還原後先讓總召核對，再對全場開放（3.1）；健康檢查不能失敗，總召在沒快取過外殼的裝置上也要能開頁面 |
| T4 | SQL 與遷移 | `database/sql` + 手寫參數化 SQL；遷移為內嵌編號 SQL 檔，以 `PRAGMA user_version` 記錄；**遷移只做向後相容的新增**（加表、加可為空欄位）；程式遇到比自己新的 `user_version` 照常啟動 | 表少，不需 ORM；可退回上一個發行版 |
| T5 | 平移預覽 | 伺服器提供「試算」端點（同一套演算法、不寫入），預覽面板每次勾選變更都呼叫它 | 平移邏輯只有 Go 一份；總召離線時本來就不能操作（S5） |
| T6 | Token 與復原碼 | `crypto/rand` 產生；Token 16 位元組以 base64url 編碼；復原碼 16 位元組以 Crockford Base32 分組；兩者以 SHA-256 雜湊儲存並以雜湊查詢 | 熵已 128 位元，不需要慢雜湊 |
| T7 | `lastSeenAt` | 只存在記憶體（`map[tokenHash]time.Time` + mutex），不寫資料庫；**只在 Token 驗證成功、且未被維護模式擋下之後**才更新；V4 直接讀取，重啟後歸零（`data-model.md`、`access-control.md` 同步修訂） | 只用於 V4 顯示「最近有人在用」；少一個寫入來源；隨機 Token 無法讓 map 無限成長 |
| T8 | QR Code | 伺服器在產生 Token 的同一個回應中附上 **PNG data URI**；函式庫鎖定版本（候選 `skip2/go-qrcode`，QR 規格穩定） | 明文 Token 只在產生當下存在；前端不需額外套件 |
| T9 | CSV | 匯入（B2）與事後紀錄匯出（C3）都在伺服器以 `encoding/csv` 處理；匯入先回傳驗證預覽；匯出加 UTF-8 BOM | 驗證規則只寫一份；Excel 開中文不亂碼 |
| T10 | 資料刪除（C5 修訂） | 每日排程刪除 `endsAt` 已超過 **80 天**的活動，`secure_delete` 讓內容被覆寫，checkpoint 交給 Litestream 管理，應用程式不自行執行（Litestream 持有讀取交易，外部的 `wal_checkpoint(TRUNCATE)` 可能一直回 busy）；演練時確認刪除後的資料已不在資料庫檔與 WAL 檔中。伺服器端各副本：Litestream 快照間隔 24h、保留 72h（實作時選定版本號，寫入發行版與本文件，保留設定依該版本文件）；**不排程磁碟快照**、不建立含資料的自訂映像；故障後留下的舊磁碟依 3.3 刪除。備份 bucket **關閉 soft delete（保留期 0）、不開物件版本控制**，否則已刪除的物件還會多留 7 天；**不設**生命週期規則，以免資料庫閒置時刪光唯一的備份；改由 3.3 在第 85 天驗收，未達成就手動刪除。合計**伺服器端**所有副本在 `endsAt` 後 **90 天內**消失（80 + 1 + 4 ≤ 90）。前提是主機常駐到刪除完成，否則依 3.3 手動銷毀 | C5 原為「保留 90 天後刪除」，但備份會再多留數天；改為「伺服器端所有副本 90 天內消失」才符合 F5 的刪除承諾。裝置上的 localStorage 依 `sync-and-offline.md` 第 6 節在再次開啟時清除，紙本不在系統控制範圍。代價是 C3 事後匯出的期限從 90 天縮為 80 天（`clarification.md`、`data-model.md`、`sync-and-offline.md` 同步修訂） |
| T11 | 伺服器端指令 | 同一執行檔的子命令：`bethere create-event`、`bethere reset-recovery-code`；以 `gcloud compute ssh --tunnel-through-iap` 登入 VM 後執行 | 對應「建立活動由開發者以伺服器端指令執行」；輸出只到終端機 |
| T12 | 時間 | 伺服器一律 UTC；時區資料以 `time/tzdata` 內嵌；時鐘以介面注入，測試可固定「現在」 | `endsAt` 與「開始活動僅限當天」都依活動時區計算 |
| T13 | 日誌 | `log/slog` 輸出到 stdout；不記錄請求標頭與本文 | Token 在標頭、復原碼在本文 |
| T14 | 安全標頭 | Go 中介層統一加 CSP：`default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'`（無 inline script）、`Referrer-Policy: no-referrer`、`X-Robots-Tag: noindex`；API 加 `Cache-Control: no-store` | `access-control.md` 3.2、3.3；防點擊劫持 |

### 2.2 前端

| # | 項目 | 選擇 | 理由 |
| --- | --- | --- | --- |
| T15 | 框架 | TypeScript + Preact，Vite 打包；**不使用動態 import**，輸出單一 JS bundle | API 與 React 相同、執行期約 4 KB；單一 bundle 讓新版 Service Worker 接手時，舊頁面不會載入已被清掉的分塊 |
| T16 | 路由 | **路徑**決定入口，不用路由函式庫：`/`（依快照角色類型顯示 V1／V2／V2a）、`/print`（V5 列印頁）、`/recover`（復原碼入口，不需 Token）。Master 的 V1／V3／V4 以畫面內狀態切換。頁面間跳轉一律保留 `#t=` | 網址片段被 `#t=` 佔用，只能用路徑；Go 對這三個路徑都回傳 `index.html` |
| T17 | 型別 | 手寫 `web/src/api.ts`；後端以測試比對快照 JSON 的鍵集合，防止前後端欄位漂移 | 快照僅十幾個欄位，不值得引入產生器 |
| T18 | 離線外殼 | `vite-plugin-pwa`（Workbox `generateSW`）預先快取打包產物（含三個入口）；`/api/` 一律不攔截；`skipWaiting` + `clientsClaim`；註冊腳本以外部檔案載入（不用 inline）；不產生 manifest | 對應 `sync-and-offline.md` 第 6 節；活動前 7 天凍結部署，當天不會出現前後端版本不一致 |
| T19 | Web App manifest | P0 不提供；依 R1 實機結果再決定 | 避免 `start_url` 蓋掉帶 Token 的網址 |
| T20 | 本機快照 | `localStorage`，鍵為 `snap:` + Token 的 SHA-256（`crypto.subtle`，HTTPS 下必定可用）；不在本機儲存明文 Token | 共用裝置上列舉 localStorage 不會取得 Token |
| T21 | 備援 Rundown | 列印專用網頁 `/print`（`@media print`），由瀏覽器列印或另存 PDF；資料來自目前快照（離線時用本機快照）。Master 的快照帶**完整 CueRole**（各角色與 `roleNote`），Master 版才能從快照產生 | 不需 PDF 函式庫；Safari／Chrome 離線也能匯出 |
| T22 | LINE 內建瀏覽器 | 視為**受限環境**：預設 Service Worker 不可用（iOS 版 LINE 為 WKWebView）、列印可能無效。偵測到 LINE 時在畫面上引導「以 Safari／Chrome 開啟」，V5 一律引導改用系統瀏覽器列印 | 離線外殼與列印都依賴系統瀏覽器 |

## 3. 部署與託管（S7）

| 項目 | 選擇 |
| --- | --- |
| 平台 | Google Cloud Compute Engine，區域 `asia-east1`（台灣），台灣往返約 5～15 ms |
| 規格 | **恰好一台** `e2-micro`（2 個共享 vCPU、1 GB 記憶體）、10 GB `pd-balanced` 開機磁碟（`auto-delete=false`，刪 VM 時保留磁碟）、Debian 12、1 GB swap 檔、不安裝 Ops Agent；`onHostMaintenance=MIGRATE`、`automaticRestart=true`。預估每月約 US$11～12（VM 約 7、對外 IPv4 約 3.6、磁碟約 1；實際以官方計價為準）。`e2-micro` 的免費額度只限美國區域，台灣不適用 |
| 網路 | 保留一個**區域性靜態 IP**，重建 VM 時沿用，不必改 DNS。使用**自訂 VPC**（不用內建 `default-allow-ssh`／`rdp` 規則的預設網路）：對外只開 80、443；22 只開放給 IAP 的來源網段 `35.235.240.0/20`。Go 服務只綁 `127.0.0.1`，對外一律經 Caddy |
| 網域與 TLS | **發出任何連結前**就設定自訂網域，A 紀錄指向靜態 IP；Caddy 以 Let's Encrypt 自動簽發與更新憑證（Let's Encrypt 失敗時改用 ZeroSSL），反向代理到本機的 Go 服務。重建 VM 會重新簽發；同一組網域每週最多 5 張重複憑證，所以演練一律用另一個子網域 |
| 備份 | Litestream 持續複製到同區域的 GCS bucket（版本與保留期見 T10）；**不需要任何金鑰檔**。VM 使用**專屬服務帳號**（不用預設的 Compute 服務帳號），存取範圍設 `cloud-platform`、以 IAM 收窄：備份 bucket 可讀寫，發行版 bucket **只能讀**（VM 被入侵也無法改寫發行版） |
| 發行版 | `make release` 產生 Linux 版 Go 執行檔（內嵌前端），連同鎖定版本的 Litestream、Caddy 上傳到發行版 bucket，以版本號命名，**上傳後不覆寫**（上傳時要求物件尚不存在）。恢復時不依賴 GitHub 或 apt 是否可用 |
| 服務啟動 | `bethere.service` 設 `After=`／`Wants=network-online.target`、`Restart=on-failure`（有退避間隔、不設次數上限；沒有資料庫時 `serve` 本來就不啟動，所以重試是安全的）。每次啟動（開機、重啟服務皆同）都先執行準備腳本，以 `set -e` **失敗即停**：向 metadata server 讀取版本號與維護旗標（讀不到就不啟動）→ 版本與本機已安裝的相同就跳過下載（GCS 暫時不通時仍能啟動），不同才從發行版 bucket 下載到暫存檔，以原子方式換上後才寫入「已安裝版本」標記 → 資料庫檔不存在時執行 `litestream restore -if-replica-exists`，失敗就不啟動 → 以 `litestream replicate -exec "bethere serve"` 啟動。即使還原「成功但沒有副本」，`serve` 也會因找不到資料庫而拒絕啟動（T3c） |
| 部署與回退 | 部署＝把 VM metadata 的版本號改成新版並重啟服務；回退＝改回上一版並重啟服務。活動前 7 天起凍結部署 |
| 作業系統更新 | 平時開啟 Debian `unattended-upgrades`（只套安全更新、不自動重開機）；凍結期開始時套用一次更新並重開機驗證，之後停用自動更新到活動結束 |
| 健康檢查 | `/healthz` 執行一次唯讀查詢（走唯讀連線池，不受寫入鎖影響） |

**為什麼不用 Cloud Run**：容器的檔案系統是暫時的，平台可能隨時替換實例，部署時新舊實例還會短暫並存，SQLite 會出現兩個寫入者；要用就得改成 Cloud SQL，資料層要重新設計，成本也更高。**為什麼不用負載平衡器**：Google 的 HTTPS 負載平衡器每月約 US$18，對單一 VM 沒有必要，TLS 交給 Caddy。

### 3.1 當天的維運

> **狀態：要求草案。** 本節列出操作手冊必須滿足的要求，不是可照抄的指令。gcloud 與啟動腳本的實際行為無法在紙上確認；實作時寫成腳本，在暫存專案演練驗證後再定稿，並對腳本與演練結果另行抗辯審查。

開發者本人是新人，當天無法操作。因此：

- **指定一位備援操作者**（不是當天的總召），婚前交給他：本專案的 IAM 權限與一份紙本操作手冊。
- **權限**：`roles/compute.instanceAdmin.v1`、對 VM 服務帳號的 `roles/iam.serviceAccountUser`、`roles/iap.tunnelResourceAccessor` 與 `roles/compute.osAdminLogin`（供婚前還原測試；專案開啟 OS Login 時需要）。這些權限可以改 VM 的啟動腳本並登入主機，**等同完全接管**，因此以 IAM 條件設定**到期時間**（婚禮後數天自動失效），並列入 `access-control.md` 的威脅表。
- 操作手冊只涵蓋兩件事，**由他本人在婚前演練一次**：
  1. **退回上一個發行版**：把 metadata 的版本號改回上一版並重啟服務。
  2. **VM 或磁碟故障**（啟動門檻：服務中斷超過約 10 分鐘仍未自行恢復；放棄條件：開始後超過 30 分鐘、步驟 2～3 失敗兩次、舊 VM 刪不掉（靜態 IP 仍被佔用）或建不出新的 e2-micro，就停止並改靠離線快取與紙本）：
     1. 刪除舊 VM（保留舊磁碟，之後不再掛載），確認專案裡一台 VM 都不剩。
     2. 以預先寫好的建立指令，在 `asia-east1` 的任一可用區建立恰好一台新 VM：**新的空開機磁碟**、沿用靜態 IP、metadata 帶**已記錄的目前版本號**與 `bethere-maintenance=1`；啟動腳本自動從 GCS 還原。
     3. 檢查點：專案裡恰好 1 台 VM、使用的是新磁碟、`/healthz` 正常、HTTPS 憑證有效。不符時刪除這台 VM，回到步驟 2。
     4. 請總召用 Master 連結確認 Rundown 與 Cue 狀態是最新的，並在 V4 確認各角色的連結狀態（未發／有效／已撤銷）。此時其他角色仍被擋、`lastSeenAt` 已歸零，「最近有人在用」必定是空的，不必核對。
        - 資料略舊：總召在維護模式下直接補登（Master 可寫入）；故障前若曾以復原碼換發 Master，再換發一次。
        - 資料明顯錯誤或是空的：**不解除維護模式**，當天改靠離線快取與紙本。
     5. 把 metadata 改為 `bethere-maintenance=0` 並重啟服務（開啟與解除用同一種機制，服務啟動時會重讀 metadata）；各端短暫離線後自動恢復。以備援操作者專用的**測試角色連結**確認 API 正常回應，並請總召確認維護橫幅已消失（`/healthz` 在維護模式下也正常，不能用來判斷）。目標 15 分鐘內恢復。
- 以上步驟在實作時寫成腳本（版本號、可用區參數），並抄進紙本手冊。
- **演練**在另一個暫存專案進行（同一套腳本、另一組 bucket 與 IAM、另一個子網域），於凍結期之前完成，不碰正式專案。以**備援操作者本人的帳號與權限**執行，確認權限足夠。必做項目：
  - 「空磁碟 → 資料回來」。
  - 「空磁碟加錯誤的 bucket 名稱或拿掉服務帳號權限 → 服務不啟動」（資料庫已存在時，備份失敗不會讓服務停下，所以必須在空磁碟上測）。
  - 完整走一次「程序 2 恢復 → 解除維護 → 程序 1 回退」，全程恰好 1 台 VM、磁碟不變，且回退後確實換成上一版。
  - 重開 VM 時暫時拿掉發行版 bucket 的讀取權：本機已是同一版本，服務仍應正常啟動。
  - 一般重開機後，服務不需人工介入就自行起來。
  - 確認 `BETHERE_INIT` 不在 metadata 中（「`BETHERE_INIT` 未移除 → 不啟動」由自動測試涵蓋）；確認 `litestream replicate` 在資料庫檔尚未存在時的行為；確認新 VM 上 Caddy 能在數十秒內取得憑證。
- 婚前在正式活動中替備援操作者建立一個測試角色，供步驟 5 驗證。
- **整個 `asia-east1` 區域無法使用時不切換區域**：當天靠離線快取與紙本備援 Rundown 撐住。這是刻意接受的風險（單一可用區故障可在同區域另一個可用區重建）。
- 還原期間各端同樣靠離線快取與紙本撐住。
- 若找不到合適的備援操作者，就明確接受「當天只剩離線快取與紙本」。

### 3.2 婚前檢查

- 活動前一晚：總召在電腦上匯出並列印全部角色的紙本備援 Rundown。
- 活動前一天：備援操作者經 IAP 登入正式 VM，從 GCS 實際還原一次到暫存檔，確認備份可用且內容是最新的（同時涵蓋「資料庫閒置時是否仍有可還原的快照」），**驗完立即刪除暫存檔**；確認備份 bucket 的 soft delete 仍為關閉。
- 活動前一天：請所有工作人員用系統瀏覽器開一次連結，讓外殼與快照存到裝置上（iOS Safari 對 7 天沒互動的網站會清除本機資料與 Service Worker，太早開啟的快取可能已不在）。

### 3.3 婚後收尾

由開發者本人執行，排進行事曆：

- 婚後數天內：確認備援操作者的 IAM 權限已到期失效；以例行的還原流程**重建一次 VM**（權限期間若被留下 ssh 金鑰、cron 等後門，不會隨權限到期而消失）；刪除**所有**不再使用的磁碟，以及演練用的暫存專案。
- C3 事後匯出在 `endsAt` 後 80 天內完成。
- VM 常駐到每日排程完成刪除（`endsAt` 後第 81 天）。若要提早停機省錢，改為在停機前手動刪除 VM、所有磁碟與備份 bucket。
- 第 85 天驗收：確認活動已從資料庫刪除、備份 bucket 上只剩刪除後產生的快照；未達成就手動刪除 VM、磁碟與 bucket。

## 4. 專案結構與測試

```
cmd/bethere/        main 與子命令
internal/           domain（平移、狀態轉換）、store（SQLite）、api、auth
migrations/         編號 SQL 檔（內嵌）
web/                Vite + Preact 前端
```

| 層級 | 工具 | 重點 |
| --- | --- | --- |
| 領域邏輯 | Go `testing`，表格驅動 | 4.2 平移演算法、狀態轉換、復原、`endsAt` 與時區、Token 三種驗證結果 |
| API | `net/http/httptest` + 暫存 SQLite | 權限表每一格；版本衝突（S6）；資料庫錯誤回伺服器錯誤而非「無效」；維護模式；找不到資料庫、或 `BETHERE_INIT` 遇既有資料庫時拒絕啟動；快照鍵集合 |
| 前端 | Vitest | 時鐘校正（中位數）、倒數、本機資料清除規則（`access-control.md` 3.3） |
| 負載 | `oha`：以每秒 25 請求打快照端點，同時另開一個 `oha` 打寫入端點 | 驗證第 7 節延遲預算與讀寫並行 |
| 實機 | 手動檢查清單（見第 5 節） | 婚前演練時逐項走一遍 |

## 5. 選型後必須先做的實機驗證

在寫主要功能前先做最小原型驗證。每項都在 iOS Safari、Android Chrome、**LINE 內建瀏覽器**、**iOS 主畫面模式**中測試：

| # | 驗證 | 不成立時的退路 |
| --- | --- | --- |
| R1 | `#t=` 在「LINE → 以 Safari 開啟」、加入書籤、加到主畫面時是否保留 | 提供 manifest 但不設 `start_url`；或在頁面上提供「複製本頁連結」引導 |
| R2 | 外殼能否離線開啟（LINE 預設不能，見 T22） | LINE 使用者改用系統瀏覽器或主畫面；其餘依賴紙本 |
| R3 | Wake Lock（新秘 V2a 是 P0 核心） | 提示調整手機自動鎖定；重開頁面後需點擊才能取得，V2a 加常亮提示層（5.1） |
| R4 | 台灣（含行動網路）到 `asia-east1` 的輪詢往返在 600 ms 內 | 調整輪詢間隔 |
| R5 | 列印與另存 PDF | 前一晚由總召在電腦上統一列印（3.2） |

### 5.1 實機驗證結果

以 `ops/gcp/` 部署到正式專案的裝置驗證頁測試（`web/src/DeviceCheck.tsx`），各環境的回報記在伺服器日誌。

| 日期 | 裝置與環境 | R1 Token | R2 離線開啟 | R3 常亮 | R4 往返（中位數／p95） | R5 列印 |
| --- | --- | --- | --- | --- | --- | --- |
| 2026-10-21 | iPhone，Safari 27.0.1（UA 回報 iOS 18_7），主畫面模式 | ✔ 加到主畫面後保留 | ✔ 飛航模式下關掉 App 重開仍可開啟 | ✔ 可啟用並維持；切到別的 App 再回來仍保持。**關掉 App 重開後需使用者點擊才能再啟用**，頁面載入時自動申請回 `NotAllowedError` | ✔ 19～44／65～153 ms（樣本 8） | ✔ 可叫出列印畫面 |

| 2026-10-21 | iPhone，LINE 設定為「用外部瀏覽器開啟連結」→ Safari 分頁 | ✔ 保留 | Service Worker 已控制頁面（未測離線重開） | 同上，重開後需點擊 | 24～77 ms（樣本 6） | — |

| 2026-10-21 | iPhone，LINE 26.16.0 內建瀏覽器（預設設定，User-Agent 含 `Line/26.16.0`） | ✔ 保留 | ✘ **頁面不受 Service Worker 控制，飛航模式下打不開**（符合 T22 的預設） | 未測 | 15／26 ms（樣本 12） | 未測 |
| 2026-10-21 | iPhone，LINE 內建瀏覽器 →「以 Safari 開啟」 | ✔ 保留 | Service Worker 已控制頁面 | 重開後需點擊（同上） | 16／939 ms（樣本 7，一次離群值） | — |

結論（iOS）：R1 在所有測過的路徑都成立；T22 成立，LINE 內建瀏覽器可由 User-Agent 的 `Line/` 辨識，且無法離線開啟，必須引導改用 Safari。

尚未測試：Android（Chrome、LINE、主畫面）；LINE 內建瀏覽器中的常亮與列印（依 T22 會引導離開 LINE，不影響設計）。注意：LINE 設定為外部瀏覽器時，User-Agent 與一般 Safari 相同，不含 `Line/`。

## 6. 決定紀錄

| # | 決定 | 狀態 |
| --- | --- | --- |
| T-A | 後端 Go、前端 TypeScript + Preact | ✎ 2026-10-20 |
| T-B | GCE 台灣（asia-east1）單一 e2-micro VM + Caddy + SQLite + Litestream→GCS，自訂網域與靜態 IP（S7）；不準備跨區域備援（2026-10-20 由 Fly.io 改為 GCE） | ✎ 2026-10-20 |
| T-C | 備援 Rundown 用列印專用網頁 | ✎ 2026-10-20 |
| T-D | 平移預覽由伺服器試算 | ✎ 2026-10-20 |
| T-E | 當天維運由指定的備援操作者負責 | ✎ 2026-10-20 |
| T-F | 資料庫於 `endsAt` 後 80 天刪除，伺服器端所有副本 90 天內消失（修訂 C5） | ✎ 2026-10-20 |
