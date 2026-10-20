# BeThere 文件索引

建議閱讀順序：先需求、再設計。後面的文件依賴前面的。

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

## 狀態

- 需求與 P0 設計決定皆已確認。
- 尚未決定：部署與託管（S7）、技術選型。
