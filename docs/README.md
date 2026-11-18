# BeThere 文件索引

先讀 `product/vision.md`，再讀 `requirements/`、`design/`。想看全貌從 `architecture/overview.md` 開始；想知道某個決定為什麼這樣選，看 `decisions.md`；看到不懂的代號，查 `glossary.md`；專案做到哪裡、有哪些風險，看 `status.md`。

所有文件只寫現況；修訂經過只在 git 紀錄。

| 文件 | 內容 |
| --- | --- |
| [product/vision.md](product/vision.md) | 產品願景與原則（V1～V5）；所有取捨的最上層依據 |
| [requirements/scope.md](requirements/scope.md) | 功能清單 × 優先層級 × 里程碑；限制；非功能需求（NFR）；原始需求對照 |
| [requirements/roles-and-scenarios.md](requirements/roles-and-scenarios.md) | 各角色的現場使用情境與原始使用者故事 |
| [requirements/feature-tree.md](requirements/feature-tree.md) | 功能樹：照使用者整理的功能導覽與各畫面網址 |
| [requirements/stories.md](requirements/stories.md) | 所有故事與驗收條件（US-x-n） |
| [design/data-model.md](design/data-model.md) | 實體、狀態轉換、時間平移演算法、領域服務 |
| [design/sync-and-offline.md](design/sync-and-offline.md) | 輪詢與快照、版本衝突、時鐘、連線狀態、離線快取、備援 Rundown |
| [design/access-control.md](design/access-control.md) | Token、復原碼、角色權限、撤銷與重發 |
| [design/screen-design.md](design/screen-design.md) | 各畫面的資訊架構 |
| [design/api.md](design/api.md) | API 規格：驗證結果、端點、權限、各角色快照、錯誤對照 |
| [design/tech-stack.md](design/tech-stack.md) | 技術選型、部署與託管、專案結構與測試、實機驗證結果 |
| [architecture/overview.md](architecture/overview.md) | 一頁全貌：系統情境、容器、部署視圖、橫切關注 |
| [operations/runbook.md](operations/runbook.md) | 部署與回退、當天的維運與故障重建、婚前檢查、婚後收尾、建立活動 |
| [operations/incident-card.md](operations/incident-card.md) | 當天出事速查卡：看到什麼症狀、該做哪一個程序 |
| [process/development-process.md](process/development-process.md) | 開發流程：角色、里程碑、故事做法、三個風險邊界的審查、Git 與 CI |
| [process/e2e-test-routes.md](process/e2e-test-routes.md) | 端對端測試路線 |
| [status.md](status.md) | 現況與風險：做到哪裡、還沒做什麼、風險與待驗證項目（RISK-n） |
| [decisions.md](decisions.md) | 現行決定（ADR-NNNN）與理由 |
| [glossary.md](glossary.md) | 術語與代號規則 |
