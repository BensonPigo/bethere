# BeThere 文件索引

建議閱讀順序：先 `requirements/product-vision.md`，再需求、設計。後面的文件依賴前面的。想先看全貌，從 `architecture/architecture-overview.md` 開始；想知道某個決定為什麼這樣選，看 `adr/`。

## process/ 開發流程

| 文件 | 內容 |
| --- | --- |
| [development-process.md](process/development-process.md) | 輕量版開發流程：角色、里程碑、故事做法、三個風險邊界的審查、Git 與 CI、待決定事項 |

## requirements/ 需求

| 文件 | 內容 |
| --- | --- |
| [product-vision.md](requirements/product-vision.md) | **產品願景與原則**：要解決的問題、兩個核心價值、V1～V5 原則；所有取捨的最上層依據 |
| [user-stories.md](requirements/user-stories.md) | 角色、現場使用情境與使用者故事 |
| [clarification.md](requirements/clarification.md) | 需求釐清清單與已確認決定、MVP（P0／P1／P2）範圍 |
| [m1-acceptance.md](requirements/m1-acceptance.md) | M1 最小閉環的故事與驗收條件（功能條件與情境條件） |

## design/ 設計（P0 範圍）

| 文件 | 內容 |
| --- | --- |
| [data-model.md](design/data-model.md) | 實體、時間平移演算法、狀態轉換 |
| [sync-and-offline.md](design/sync-and-offline.md) | 即時同步、快照、離線快取、備援 Rundown |
| [access-control.md](design/access-control.md) | Token 設計、角色權限、撤銷 |
| [screen-design.md](design/screen-design.md) | 各角色畫面的資訊架構 |
| [api.md](design/api.md) | API 規格：驗證結果、端點、權限、各角色快照、錯誤對照（依里程碑追加） |
| [tech-stack.md](design/tech-stack.md) | 技術選型、部署與託管（S7）、當天維運、實機驗證項目 |

## architecture/ 架構視圖

把設計文件串成全貌，圖以 Mermaid 撰寫。細節仍以 `design/` 為準。

| 文件 | 內容 |
| --- | --- |
| [architecture-overview.md](architecture/architecture-overview.md) | 目標與限制、品質目標、系統情境、容器、元件、部署視圖、橫切關注、風險 |
| [runtime-views.md](architecture/runtime-views.md) | 循序圖：開啟連結與輪詢、版本衝突、時間平移、撤銷重發、復原碼、部署回退、故障重建 |
| [behavior-models.md](architecture/behavior-models.md) | 狀態圖：活動、Cue、角色連結、客戶端連線、常亮提示層；領域類別圖 |

## adr/ 架構決策紀錄

每個重大決定的背景、考慮過的選項與後果。索引與格式見 [adr/README.md](adr/README.md)。

## 狀態

> 只寫現況；歷史見 git log。

- **目前里程碑**：M1 最小閉環已實作並通過獨立驗證，待 PO 在電腦上 Demo 驗收；下一個是 M2a（平移、新人儀表板、第一次真人彩排）。
- **已完成**：需求與 P0 設計；技術選型（`design/tech-stack.md`）；iOS 實機驗證（`tech-stack.md` 5.1）；`internal/domain` 與 `internal/store`。
- **P0 範圍**：2026-10-21 縮小，CSV 匯入、以角色預覽、連結管理介面延到 P1（`requirements/clarification.md`）。
- **決定日**：2027-04-15，未通過完整彩排就改用紙本 + LINE 群組。
- **已知問題**：無。
- **已知且接受的風險（store 層）**：角色類型權限（`access-control.md` 第 4 節）由呼叫端檢查；復原碼可在 Draft 階段把日期延到 2199 年以延長保留（屬「復原碼外流＝完全接管」）；`secure_delete` 的檔案層抹除待 checkpoint，需演練驗證；非 Master 的 Cue 寫入不檢查版本；舊版回退後新格式的最後一步無法復原。
- **尚未完成**：API 層、前端正式畫面、Android 實機驗證、當天維運腳本與演練（`tech-stack.md` 3.1 仍為要求草案）。
- **未定**：當天的總召與備援操作者人選。
