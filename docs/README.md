# BeThere 文件索引

建議閱讀順序：先需求、再設計。後面的文件依賴前面的。想先看全貌，從 `architecture/architecture-overview.md` 開始；想知道某個決定為什麼這樣選，看 `adr/`。

## requirements/ 需求

| 文件 | 內容 |
| --- | --- |
| [user-stories.md](requirements/user-stories.md) | 角色、現場使用情境與使用者故事 |
| [clarification.md](requirements/clarification.md) | 需求釐清清單與已確認決定、MVP（P0／P1／P2）範圍 |

## design/ 設計（P0 範圍）

| 文件 | 內容 |
| --- | --- |
| [data-model.md](design/data-model.md) | 實體、時間平移演算法、狀態轉換 |
| [sync-and-offline.md](design/sync-and-offline.md) | 即時同步、快照、離線快取、備援 Rundown |
| [access-control.md](design/access-control.md) | Token 設計、角色權限、撤銷 |
| [screen-design.md](design/screen-design.md) | 各角色畫面的資訊架構 |
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

- 需求與 P0 設計決定皆已確認。
- 2026-10-20 依設計 review 修訂：合併 Owner 與 Master、管理密碼改為復原碼、同步改為輪詢、Token 保留在網址、活動自動結束、平移檢查與新秘倒數規則（各文件以 ✎ 標示）。
- 2026-10-20 完成技術選型（`tech-stack.md`）：Go + SQLite 後端、Preact 前端、GCE 台灣單一 VM + Litestream→GCS（同日由 Fly.io 改為 GCE）；C5 改為伺服器端所有副本 90 天內消失。經七輪三方抗辯審查。
- 2026-10-21 完成 iOS 實機驗證（`tech-stack.md` 5.1）：R1～R5 成立；LINE 內建瀏覽器無法離線開啟；重開頁面後需點擊才能常亮，`screen-design.md` V2a 加上常亮提示層。
- 2026-10-21 新增架構視圖（`architecture/`）與 14 份架構決策紀錄（`adr/`），內容整理自既有設計文件，未新增或改變決定。
- 尚未完成：`tech-stack.md` 3.1 當天維運手冊為要求草案，待實作時寫成腳本並演練驗證；Android 實機驗證；`behavior-models.md` 第 2 節「略過能否復原」待確認。
