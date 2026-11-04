# 計畫：拆開 internal/api/body.go 的請求解析

狀態：草稿，待核准。單一 slice，ID = RB1。

## 目標

`internal/api/body.go` 有四個函式超過 CRAP 門檻 12（quality-baseline.md，量測 commit f42618d；之後 body.go 沒有改過）：

| 函式 | 複雜度 | 覆蓋率 | CRAP | 修改次數 |
|---|---|---|---|---|
| `readBody` | 68 | 94.9% | 68.6 | 6（優先處理） |
| `readNewRoles` | 22 | 82.9% | 24.4 | 0 |
| `readRoles` | 21 | 82.8% | 23.2 | 0 |
| `parseInt` | 15 | 100% | 15.0 | 0 |

把它們拆成每個 CRAP 都不超過 12 的小函式，**每個請求被接受或拒絕的結果、解析出來的值都不變**。第二關只檢查改到的函式，動到其中一個，同一個 PR 裡的其他函式也要過門檻，所以四個一起做。`readIDs` 只會因為改用 `expectDelim` 被動到，也在範圍內，同樣要 ≤ 12。

順帶消掉抓錯檢查留下的 6 個「看起來改了也沒差、但不能寫進排除清單」的改壞版本（body.go 157、360、411 第 18 欄，375、433 第 42 欄，473 第 12 欄）。排除清單只比對檔案、改法和那一行的原文，不看欄位；這幾行同一種改法還蓋到真正的檢查，所以要把兩個條件拆到不同行。

`readBody` 是所有寫入請求唯一的輸入檢查，其中 `recoverMaster`（recover.go）、邀請建活動（invite.go）、admin.go 在驗證身分之前就會呼叫它，所以這是輸入驗證的程式碼，照風險工作處理。

## 不變的行為

以 design/api.md 第 1 節和目前的 `origin/main:internal/api/body.go` 為準。驗收要確認的至少包括：

- 只收一個 JSON 物件，大小上限照 `bodyLimit`；物件後面只能有空白。`fieldSet{}` 只收 `{}`。
- 欄位名稱大小寫要完全相符（比對的是 JSON 解碼後的名稱，所以 `"version"` 等同 `"version"`），重複的欄位、不認得的欄位都拒絕；`idList` 和 `optString` 以外的欄位都必填。
- 數字只收純整數：不收 `1.0`、`1e0`、`-0`、前導零，最多 18 位數。
  - 不帶正負號的要 ≥ 1。
  - `signedInt` 限 int32 範圍。
  - `count` 可以是 0，範圍 0..2^31−1，以 `signed=true` 解析，`-0` 也拒絕。
- 布林只收 JSON 的 `true`、`false`，包括 `newRoles` 項目裡的 `checksIn`。
- 時間用 `time.Parse(time.RFC3339Nano, …)`。
- 欄位寫進 `body` 的對應不變：
  - `acked`、`arrived`、`done` → `b.on`。
  - `text`、`recoveryCode`、`inviteCode` → `b.text`。
  - `date` → `b.date`。
  - `origin` 不論是 `text` 還是 `optString`，都同時設定 `origin` 和 `originGiven=true`。
- null 的意義不變：
  - `nullableID` 是 null 時 `afterCueID` 維持 0。
  - `optString` 的 null → nil，`""` → 指向 `""` 的指標。
- `roles`：
  - 每項剛好是 `{"roleId":n,"roleNote":"…"|null}`，`roleId` ≥ 1。
  - 兩個欄位都有了之後才檢查同一個角色有沒有出現兩次。
  - 最多 50 項，第 51 項在讀之前就拒絕。
- `newRoles`：
  - 每項剛好是 `{"name":"…","view":"…","checksIn":bool}`，不認得的欄位拒絕。
  - 名稱照原樣存，只有比對重複時用 `strings.TrimSpace`（Unicode 空白也算）。
  - 最多 50 項，第 51 項在讀之前就拒絕。
- `excluded`：最多 200 個 id，第 201 個在讀之前就拒絕。
- 拒絕時一律回 `body{}, false`（或 `nil, false`、`errBadIDs`），中途寫進去的值不能漏出去。呼叫端只看 `ok`。
- 解析時不能 panic；錯誤訊息不能帶請求的內容（api.md 第 1 節：記錄不含請求內容）。

## 改法

0. **先補測試，在現在的程式上跑過。** 這些是新的規格測試，不改既有測試，留在 main。答案照上面的規格寫死，不呼叫正式程式碼算：
   - `roles` 50 項接受、51 項拒絕；`newRoles` 50 項接受、51 項拒絕；`excluded` 200 個接受（201 個拒絕已經有測試）。
   - 括號檢查：
     - `versionOnly` 收到 `["version",1]` 要拒絕。
     - `fieldSet{}` 收到 `[]` 要拒絕。
     - `{"roles":[["roleId",1,"roleNote",null]]}` 要拒絕。
   - 表格測試：每個 `fieldSet` 的每個欄位，依它的 `fieldKind` 都對應得到 `body` 的欄位。涵蓋：
     - body.go 裡所有的 `fieldSet`
     - `shiftFields(true)`、`shiftFields(false)`
     - `createEventFields`（invite.go）
     - `recoverFields`（recover.go）
     - `fieldSet{}`（admin.go）
1. 共用的小函式：
   - `expectDelim(dec, d) bool`：讀一個符號，必須是 `d`。取代 12 處 `err != nil || tok != json.Delim(X)`，包括 `readIDs` 的兩處。
   - `readKey(dec, seen) (string, bool)`：讀物件的一個欄位名稱。「讀不到或不是字串」和「重複」分成兩個 `if`，各在自己的一行。`readBody`、`readRoles`、`readNewRoles` 共用。
2. `readBody` 拆成：開頭 `expectDelim('{')` → 迴圈 `readKey` → `readField(dec, kind, key, &b) bool` → 結尾 `expectDelim('}')` → 檢查後面沒有東西 → `missingRequired(fields, seen) bool`。
   - `readField` 依 `fieldKind` 分派到每種欄位各自的小函式，例如 `readPositiveField`、`readSignedField`、`readCountField`、`readBoolField`、`readTextField`、`readNullableID` 等。不認得的 `fieldKind` 回 `false`。
   - 依欄位名稱寫進 `body` 的大 `switch key` 改成「欄位名稱 → 指向 `body` 欄位的指標」的對照，例如 `func intTarget(b *body, key string) *int64`，讓每個函式的分支數降下來。對照不到時回 `nil`，呼叫的地方看到 `nil` 就回 `false`，不能對 `nil` 寫值。
   - `count` 照原樣以 `signed=true` 解析，不改走 `readInt(dec, false)`。
   - 任何一步回 `false`，`readBody` 都回 `body{}, false`。
3. `readRoles`、`readNewRoles`：外層讀陣列，每一項交給 `readRoleItem`、`readNewRoleItem`。
   - 上限檢查留在讀下一項之前。
   - 每一項有自己新的 `seen`，不跟外層或別的項目共用。
   - 項目的結尾檢查拆成兩行：`expectDelim('}')` 一行，「缺欄位或重複」一行。
4. `parseInt`：`digits := raw; if signed { digits = strings.TrimPrefix(raw, "-") }`，不再用 `signed && len(raw) > 0 && raw[0] == '-'`。
   - 長度和前導零、只含數字兩項檢查各抽成小函式。
   - `strconv.ParseInt` 仍然吃 `raw`（帶正負號），`raw == "-0"` 的檢查保留。
5. 不改任何呼叫端（`readBody` 7 處：admin.go、api.go、invite.go、prep.go 兩處、recover.go、shift.go），不改 `fieldSet`、`fieldKind`、`body` 的定義，不改 design/api.md。

## 排除清單

`.quality-equivalents.json` 現在 7 項都在 body.go，比對的是「那一行的原文」。這次改動後多數對不上，工具會列成「沒用到」。還對得上的也可能換了位置和意義，例如 `readInt` 裡的 `if err != nil || !isNumber {`。

- 舊的理由不能照搬到共用的小函式上。例如舊理由是「開頭括號被改壞了，結尾括號的檢查還是會拒絕」，但 `expectDelim` 同時負責開頭和結尾，改壞一次兩邊一起壞。改法第 0 步的括號測試就是要抓這種。
- 同一個 PR 裡更新排除清單：
  - 刪掉對不上的項目。
  - 還對得上的項目，依新的程式重新判斷。
  - 對新程式碼的抓錯檢查結果，真的改了也沒差的，逐項列出：檔案、行號、改法、原文、理由。另外列出 body.go 裡所有原文相同的行，逐行說明為什麼都改了也沒差；說不清楚的就不提。
  - 由人核准後才寫進去。
- 依 BeThere 的 CLAUDE.md，AI 不自己改這個檔案；寫法跟 PR #10 一樣，要明確同意。

## 驗收

1. 行為不變：
   - `go test ./...` 全過，既有測試一行都不改；改法第 0 步的新測試在改動前的 commit 上也全過。
   - 對照測試：
     - **舊版**：把 `origin/main:internal/api/body.go` 的整條呼叫鏈複製到測試檔並改名，包括 `readBody`、`readInt`、`readString`、`readOptString`、`readRoles`、`readNewRoles`、`readIDs`、`parseInt`。除了改名之外，和 `git show origin/main:internal/api/body.go` 對應的部分完全一樣。
     - **Fuzz 目標**：一個 Go fuzz 目標，每個輸入對所有 `fieldSet`（同改法第 0 步的清單）各跑一次新舊兩版。
     - **比較方式**：用 `reflect.DeepEqual` 比較整個 `body` 和 `ok`，拒絕的輸入也比，任一邊 panic 都算失敗。
     - **種子**：每個 `fieldSet` 一個合法的請求，加上下列輸入：
       - 既有測試裡所有拒絕的字串：server/api_test.go 的 `TestWriteBodyIsStrict`、shift_and_notice_test.go、body_test.go。
       - roles 50/51 項、newRoles 50/51 項、excluded 200/201 個。
       - Unicode 空白造成的重複名稱，例如 `"　攝影"`。
       - 跳脫過的欄位名稱，例如 `{"version":1}`。
       - `notePublic`、`roleNote`、`afterCueId` 的 null 和字串。
       - 各種 RFC 3339 寫法：時區、小數秒、小寫 t/z。
       - 接近 16 KB 和 256 KB 的請求。
     - **跑多久**：`go test -fuzz` 至少跑 30 分鐘，沒找到差異才算過。
     - **`parseInt` 窮舉**：另外對 `{0-9,-,+,.,e,E}` 組成、長度 0～5 的所有字串，`signed` 為 true 和 false 各比對一次新舊兩版。
     - **留下紀錄**：對照測試在功能分支上以獨立的 commit 加入，PR 合併前再用另一個 commit 刪掉，合併後的檔案裡不留。PR 描述寫下：
       - 對照測試那個 commit 的 SHA
       - 執行指令
       - Go 版本
       - 跑的時間
       - 結果

       verifier 可以 checkout 那個 SHA 重跑。
2. CRAP：`python tools/quality/quality.py crap --base origin/main` 通過，body.go 每個函式都 ≤ 12。
3. 抓錯檢查：
   - **前提**：功能分支以啟用 `mutation` 關卡之後的 `main` 為基礎（`.quality.json` 的 `gates` 有 `mutation`，PR #11）。
   - **在哪個 commit 跑**：抓錯檢查會對每個改壞版本跑整個 module 的測試，對照測試還在的話，幾乎都會被它抓到，量不出留下來的測試夠不夠。所以要在對照測試已經刪掉的 commit 上跑，原則上就是 PR 最後的 head。
   - **指令**：在那個 commit 上執行 `python tools/quality/quality.py mutation --base origin/main --json <暫存路徑>`。
   - **結果判定**：
     - 輸出必須列出 body.go 的改壞版本，以及抓到、沒抓到、測試沒跑到的數量。
     - 印出「還沒啟用」，或報告的 result 是 skipped，都算**失敗**，不算通過。
   - **通過條件**：body.go「沒抓到」和「測試沒跑到」的改壞版本必須是零，或者每一項都已經逐項列出、由人核准寫進排除清單。
   - **紀錄**：PR 描述寫下：
     - 跑抓錯檢查那個 commit 的 SHA
     - JSON 報告的 `commit` 欄位，必須等於這個 SHA
     - 數量
     - 沒抓到的清單

     這些和對照測試的 SHA 放在一起。CI 的第三關只檢查門檻（抓錯率 80%、測到率 50%），不能取代這一項。
4. CI 全過。
5. 做完由獨立的 verifier 對第 1～3 項出具 CONFIRMED，才請你合併。verifier 要確認的事：
   - 舊版副本和 `origin/main` 的差別只有改名。
   - `fieldSet` 清單完整。
   - 種子有上面列的邊界輸入。
   - 第 3 項記錄的 SHA 上沒有對照測試，在那個 SHA 重跑抓錯檢查，數量相同。

   安全審查發現的處置寫在 PR 描述，不寫進這份文件（CLAUDE.md：文件不寫審查紀錄）。

## 非目標

- 不改 API 的行為和錯誤碼，不改其他檔案的超標函式（`checkOrigin`、`run` 等另案處理）。
- 不換掉 `json.Decoder` 逐符號讀取的做法（design/api.md 第 1 節的理由不變）。
- 不比對 `ResponseWriter` 的副作用和讀了多少位元組；`MaxBytesReader(…, bodyLimit(fields))` 的寫法不改。
- 不重跑 baseline；合併後要更新 quality-baseline.md 時再跑一次。

## 回復

單一 PR，revert 就回到現在的 body.go 和排除清單。改法第 0 步的新測試在舊程式上也會過，revert 時可以留著。

## 停止條件

- 改法第 0 步的新測試在現在的程式上沒過：停下來回報，那表示規格和程式不一致，要先由人決定。
- 對照測試找到任何差異，而且無法在不改行為的前提下修好：停下來回報。
- 拆完仍有函式超過 12，又沒辦法不改行為再拆：停下來，列出那個函式和原因。
