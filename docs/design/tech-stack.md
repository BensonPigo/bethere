# 技術選型 (Tech Stack) — P0 範圍

> 依據：`data-model.md`、`sync-and-offline.md`、`access-control.md`、`screen-design.md`。本文件決定部署與託管，以及各設計文件留給技術選型的項目。
> 前提：一人開發；第一場使用為 2027/06/06，**開發者本人就是當天的新人，無法操作任何維運**；單場同時在線 ≤ 50 人（NFR-1），約每秒 25 個輪詢請求。

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

| 項目 | 選擇 | 理由 |
| --- | --- | --- |
| 語言 | Go（最新穩定版） | 單一執行檔、記憶體小、標準庫足夠；設計把邏輯集中在伺服器，前後端幾乎無共用程式碼 |
| HTTP | 標準庫 `net/http`（含方法與路徑樣式路由），不用框架 | API 只有十幾個端點 |
| 資料庫 | SQLite，WAL 模式；驅動 `modernc.org/sqlite`（純 Go，免 cgo） | 單場資料量極小；`data-model.md` 4.2 的「單一交易」對應一筆寫入交易 |
| 連線 | 開兩個 `*sql.DB`。**寫入專用**：`MaxOpenConns = 1`、`_txlock=immediate`，DSN 帶 `journal_mode(WAL)`、`busy_timeout(5000)`、`secure_delete(ON)`；啟動時**先開寫入連線並完成遷移**，再開**唯讀連線池**（DSN 帶 `busy_timeout(5000)`）。PRAGMA 一律寫在 DSN，每條連線都會套用 | WAL 下讀不擋寫；讀寫分開後，輪詢不會排在慢寫入（清理、等待子命令的鎖）後面 |
| 逾時與錯誤 | 輪詢的資料庫查詢帶 3 秒 context 逾時；資料庫錯誤（含逾時、`SQLITE_BUSY`）一律回伺服器錯誤，絕不回「無效」 | `access-control.md` 3.3：只有確實查無有效 Token 才回「無效」 |
| 不自動建庫 | `bethere serve` 找不到資料庫檔時**拒絕啟動**。只有第一次部署時設定 `BETHERE_INIT=1` 才建立新資料庫；`BETHERE_INIT=1` 而資料庫檔已存在時同樣**拒絕啟動**，所以忘記移除會在下一次開機就暴露。**0 位元組或沒有結構描述（`user_version` 為 0）的檔案同樣拒絕啟動**，先以唯讀方式檢查、不改動檔案，錯誤訊息明示「還原可能失敗，不要設 `BETHERE_INIT`」；啟動時實際寫入一次，寫不進去也拒絕啟動；路徑以跳脫過的 `file:` URI 開啟並帶 `mode=rw`／`ro`，SQLite 本身不會建新檔；`BETHERE_INIT=1` 以獨佔方式建檔，設定失敗就刪除 | 避免還原失敗時悄悄起一個空庫，讓所有裝置收到「無效」而清掉離線快取 |
| 維護模式 | VM 的 metadata `bethere-maintenance=1`，或資料庫是從備份還原的（準備腳本在還原**之前**寫下 `/var/lib/bethere/maintenance.forced` 標記，由 `ops/gcp/maintenance.sh off` 刪除；`ops/gcp/init-db.sh` 建立空資料庫時也刪除）時，服務以維護模式啟動。準備腳本每次啟動都讀 metadata：鍵不存在視為 0，讀不到或值不是剛好 0、1 就拒絕啟動；要以維護模式啟動時，已安裝的版本必須在 `bethere features` 列出 `maintenance`，否則拒絕啟動（不支援的舊版不會默默開放）。結果寫入 `/run/bethere/maintenance.env`；還原之前另建婚顧公司成員連結的待清除標記 `member-reset.pending`（`api.md` 10.2）；服務設定預設 `BETHERE_MAINTENANCE=unread`，`serve` 只接受 0、1 或未設定，所以沒讀到旗標就不啟動。維護模式**只限制 `/api/`**（`api.md` 第 8 節）：Master Token 的請求正常處理（建立活動除外），復原碼只放行「換發 Master」，其他一律回 503 `maintenance`（客戶端視為離線、保留快照）。`/healthz` 與靜態檔照常回應。Master 的快照帶 `maintenance`，主控台頂端顯示醒目橫幅「維護中，其他角色無法連線」。維護中不執行每日刪除 | 還原後先讓總召核對，再對全場開放（`operations/runbook.md` 第 2 節）；健康檢查不能失敗，總召在沒快取過外殼的裝置上也要能開頁面 |
| SQL 與遷移 | `database/sql` + 手寫參數化 SQL；遷移為內嵌編號 SQL 檔，以 `PRAGMA user_version` 記錄；**遷移只做向後相容的新增**（加表、加可為空欄位、加索引；理由與唯一例外見 ADR-0019）；已發行的遷移檔不再修改，結構變更一律新增檔案；程式遇到比自己新的 `user_version` 照常啟動 | 表少，不需 ORM；可退回上一個發行版 |
| 平移預覽 | 伺服器提供「試算」端點（同一套演算法、不寫入），預覽面板每次勾選變更都呼叫它 | 平移邏輯只有 Go 一份；總召離線時本來就不能操作（`sync-and-offline.md` 第 6 節） |
| Token 與復原碼 | `crypto/rand` 產生；Token 16 位元組以 base64url 編碼；復原碼 16 位元組以 Crockford Base32 分組；兩者以 SHA-256 雜湊儲存並以雜湊查詢 | 熵已 128 位元，不需要慢雜湊 |
| `lastSeenAt` | 只存在記憶體（`map[tokenHash]time.Time` + mutex），不寫資料庫；**只在 Token 驗證成功、且未被維護模式擋下之後**才更新；角色與連結管理畫面直接讀取，重啟後歸零 | 只用於角色與連結管理畫面顯示「最近有人在用」；少一個寫入來源；隨機 Token 無法讓 map 無限成長 |
| QR Code | 伺服器在產生 Token 的同一個回應中附上 **PNG data URI**；函式庫鎖定版本 `github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e`（QR 規格穩定），容錯等級 Medium、256 像素；內容為畫面上同一條文字連結（`api.md` 第 3 節「QR Code」） | 明文 Token 只在產生當下存在；前端不需額外套件 |
| CSV | 匯入與事後紀錄匯出都在伺服器以 `encoding/csv` 處理；匯入先回傳驗證預覽；匯出加 UTF-8 BOM | 驗證規則只寫一份；Excel 開中文不亂碼 |
| 資料刪除 | 每日排程（服務啟動時一次，之後每 24 小時）刪除 `endsAt` 已超過 **80 天**的活動與已過期的邀請碼（ADR-0036），時鐘落在 2000～2199 年以外時不刪，`secure_delete` 讓內容被覆寫，checkpoint 交給 Litestream 管理，應用程式不自行執行（Litestream 持有讀取交易，外部的 `wal_checkpoint(TRUNCATE)` 可能一直回 busy）；演練時確認刪除後的資料已不在資料庫檔與 WAL 檔中。伺服器端各副本：Litestream 快照間隔 24h、保留 72h（版本 0.5.17，設定檔由 VM 的準備腳本產生，見 `ops/gcp/vm-startup.sh`）；**不排程磁碟快照**、不建立含資料的自訂映像；故障後留下的舊磁碟依 `operations/runbook.md` 第 4 節刪除。備份 bucket **關閉 soft delete（保留期 0）、不開物件版本控制**，否則已刪除的物件還會多留 7 天；**不設**生命週期規則，以免資料庫閒置時刪光唯一的備份；改由 `operations/runbook.md` 第 4 節在第 85 天驗收，未達成就手動刪除。合計**伺服器端**所有副本在 `endsAt` 後 **90 天內**消失（80 + 1 + 4 ≤ 90）。前提是主機常駐到刪除完成，否則依 `operations/runbook.md` 第 4 節手動銷毀 | 只刪資料庫不夠，備份會再多留數天；以「伺服器端所有副本 90 天內消失」為準才符合刪除承諾（NFR-5）（ADR-0011）。裝置上的 localStorage 依 `sync-and-offline.md` 第 6 節在再次開啟時清除，紙本不在系統控制範圍。代價是事後紀錄匯出必須在 80 天內完成 |
| 伺服器端指令 | 同一執行檔的子命令：`serve`、`create-event`、`add-role`、`import-rundown`、`create-invite`（邀請碼，ADR-0036）、`reset-recovery-code`（復原碼外流時重設，US-O-4）、`issue-admin-link`（系統後台的連結，US-O-9）、`find-member`、`add-member`、`remove-member`、`issue-login-link`（婚顧公司成員的救援，US-O-12）、`version`、`features`；以 `gcloud compute ssh --tunnel-through-iap` 登入 VM 後執行 | 建立活動與邀請碼都由開發者在伺服器上產生；輸出只到終端機 |
| 時間 | 伺服器一律 UTC；時區資料以 `time/tzdata` 內嵌；活動時區只接受內嵌資料逐字收錄的名稱清單（`domain.SupportedTimeZone`，目前只有 `Asia/Taipei`）；時鐘以介面注入，測試可固定「現在」 | `endsAt` 與「活動僅限當天開始」都依活動時區計算 |
| 日誌 | `log/slog` 輸出到 stdout；不記錄請求標頭與本文 | Token 在標頭、復原碼在本文 |
| 安全標頭 | Go 中介層統一加 CSP：`default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'`（無 inline script）、`Referrer-Policy: no-referrer`、`X-Robots-Tag: noindex`；API 加 `Cache-Control: no-store` | `access-control.md` 3.2、3.3；防點擊劫持 |

### 2.2 前端

| 項目 | 選擇 | 理由 |
| --- | --- | --- |
| 框架 | TypeScript + Preact，Vite 打包；**不使用動態 import**，輸出單一 JS bundle | API 與 React 相同、執行期約 4 KB；單一 bundle 讓新版 Service Worker 接手時，舊頁面不會載入已被清掉的分塊 |
| 路由 | **路徑**決定入口，不用路由函式庫：`/`（依快照中角色的畫面設定顯示主控台、快速瞄畫面、免手持畫面或平安儀表板（ADR-0025））、`/print`（備援 Rundown 列印頁）、`/recover`（復原碼入口，不需 Token）、`/new`（以邀請碼建立活動，不需 Token）、`/admin`（系統後台，以管理員連結進入，ADR-0038）。Master 的主控台、Rundown 編輯器、角色與連結管理以畫面內狀態切換。頁面間跳轉一律保留 `#t=` | 網址片段被 `#t=` 佔用，只能用路徑；Go 對這五個路徑都回傳 `index.html` |
| 型別 | 手寫 `web/src/api.ts`；後端以測試比對快照 JSON 的鍵集合，防止前後端欄位漂移 | 快照僅十幾個欄位，不值得引入產生器 |
| 離線外殼 | `vite-plugin-pwa`（Workbox `generateSW`）預先快取打包產物（含四個入口）；`/api/` 一律不攔截；`skipWaiting` + `clientsClaim`；註冊腳本以外部檔案載入（不用 inline）；不產生 manifest | 對應 `sync-and-offline.md` 第 6 節；活動前 7 天凍結部署，當天不會出現前後端版本不一致 |
| Web App manifest | P0 不提供；依 Token 保留的實機結果（第 5 節）再決定 | 避免 `start_url` 蓋掉帶 Token 的網址 |
| 本機快照 | `localStorage`，鍵為 `snap:` + SHA-256（`"bethere-local:"` + Token）（`crypto.subtle`，HTTPS 下必定可用），不等於伺服器的 Token 雜湊；不在本機儲存明文 Token | 共用裝置上列舉 localStorage 不會取得 Token；伺服器的 Token 雜湊不應出現在伺服器以外 |
| 備援 Rundown | 列印專用網頁 `/print`（`@media print`），由瀏覽器列印或另存 PDF；資料來自目前快照（離線時用本機快照）。Master 的快照帶**完整 CueRole**（各角色與 `roleNote`），Master 版才能從快照產生 | 不需 PDF 函式庫；Safari／Chrome 離線也能匯出 |
| LINE 內建瀏覽器 | 視為**受限環境**：預設 Service Worker 不可用（iOS 版 LINE 為 WKWebView）、列印可能無效。連線時一切照常可用，不阻擋、不跳轉（`product/vision.md` V2）；偵測到 LINE 時只顯示非強制的一行小字，說明斷網時可能打不開、可改用 Safari／Chrome；列印時提示改用系統瀏覽器 | 離線外殼與列印都依賴系統瀏覽器 |

## 3. 部署與託管

| 項目 | 選擇 |
| --- | --- |
| 平台 | Google Cloud Compute Engine，區域 `asia-east1`（台灣），台灣往返約 5～15 ms |
| 規格 | **恰好一台** `e2-micro`（2 個共享 vCPU、1 GB 記憶體）、10 GB `pd-balanced` 開機磁碟（`auto-delete=false`，刪 VM 時保留磁碟）、Debian 12、1 GB swap 檔、不安裝 Ops Agent；`onHostMaintenance=MIGRATE`、`automaticRestart=true`。預估每月約 US$11～12（VM 約 7、對外 IPv4 約 3.6、磁碟約 1；實際以官方計價為準）。`e2-micro` 的免費額度只限美國區域，台灣不適用 |
| 網路 | 保留一個**區域性靜態 IP**，重建 VM 時沿用，不必改 DNS。使用**自訂 VPC**（不用內建 `default-allow-ssh`／`rdp` 規則的預設網路）：對外只開 80、443；22 只開放給 IAP 的來源網段 `35.235.240.0/20`。Go 服務只綁 `127.0.0.1`，對外一律經 Caddy |
| 網域與 TLS | **發出任何連結前**就設定自訂網域，A 紀錄指向靜態 IP；Caddy 以 Let's Encrypt 自動簽發與更新憑證（Let's Encrypt 失敗時改用 ZeroSSL），反向代理到本機的 Go 服務。重建 VM 會重新簽發；同一組網域每週最多 5 張重複憑證，所以演練一律用另一個子網域 |
| 備份 | Litestream 持續複製到同區域的 GCS bucket（版本與保留期見 2.1「資料刪除」）；**不需要任何金鑰檔**。VM 使用**專屬服務帳號**（不用預設的 Compute 服務帳號），存取範圍設 `cloud-platform`、以 IAM 收窄：備份 bucket 可讀寫，發行版 bucket **只能讀**（VM 被入侵也無法改寫發行版） |
| 發行版 | `make release` 產生 Linux 版 Go 執行檔（內嵌前端），連同鎖定版本的 Litestream、Caddy 上傳到發行版 bucket，以版本號命名，**上傳後不覆寫**（上傳時要求物件尚不存在）。恢復時不依賴 GitHub 或 apt 是否可用 |
| 服務啟動 | `bethere.service` 設 `After=`／`Wants=network-online.target`、`Restart=on-failure`（有退避間隔、不設次數上限；沒有資料庫時 `serve` 本來就不啟動，所以重試是安全的）。每次啟動（開機、重啟服務皆同）都先執行準備腳本，以 `set -e` **失敗即停**：向 metadata server 讀取版本號與維護旗標（讀不到就不啟動）→ 版本與本機已安裝的相同就跳過下載（GCS 暫時不通時仍能啟動），不同才從發行版 bucket 下載到暫存檔，以原子方式換上後才寫入「已安裝版本」標記 → 資料庫檔不存在時先刪除殘留的 `-wal`／`-shm`，再執行 `litestream restore -if-replica-exists`，失敗就不啟動 → 讀取專案層級 metadata 的客服信箱（US-O-8.2；讀不到或格式不對就不顯示，不擋啟動）→ 以 `litestream replicate -exec "bethere serve"` 啟動。即使還原「成功但沒有副本」，`serve` 也會因找不到資料庫而拒絕啟動（2.1「不自動建庫」） |
| 部署與回退 | 部署＝把 VM metadata 的版本號改成新版並重啟服務；回退＝改回上一版並重啟服務。活動前 7 天起凍結部署 |
| 作業系統更新 | 平時開啟 Debian `unattended-upgrades`（只套安全更新、不自動重開機）；凍結期開始時套用一次更新並重開機驗證，之後停用自動更新到活動結束 |
| 健康檢查 | `/healthz` 執行一次唯讀查詢（走唯讀連線池，不受寫入鎖影響），3 秒內沒成功就回 503，外部監控因此看得到資料庫讀不到；寫不進去（例如磁碟滿）不在這個檢查內 |
| 監控與告警 | Cloud Monitoring Uptime Check 從外部探測 `/healthz`；另一個探測看備份新鮮度端點（本機變動超過 5 分鐘未複製即失敗，尚待確認 Litestream 能否提供）；告警平時送開發者 email，凍結期改送備援操作者的手機推播與簡訊。開發期主機平時關機，告警以 `ops/gcp/power.sh` 跟著開關。不在 VM 安裝代理程式（ADR-0024） |
| 寄信 | 成員的登入信（ADR-0040）以一個專用 Gmail 的應用程式密碼經 `smtp.gmail.com:587`（STARTTLS）寄出；GCE 封鎖的是 25 埠，587 不受影響。寄件地址放在專案層級的 metadata `bethere-mail-from`，密碼放在 Secret Manager 的 `bethere-mail-password`（有專案檢視權限的人讀得到 metadata、讀不到它；專案擁有者與能登入主機的人讀得到，`access-control.md` 第 1 節「寄信帳號外流」）；兩者都由 `ops/gcp/mail.sh` 設定（密碼從標準輸入讀，不進指令列與歷史紀錄），重建 VM 也沿用。準備腳本每次啟動讀取兩者與 VM 的 `bethere-domain`，寫入只有 root 能讀的 `/run/bethere/mail.env`（`BETHERE_MAIL=smtp` 等，`api.md` 10.2）；同客服信箱，寫入前過濾：寄件地址只接受客服信箱的同一個格式，應用程式密碼去掉空白後只接受 16 個小寫英文字母，網域只接受主機名稱的字元，所以值裡的換行無法多加一個變數。服務設定裡 `mail.env` 排在 `maintenance.env` 之前，就算多出變數也蓋不掉維護旗標。任何一項讀不到或不合格就不寫，服務照常啟動、登入顯示寄不出去（US-CO-1.12），不擋婚禮。準備腳本只寫 `smtp`，絕不寫 `log`。測試服務不設定寄信。只需 email 的版本（`bethere features` 沒有 `mail-login`，包括 M4 以前的所有版本）不能部署，兩道檢查： ① 提早攔截：`release.sh` 上傳發行版時，一併上傳 `go run ./cmd/bethere features` 從同一份程式碼產生的 `features.txt`（發行版是 Linux 執行檔，在開發機上跑不了）。共用的 `check_release`（`ops/gcp/env.sh`，`deploy.sh`、`rebuild.sh`、`create-vm.sh` 在改任何 metadata 或刪除任何東西之前都會呼叫）只檢查要部署的那個版本，沒有這個檔或沒有 `mail-login` 就拒絕；上一版不檢查，只在 `deploy.sh` 不帶參數時標出是否支援。 ② 真正可信的檢查：準備腳本安裝執行檔後（正式與測試服務共用這一步）以實際裝上的 `bethere features` 再查一次，執行失敗或沒有 `mail-login` 都拒絕啟動，擋住繞過腳本、竄改 `features.txt` 或重開機的情況。 兩道都寫在這個 repo 的腳本裡，只在用 M5 以後的程式碼部署時有效；沒有放行開關，真的必須退回時由開發者暫時改本機的腳本（`operations/runbook.md` 第 1 節） |

**為什麼不用 Cloud Run**：容器的檔案系統是暫時的，平台可能隨時替換實例，部署時新舊實例還會短暫並存，SQLite 會出現兩個寫入者；要用就得改成 Cloud SQL，資料層要重新設計，成本也更高。**為什麼不用負載平衡器**：Google 的 HTTPS 負載平衡器每月約 US$18，對單一 VM 沒有必要，TLS 交給 Caddy。

維運程序（當天的維運、婚前檢查、婚後收尾）見 `operations/runbook.md`。

## 4. 專案結構與測試

```
cmd/bethere/        main 與子命令
internal/           domain（平移、狀態轉換）、store（SQLite，遷移為 store/migrations/ 的內嵌編號 SQL 檔）、api、auth
web/                Vite + Preact 前端
```

| 層級 | 工具 | 重點 |
| --- | --- | --- |
| 領域邏輯 | Go `testing`，表格驅動 | 4.2 平移演算法、狀態轉換、復原、`endsAt` 與時區、Token 三種驗證結果 |
| API | `net/http/httptest` + 暫存 SQLite | 權限表每一格；版本衝突（`sync-and-offline.md` 第 3 節）；資料庫錯誤回伺服器錯誤而非「無效」；維護模式；找不到資料庫、或 `BETHERE_INIT` 遇既有資料庫時拒絕啟動；快照鍵集合 |
| 前端 | Vitest | 時鐘校正（中位數）、倒數、本機資料清除規則（`access-control.md` 3.3） |
| 負載 | `oha`：以每秒 25 請求打快照端點，同時另開一個 `oha` 打寫入端點 | 驗證 `sync-and-offline.md` 第 7 節的延遲預算與讀寫並行 |
| 實機 | 手動檢查清單（見第 5 節） | 婚前演練時逐項走一遍 |

## 5. 選型後必須先做的實機驗證

在寫主要功能前先做最小原型驗證。每項都在 iOS Safari、Android Chrome、**LINE 內建瀏覽器**、**iOS 主畫面模式**中測試：

| 驗證 | 不成立時的退路 |
| --- | --- |
| `#t=` 在「LINE → 以 Safari 開啟」、加入書籤、加到主畫面時是否保留 | 提供 manifest 但不設 `start_url`；或在頁面上提供「複製本頁連結」引導 |
| 外殼能否離線開啟（LINE 預設不能，見 2.2「LINE 內建瀏覽器」） | LINE 使用者改用系統瀏覽器或主畫面；其餘依賴紙本 |
| Wake Lock（免手持畫面是 P0 核心） | 提示調整手機自動鎖定；重開頁面後需點擊才能取得，免手持畫面加常亮提示層（5.1） |
| 台灣（含行動網路）到 `asia-east1` 的輪詢往返在 600 ms 內 | 調整輪詢間隔 |
| 列印與另存 PDF | 前一晚由總召在電腦上統一列印（`operations/runbook.md` 第 3 節） |

### 5.1 實機驗證結果

| 裝置與環境 | Token 保留 | 離線開啟 | 常亮 | 往返（中位數／p95） | 列印 |
| --- | --- | --- | --- | --- | --- |
| iPhone，Safari 27.0.1（UA 回報 iOS 18_7），主畫面模式 | ✔ 加到主畫面後保留 | ✔ 飛航模式下關掉 App 重開仍可開啟 | ✔ 可啟用並維持；切到別的 App 再回來仍保持。**關掉 App 重開後需使用者點擊才能再啟用**，頁面載入時自動申請回 `NotAllowedError` | ✔ 19～44／65～153 ms | ✔ 可叫出列印畫面 |
| iPhone，LINE 設定為「用外部瀏覽器開啟連結」→ Safari 分頁 | ✔ 保留 | Service Worker 已控制頁面（未測離線重開） | 同上，重開後需點擊 | 24～77 ms | — |
| iPhone，LINE 26.16.0 內建瀏覽器（預設設定，User-Agent 含 `Line/26.16.0`） | ✔ 保留 | ✘ **頁面不受 Service Worker 控制，飛航模式下打不開**（符合 2.2「LINE 內建瀏覽器」的預設） | 未測 | 15／26 ms | 未測 |
| iPhone，LINE 內建瀏覽器 →「以 Safari 開啟」 | ✔ 保留 | Service Worker 已控制頁面 | 重開後需點擊（同上） | 16／939 ms | — |

結論（iOS）：Token 在所有測過的路徑都保留；2.2「LINE 內建瀏覽器」的假設成立，LINE 內建瀏覽器可由 User-Agent 的 `Line/` 辨識，且無法離線開啟；依 `product/vision.md` V2 只以非強制的小字提示可改用 Safari，不阻擋使用。

尚未測試：Android（Chrome、LINE、主畫面）；LINE 內建瀏覽器中的常亮與列印（列印依 2.2「LINE 內建瀏覽器」提示改用系統瀏覽器；常亮待 Android 實機驗證一併測，RISK-1）。注意：LINE 設定為外部瀏覽器時，User-Agent 與一般 Safari 相同，不含 `Line/`。
