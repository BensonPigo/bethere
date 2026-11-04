# 品質現況

量測日期：2026-11-04，commit：f42618d

## CRAP

門檻 12。函式總數 304，通過 269，超標 35（補測試就能過 4，要拆函式 31），優先處理 2。

覆蓋率算整個模組的測試，其他套件的測試執行到的程式碼也算（go.coverage_scope = module）。

修改次數是 6 months ago 以來改到這個函式的 commit 數（建立的那一次不算），只算超標的函式。超標且修改次數 ≥ 5 的標「優先處理」，排在最前面。

| CRAP | C | 覆蓋率 | 修改次數 | 函式 | 位置 | 改善方向 | 優先處理 |
|---|---|---|---|---|---|---|---|
| 68.6 | 68 | 94.9% | 6 | `readBody` | `internal/api/body.go:146` | 要拆函式 | 優先處理 |
| 15.6 | 13 | 75.0% | 5 | `run` | `cmd/bethere/main.go:71` | 要拆函式 | 優先處理 |
| 30.2 | 30 | 94.4% | 1 | `validateRows` | `internal/store/rundown.go:164` | 要拆函式 |  |
| 28.7 | 28 | 90.5% | 0 | `checkOrigin` | `internal/api/prep.go:88` | 要拆函式 |  |
| 27.2 | 24 | 82.2% | 1 | `(*Store).InsertCue` | `internal/store/liveedit.go:48` | 要拆函式 |  |
| 26.2 | 23 | 81.8% | 1 | `(*Store).ReplaceRundown` | `internal/store/prep.go:419` | 要拆函式 |  |
| 24.4 | 22 | 82.9% | 0 | `readNewRoles` | `internal/api/body.go:391` | 要拆函式 |  |
| 23.5 | 23 | 90.5% | 0 | `(*Store).SetTaskState` | `internal/store/jobs.go:142` | 要拆函式 |  |
| 23.2 | 21 | 82.8% | 0 | `readRoles` | `internal/api/body.go:340` | 要拆函式 |  |
| 21.9 | 19 | 80.0% | 2 | `applyMigration` | `internal/store/store.go:242` | 要拆函式 |  |
| 20.0 | 4 | 0.0% | 0 | `(ImportCheck).OK` | `internal/store/prep.go:389` | 覆蓋率要到 21% |  |
| 18.7 | 17 | 81.8% | 1 | `(*Store).SetCueRoles` | `internal/store/liveedit.go:122` | 要拆函式 |  |
| 17.8 | 15 | 76.9% | 1 | `(*Store).DeleteRole` | `internal/store/prep.go:155` | 要拆函式 |  |
| 17.5 | 16 | 81.8% | 0 | `resetRecoveryCode` | `cmd/bethere/main.go:468` | 要拆函式 |  |
| 17.0 | 17 | 100.0% | 4 | `loadSnapshotData` | `internal/store/snapshot.go:118` | 要拆函式 |  |
| 17.0 | 17 | 100.0% | 2 | `PlanShift` | `internal/domain/shift.go:46` | 要拆函式 |  |
| 17.0 | 13 | 71.4% | 0 | `(*API).previewRole` | `internal/api/prep.go:235` | 要拆函式 |  |
| 16.5 | 14 | 76.7% | 0 | `(*Store).AdminOverview` | `internal/store/admin.go:106` | 要拆函式 |  |
| 15.5 | 15 | 87.1% | 2 | `(*Store).applyCueOp` | `internal/store/cues.go:153` | 要拆函式 |  |
| 15.1 | 15 | 92.7% | 3 | `addRole` | `cmd/bethere/main.go:364` | 要拆函式 |  |
| 15.0 | 15 | 100.0% | 1 | `parseRow` | `internal/rundown/rundown.go:145` | 要拆函式 |  |
| 15.0 | 15 | 100.0% | 0 | `parseInt` | `internal/api/body.go:471` | 要拆函式 |  |
| 15.0 | 14 | 82.9% | 1 | `(*Store).ImportRundown` | `internal/store/rundown.go:73` | 要拆函式 |  |
| 14.7 | 13 | 78.6% | 1 | `(*Store).ReplaceChecklist` | `internal/store/jobs.go:296` | 要拆函式 |  |
| 14.4 | 14 | 87.1% | 0 | `(*Store).MoveCue` | `internal/store/prep.go:276` | 要拆函式 |  |
| 14.3 | 14 | 88.2% | 0 | `loadSnapshotCues` | `internal/store/snapshot.go:195` | 要拆函式 |  |
| 14.1 | 14 | 93.1% | 1 | `parseGuests` | `internal/api/jobs.go:63` | 要拆函式 |  |
| 14.0 | 14 | 100.0% | 3 | `buildRole` | `internal/api/snapshot.go:369` | 要拆函式 |  |
| 13.9 | 13 | 82.6% | 1 | `(*Store).UpdateCue` | `internal/store/prep.go:231` | 要拆函式 |  |
| 13.9 | 13 | 82.8% | 1 | `(*Store).UndoLastCueOp` | `internal/store/cues.go:205` | 要拆函式 |  |
| 13.0 | 13 | 96.8% | 1 | `parse` | `internal/rundown/rundown.go:90` | 要拆函式 |  |
| 13.0 | 13 | 100.0% | 3 | `allowed` | `internal/api/api.go:209` | 要拆函式 |  |
| 12.4 | 11 | 77.3% | 1 | `(*Store).ReplaceGuests` | `internal/store/jobs.go:228` | 覆蓋率要到 80% |  |
| 12.4 | 11 | 77.4% | 2 | `(*Store).PurgeEnded` | `internal/store/events.go:337` | 覆蓋率要到 80% |  |
| 12.3 | 12 | 87.9% | 1 | `(*Store).CreateEvent` | `internal/store/events.go:78` | 覆蓋率要到 100% |  |

## 抓錯檢查

抓錯率 =（抓到 + 逾時）÷（抓到 + 逾時 + 沒抓到），看有測到的程式碼測得夠不夠嚴。
測到率 =（抓到 + 逾時 + 沒抓到）÷（再加上測試沒跑到），看有多少程式碼有測試。兩個數字要一起看。

每個改壞的版本跑整個模組的測試（go.coverage_scope = module，gremlins --integration --coverpkg）。

| 套件 | 抓到 | 逾時 | 沒抓到 | 測試沒跑到 | 抓錯率 | 測到率 |
|---|---|---|---|---|---|---|
| `cmd/bethere` | 101 | 0 | 11 | 3 | 90.18% | 97.39% |
| `internal/api` | 265 | 0 | 53 | 18 | 83.33% | 94.64% |
| `internal/auth` | 18 | 0 | 3 | 7 | 85.71% | 75.00% |
| `internal/domain` | 121 | 0 | 14 | 12 | 89.63% | 91.84% |
| `internal/rundown` | 35 | 0 | 10 | 0 | 77.78% | 100.00% |
| `internal/server` | 6 | 0 | 0 | 0 | 100.00% | 100.00% |
| `internal/store` | 478 | 3 | 45 | 37 | 91.44% | 93.43% |
