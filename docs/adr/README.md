# 架構決策紀錄 (Architecture Decision Records)

每一份 ADR 記錄一個「改了就很難回頭」的架構決定：當時的背景、考慮過哪些選項、選了什麼、要付出什麼代價。

## 與設計文件的分工

- **ADR 回答「為什麼」**：背景、取捨與後果。決定一旦接受就不改寫內容；要推翻時新增一份 ADR，並把舊的狀態改為「被 ADR-NNNN 取代」。
- **設計文件回答「是什麼」**：`docs/design/` 的欄位、規則與數字才是實作依據。ADR 只摘要，細節以設計文件為準。
- 新的決定一律寫成 ADR，設計文件不再開決定表（ADR-0017）。每份 ADR 的「對應」列出相關的設計章節；舊的決定代號（M、S、P、U、T 等）已退役，意思見 `glossary.md` 第 3 節。

## 格式

檔名 `NNNN-short-title.md`，四位數流水號，不重複使用。內容依 [0000-template.md](0000-template.md)。

狀態：`提議中` → `已接受` → （`已棄用` 或 `被 ADR-NNNN 取代`）。

## 索引

| # | 決定 | 狀態 |
| --- | --- | --- |
| [0001](0001-record-architecture-decisions.md) | 以 ADR 記錄架構決策 | 已接受 |
| [0002](0002-token-links-in-url-fragment.md) | 免註冊：角色連結的 Token 放在網址片段並保留 | 已接受 |
| [0003](0003-single-master-with-recovery-code.md) | 只有 Master 一種管理身分，另以復原碼救援 | 已接受（保管方式由 0018 修訂） |
| [0004](0004-polling-full-snapshots.md) | 同步採每 2 秒輪詢完整快照，不用推送 | 已接受 |
| [0005](0005-optimistic-writes-no-offline-queue.md) | 寫入以版本號拒絕過期操作，離線時不排隊 | 已接受 |
| [0006](0006-per-cue-offset-and-shift-log.md) | 偏移存在每個 Cue 上，平移留紀錄以精確復原 | 已接受（儲存方式由 0015 修訂） |
| [0007](0007-server-side-domain-logic.md) | 領域邏輯與資料過濾只在伺服器 | 已接受 |
| [0008](0008-go-backend-preact-frontend.md) | 後端 Go、前端 Preact + TypeScript | 已接受 |
| [0009](0009-single-gce-vm-sqlite-litestream.md) | 單一 GCE VM + SQLite + Litestream 備份 | 已接受 |
| [0010](0010-offline-shell-and-local-snapshot.md) | 離線：Service Worker 只快取外殼、快照存本機；LINE 視為受限環境 | 已接受 |
| [0011](0011-auto-end-and-data-retention.md) | 活動自動結束，伺服器端資料 90 天內消失 | 已接受 |
| [0012](0012-fail-safe-startup-and-maintenance-mode.md) | 失效安全：不自動建庫、維護模式 | 已接受 |
| [0013](0013-day-of-operations-by-backup-operator.md) | 當天維運交給備援操作者，不做跨區域備援 | 已接受 |
| [0014](0014-printable-backup-rundown.md) | 備援 Rundown 用列印專用網頁 | 已接受 |
| [0015](0015-cue-ops-in-action-log.md) | Cue 操作以前後狀態記在 ActionLog，取代 ShiftLog | 已接受 |
| [0016](0016-extensibility-principles.md) | 為商業化留後路：只處理難改的結構，功能不提前建造 | 已接受 |
| [0017](0017-documentation-structure-and-id-scheme.md) | 文件結構與代號系統：單一正本、縮減代號、重組目錄 | 已接受 |
| [0018](0018-recovery-code-held-by-backup-operator.md) | 復原碼由備援操作者保管，不交給總召 | 已接受 |
| [0019](0019-additive-only-migrations.md) | 資料庫遷移只做向後相容的新增，正式資料前允許一次重建 | 已接受 |
| [0020](0020-draft-view-and-start-on-first-advance.md) | Draft 階段照常顯示角色的 Cue，第一次推進即開始活動 | 已接受 |
| [0021](0021-live-cue-insert-and-role-assignment.md) | 活動中可新增 Cue 並指派角色，被指派的角色會收到變更提示 | 已接受 |
| [0022](0022-overrun-prompt-and-delay-display.md) | 超時提示總召平移，新人儀表板的延誤含超時 | 已接受（延誤算法由 0026 修訂） |
| [0023](0023-snapshot-etag-by-content.md) | 快照條件請求以內容指紋判斷，就位打卡不改版本號 | 已接受 |
| [0024](0024-external-monitoring-and-alerts.md) | 外部監控伺服器與備份，告警在活動前改送備援操作者 | 已接受 |
| [0025](0025-screens-by-usage-situation.md) | 畫面依使用情況分類，與角色類型分開 | 已接受 |
| [0026](0026-delay-measured-on-next-pending-cue.md) | 新人儀表板的延誤以下一個待辦 Cue 為準 | 已接受 |
| [0027](0027-small-field-tests-and-one-full-rehearsal.md) | 里程碑改用小型實測，完整彩排只做一次 | 已接受 |
| [0028](0028-show-original-time-of-shifted-cues.md) | 被平移的環節旁顯示原時間，不顯示偏移量 | 已接受 |
| [0029](0029-e2e-routes-on-a-vm-test-service.md) | 端對端測試路線在 VM 的測試服務上，由 PO 手動執行 | 已接受 |
| [0030](0030-split-m3-into-data-ops-and-field-readiness.md) | M3 拆成資料與維運（M3a）、現場準備（M3b） | 已接受 |
| [0031](0031-pre-event-setup-and-more-roles-before-m3a.md) | M3a 之前先做婚前準備（M2c）與更多角色（M2d），第一版處理賓客 | 已接受 |
