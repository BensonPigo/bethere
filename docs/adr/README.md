# 架構決策紀錄 (Architecture Decision Records)

每一份 ADR 記錄一個「改了就很難回頭」的架構決定：當時的背景、考慮過哪些選項、選了什麼、要付出什麼代價。

## 與設計文件的分工

- **ADR 回答「為什麼」**：背景、取捨與後果。決定一旦接受就不改寫內容；要推翻時新增一份 ADR，並把舊的狀態改為「被 ADR-NNNN 取代」。
- **設計文件回答「是什麼」**：`docs/design/` 的欄位、規則與數字才是實作依據。ADR 只摘要，細節以設計文件為準。
- 設計文件內各自的決定編號（M、S、P、U、T）照舊保留；下表列出每份 ADR 對應的編號。

## 格式

檔名 `NNNN-short-title.md`，四位數流水號，不重複使用。內容依 [0000-template.md](0000-template.md)。

狀態：`提議中` → `已接受` → （`已棄用` 或 `被 ADR-NNNN 取代`）。

## 索引

| # | 決定 | 狀態 | 對應編號 |
| --- | --- | --- | --- |
| [0001](0001-record-architecture-decisions.md) | 以 ADR 記錄架構決策 | 已接受 | — |
| [0002](0002-token-links-in-url-fragment.md) | 免註冊：角色連結的 Token 放在網址片段並保留 | 已接受 | P2、P3、T6、T19、R1 |
| [0003](0003-single-master-with-recovery-code.md) | 只有 Master 一種管理身分，另以復原碼救援 | 已接受 | P1、P5 |
| [0004](0004-polling-full-snapshots.md) | 同步採每 2 秒輪詢完整快照，不用推送 | 已接受 | S1、S2、S3 |
| [0005](0005-optimistic-writes-no-offline-queue.md) | 寫入以版本號拒絕過期操作，離線時不排隊 | 已接受 | S5、S6、M7 |
| [0006](0006-per-cue-offset-and-shift-log.md) | 偏移存在每個 Cue 上，平移留紀錄以精確復原 | 已接受（儲存方式由 0015 修訂） | M1、M5、M6、M8、A1～A3 |
| [0007](0007-server-side-domain-logic.md) | 領域邏輯與資料過濾只在伺服器 | 已接受 | T5、F3、E7 |
| [0008](0008-go-backend-preact-frontend.md) | 後端 Go、前端 Preact + TypeScript | 已接受 | T-A、T1～T17 |
| [0009](0009-single-gce-vm-sqlite-litestream.md) | 單一 GCE VM + SQLite + Litestream 備份 | 已接受 | S7、T-B、T3 |
| [0010](0010-offline-shell-and-local-snapshot.md) | 離線：Service Worker 只快取外殼、快照存本機；LINE 視為受限環境 | 已接受 | S4、T18、T20、T22 |
| [0011](0011-auto-end-and-data-retention.md) | 活動自動結束，伺服器端資料 90 天內消失 | 已接受 | P4、M9、C5、T10、T-F |
| [0012](0012-fail-safe-startup-and-maintenance-mode.md) | 失效安全：不自動建庫、維護模式 | 已接受 | T3b、T3c、T3d |
| [0013](0013-day-of-operations-by-backup-operator.md) | 當天維運交給備援操作者，不做跨區域備援 | 已接受 | T-E |
| [0014](0014-printable-backup-rundown.md) | 備援 Rundown 用列印專用網頁 | 已接受 | T-C、T21 |
| [0015](0015-cue-ops-in-action-log.md) | Cue 操作以前後狀態記在 ActionLog，取代 ShiftLog | 已接受 | M7、M10、T4 |
