# BeThere 文件索引

建議閱讀順序：先 `product/vision.md`，再 `requirements/`、`design/`。想先看全貌，從 `architecture/overview.md` 開始；想知道某個決定為什麼這樣選，看 `adr/`；看到不懂的代號，查 `glossary.md`。

文件結構與代號規則見 ADR-0017：每件事只有一個正本，其他地方只引用。

## 術語與代號

| 文件 | 內容 |
| --- | --- |
| [glossary.md](glossary.md) | 術語、代號規則、舊代號對照表 |

## product/ 產品

| 文件 | 內容 |
| --- | --- |
| [vision.md](product/vision.md) | **產品願景與原則**：要解決的問題、兩個核心價值、V1～V5 原則；所有取捨的最上層依據 |

## requirements/ 需求

| 文件 | 內容 |
| --- | --- |
| [scope.md](requirements/scope.md) | 功能清單 × 優先層級（P0／P1／P2）× 里程碑；限制；非功能需求（NFR） |
| [roles-and-scenarios.md](requirements/roles-and-scenarios.md) | 各角色的現場使用情境與原始使用者故事；P0 以外的故事作為待辦 |
| [stories/m1.md](requirements/stories/m1.md) | M1 最小閉環的故事與驗收條件（已驗收） |
| [stories/m2a.md](requirements/stories/m2a.md) | M2a 平移與第一次彩排的故事與驗收條件（草稿） |

## design/ 設計

規則、欄位與數字的正本；狀態圖與循序圖放在它說明的規則旁。

| 文件 | 內容 |
| --- | --- |
| [data-model.md](design/data-model.md) | 實體、狀態轉換、時間平移演算法、領域服務 |
| [sync-and-offline.md](design/sync-and-offline.md) | 輪詢與快照、版本衝突、時鐘、連線狀態、離線快取、備援 Rundown |
| [access-control.md](design/access-control.md) | Token、復原碼、角色權限、撤銷與重發 |
| [screen-design.md](design/screen-design.md) | 各畫面的資訊架構 |
| [api.md](design/api.md) | API 規格：驗證結果、端點、權限、各角色快照、錯誤對照（依里程碑追加） |
| [tech-stack.md](design/tech-stack.md) | 技術選型、部署與託管、專案結構與測試、實機驗證 |

## architecture/ 架構

| 文件 | 內容 |
| --- | --- |
| [overview.md](architecture/overview.md) | 一頁全貌：目標與限制、系統情境、容器、部署視圖、橫切關注 |

## operations/ 維運

| 文件 | 內容 |
| --- | --- |
| [runbook.md](operations/runbook.md) | 部署與回退、當天的維運與故障重建、婚前檢查、婚後收尾 |

## process/ 開發流程

| 文件 | 內容 |
| --- | --- |
| [development-process.md](process/development-process.md) | 輕量版開發流程：角色、里程碑、故事做法、三個風險邊界的審查、Git 與 CI |

## adr/ 架構決策紀錄

每個重大決定的背景、考慮過的選項與後果。索引與格式見 [adr/README.md](adr/README.md)。

## archive/ 歸檔

不再維護的原始紀錄，只供查證；內容可能已過時，以正本為準。

| 文件 | 內容 |
| --- | --- |
| [clarification.md](archive/clarification.md) | 初期的需求釐清問答；每題的去向見 `glossary.md` 第 3 節 |

## 狀態

> 只寫現況；歷史見 git log。

- **目前里程碑**：M2a（平移、新人儀表板、第一次真人彩排），目標 2026-12 中。M1 已於 2026-10-22 經 PO 驗收；需要真實裝置的情境條件移到 M2a 彩排。
- **文件整合**：ADR-0017 的六個步驟已完成；文件規則由 `internal/docscheck` 的測試在 CI 檢查。
- **決定日**：2027-04-15，未通過完整彩排就改用紙本 + LINE 群組。
- **已知問題**：無。
- **尚未完成**：見 `requirements/scope.md` 的功能清單。

### 風險

| 代號 | 風險 | 處理 |
| --- | --- | --- |
| RISK-1 | Android（Chrome、LINE、主畫面）實機驗證未做；`#t=` 是否保留若不成立，要重審 ADR-0002 | M3 |
| RISK-2 | 延遲預算沒有緩衝（`design/sync-and-offline.md` 第 7 節） | M3 負載測試；必要時把輪詢縮到 1～1.5 秒 |
| RISK-3 | 單一 VM 單點故障；整個 `asia-east1` 故障時不切換區域 | 刻意接受；離線快取、紙本、15 分鐘恢復 |
| RISK-4 | 當天維運腳本未寫、未演練（`operations/runbook.md` 仍為要求草案） | M3；寫成腳本，在暫存專案以備援操作者帳號演練 |
| RISK-5 | 備援操作者與當天總召未定；未定期間復原碼只有開發者保管（ADR-0018） | 每個里程碑檢查；備援操作者須符合 `operations/runbook.md` 第 2 節的條件（不在婚禮工作組、帶已登入 gcloud 的筆電與熱點）；找不到就接受當天只剩離線快取與紙本（ADR-0013） |
| RISK-6 | 正式網域尚未購買（原型用 `sslip.io`） | 發出任何真實連結前處理 |
| RISK-7 | iOS 會清除 7 天未互動網站的本機資料與 Service Worker | 活動前一天請所有人再開一次連結（`operations/runbook.md` 第 3 節） |
| RISK-8 | `GET /api/ping` 揭露版本號；Cue id 遞增 | 接受 |
| RISK-9 | 維護模式尚未實作 | M3；M3 前正式環境不放真實資料 |
| RISK-10 | 角色類型的權限（`design/access-control.md` 第 4 節）由 store 的呼叫端檢查 | 接受 |
| RISK-11 | 復原碼可在 Draft 階段把日期延到 2199 年以延長保留 | 接受；屬「復原碼外流＝完全接管」 |
| RISK-12 | `secure_delete` 的檔案層抹除要等 checkpoint | 演練時驗證 |
| RISK-13 | 非 Master 的 Cue 寫入不檢查版本 | 接受 |
| RISK-14 | 舊版回退後，新格式的最後一步 Cue 操作無法復原（ADR-0015） | 接受 |
| RISK-15 | iOS Safari 在螢幕鎖定或切到背景時暫停輪詢 | 回到前景時立即輪詢；新秘有螢幕常亮，影響較小 |
| RISK-16 | 場地網路品質（地下室、訊號弱的宴會廳） | 婚前到場地實測；必要時準備行動熱點 |
| RISK-17 | 系統以外的單點：GCP 帳單停權、網域到期、開發者的 Google 帳號被鎖；任一項發生，備援操作者都無權處理 | 決定日前依 `operations/runbook.md` 第 3 節處理：信用卡效期、預算告警、網域續約與鎖定、第二位 Owner（開發者本人的另一個帳號） |
