# BeThere 文件索引

先讀 `product/vision.md`，再讀 `requirements/`、`design/`。想看全貌從 `architecture/overview.md` 開始；想知道某個決定為什麼這樣選，看 `decisions.md`；看到不懂的代號，查 `glossary.md`。

所有文件只寫現況；修訂經過只在 git 紀錄。

| 文件 | 內容 |
| --- | --- |
| [product/vision.md](product/vision.md) | 產品願景與原則（V1～V5）；所有取捨的最上層依據 |
| [requirements/scope.md](requirements/scope.md) | 功能清單 × 優先層級 × 里程碑；限制；非功能需求（NFR）；原始需求對照 |
| [requirements/roles-and-scenarios.md](requirements/roles-and-scenarios.md) | 各角色的現場使用情境與原始使用者故事 |
| [requirements/stories.md](requirements/stories.md) | 所有故事與驗收條件（US-x-n） |
| [design/data-model.md](design/data-model.md) | 實體、狀態轉換、時間平移演算法、領域服務 |
| [design/sync-and-offline.md](design/sync-and-offline.md) | 輪詢與快照、版本衝突、時鐘、連線狀態、離線快取、備援 Rundown |
| [design/access-control.md](design/access-control.md) | Token、復原碼、角色權限、撤銷與重發 |
| [design/screen-design.md](design/screen-design.md) | 各畫面的資訊架構 |
| [design/api.md](design/api.md) | API 規格：驗證結果、端點、權限、各角色快照、錯誤對照 |
| [design/tech-stack.md](design/tech-stack.md) | 技術選型、部署與託管、專案結構與測試、實機驗證結果 |
| [architecture/overview.md](architecture/overview.md) | 一頁全貌：系統情境、容器、部署視圖、橫切關注 |
| [operations/runbook.md](operations/runbook.md) | 部署與回退、當天的維運與故障重建、婚前檢查、婚後收尾 |
| [process/development-process.md](process/development-process.md) | 開發流程：角色、里程碑、故事做法、三個風險邊界的審查、Git 與 CI |
| [process/e2e-test-routes.md](process/e2e-test-routes.md) | 端對端測試路線 |
| [process/readbody-split-plan.md](process/readbody-split-plan.md) | 計畫 RB1：把 `internal/api/body.go` 的請求解析拆到 CRAP 門檻以下，行為不變 |
| [decisions.md](decisions.md) | 現行決定（ADR-NNNN）與理由 |
| [glossary.md](glossary.md) | 術語與代號規則 |

## 狀態

- **已完成**：產品本身：一場婚禮從頭到尾需要的功能（建立活動（含以邀請碼自己建立）、婚前準備、傳連結、當天推進與平移、各角色看倒數、離線唯讀、紙本備援），以及主持、領班、機動組、迎賓、禮金的工作；另有持續備份與開機還原，以及開發者的系統後台（發邀請碼、看所有活動、建立婚顧公司）；婚顧公司的成員以 email 登入（測試版）一起管理公司的活動（M4，ADR-0039）。
- **進行中**：婚顧公司成員的正式登入：收登入信登入（M5，ADR-0040），正在寫故事。
- **還沒做**：維運的其餘項目（備份新鮮度探測、備援操作者本人演練）與 M3b，之後再決定（ADR-0033）；清單見 `requirements/scope.md`。
- **決定日**：2027-04-15；之前做唯一一次完整彩排，日期與參加者未定；沒通過就改用紙本 + LINE 群組。
- **已知問題**：無。

### 風險

| 代號 | 風險 | 處理 |
| --- | --- | --- |
| RISK-1 | Android（Chrome、LINE、主畫面）實機驗證未做；`#t=` 是否保留若不成立，要重審 ADR-0002 | M3b |
| RISK-2 | 延遲預算沒有緩衝（`design/sync-and-offline.md` 第 7 節） | M3b 負載測試；必要時把輪詢縮到 1～1.5 秒 |
| RISK-3 | 單一 VM 單點故障；整個 `asia-east1` 故障時不切換區域 | 刻意接受；離線快取、紙本、15 分鐘恢復 |
| RISK-4 | 故障重建已在暫存專案以 PO 的帳號演練通過，但備援操作者本人的帳號與權限還沒演練 | M3a；人選決定後由本人在暫存專案再演練（ADR-0030） |
| RISK-5 | 備援操作者與當天總召未定；未定期間復原碼只有開發者保管（ADR-0018） | 決定日前確定；備援操作者須符合 `operations/runbook.md` 第 2 節的條件（不在婚禮工作組、帶已登入 gcloud 的筆電與熱點）；找不到就接受當天只剩離線快取與紙本（ADR-0013） |
| RISK-6 | 正式網域尚未購買（目前用 `sslip.io`） | 發出任何真實連結前處理 |
| RISK-7 | iOS 會清除 7 天未互動網站的本機資料與 Service Worker | 活動前一天請所有人再開一次連結（`operations/runbook.md` 第 3 節） |
| RISK-8 | `GET /api/ping` 揭露版本號；Cue id 遞增 | 接受 |
| RISK-10 | 角色類型的權限（`design/access-control.md` 第 4 節）由 store 的呼叫端檢查 | 接受 |
| RISK-11 | 復原碼可在 Draft 階段把日期延到 2199 年以延長保留 | 接受；屬「復原碼外流＝完全接管」 |
| RISK-12 | `secure_delete` 的檔案層抹除要等 checkpoint | 演練時驗證 |
| RISK-13 | 非 Master 的 Cue 寫入不檢查版本 | 接受 |
| RISK-14 | 舊版回退後，新格式的最後一步 Cue 操作無法復原（ADR-0015） | 接受 |
| RISK-15 | iOS Safari 在螢幕鎖定或切到背景時暫停輪詢 | 回到前景時立即輪詢；新秘有螢幕常亮，影響較小 |
| RISK-16 | 場地網路品質（地下室、訊號弱的宴會廳） | 婚前到場地實測；必要時準備行動熱點 |
| RISK-17 | 系統以外的單點：GCP 帳單停權、網域到期、開發者的 Google 帳號被鎖；任一項發生，備援操作者都無權處理 | 決定日前依 `operations/runbook.md` 第 3 節處理：信用卡效期、預算告警、網域續約與鎖定、第二位 Owner（開發者本人的另一個帳號） |
| RISK-18 | LINE 內建瀏覽器（預設設定）不受 Service Worker 控制，斷網時打不開，而廠商多半從 LINE 點開連結（`design/tech-stack.md` 5.1） | 活動前一天請所有工作人員用系統瀏覽器開一次連結（`operations/runbook.md` 第 3 節）；LINE 中顯示改用 Safari／Chrome 的小字；仍打不開的人靠紙本與總召口頭通知 |
| RISK-19 | 復原碼紙本遺失、撿到的人又已換掉 Master 連結時，Master 連結與復原碼都不在手上，`reset-recovery-code` 無法執行，活動收不回來（`design/access-control.md` 第 1 節） | 接受；不另開不需憑證的入口。當天改用離線快取、紙本與 LINE 群組（ADR-0013） |
| RISK-20 | 寄信登入（M5）上線前，正式主機的成員登入只需 email：知道 email 的人就能進入那家公司、指揮它所有的活動，也能把別人的 email 加進去佔用。上線後，信箱被盜、登入信被轉寄或寄信用的 Gmail 外流就等同成員；退回上線前的版本會重新開放只需 email 的登入（`design/access-control.md` 第 1 節） | 不屬於任何公司的活動（包括第一場）不受影響；只需 email 的版本產生的登入連結與主控台連結，上線後一律不接受，那段期間的成員、活動與角色連結依 `operations/runbook.md` 第 1 節核對（US-CO-1.13）；佔用由開發者以指令救援（US-O-12）；登入信 15 分鐘內有效、只能用一次；部署腳本與主機啟動時都拒絕那之前的版本；婚後重產 Gmail 應用程式密碼 |
| RISK-21 | 登入信靠一個 Gmail 寄：帳號被停用、應用程式密碼失效、達到每天的上限，或信被當成垃圾信時，成員無法在新裝置登入；知道成員 email 的人能讓那些人當天收不到信，知道約 10 位的能讓那家公司、約 40 位的能讓全系統當天收不到（`design/access-control.md`「濫用登入頁」）；寄信失敗只記在服務日誌，要等成員反映才知道 | 已登入的裝置 30 天內不受影響；開發者以 `issue-login-link` 救援（US-O-12.8）；買網域後改用正式的寄件信箱 |
