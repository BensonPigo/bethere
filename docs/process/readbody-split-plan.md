# 計畫：拆開 internal/api/body.go 的請求解析

狀態：草稿，待核准（2026-11-04）。單一 slice，ID = RB1。

## 目標

`internal/api/body.go` 有四個函式超過 CRAP 門檻 12（quality-baseline.md，量測 commit f42618d）：

| 函式 | 複雜度 | 覆蓋率 | CRAP | 修改次數 |
|---|---|---|---|---|
| `readBody` | 68 | 94.9% | 68.6 | 6（優先處理） |
| `readNewRoles` | 22 | 82.9% | 24.4 | 0 |
| `readRoles` | 21 | 82.8% | 23.2 | 0 |
| `parseInt` | 15 | 100% | 15.0 | 0 |

把它們拆成每個 CRAP 都不超過 12 的小函式，**每個請求被接受或拒絕的結果、解析出來的值都不變**。第二關只檢查改到的函式，動到其中一個，同一個 PR 裡的其他函式也要過門檻，所以四個一起做。

順帶消掉抓錯檢查留下的 6 個「看起來改了也沒差、但不能寫進排除清單」的改壞版本（body.go 157、360、411 第 18 欄，375、433 第 42 欄，473 第 12 欄）。排除清單只比對檔案、改法和那一行的原文，不看欄位；這幾行同一種改法還蓋到真正的檢查，所以要把兩個條件拆到不同行。

## 不變的行為（design/api.md 第 1 節）

- 只收一個 JSON 物件，大小上限照 `bodyLimit`；物件後面只能有空白。
- 欄位名稱大小寫要完全相符，重複的欄位、不認得的欄位都拒絕；`idList` 和 `optString` 以外的欄位都必填。
- 數字只收純整數：不收 `1.0`、`1e0`、`-0`、前導零，最多 18 位數；不帶正負號的要 ≥ 1。
- `roles` 每項剛好是 `{"roleId":n,"roleNote":"…"|null}`，同一個角色不能出現兩次，最多 50 項。
- `newRoles` 每項剛好是 `{"name":"…","view":"…","checksIn":bool}`，去頭尾空白後名稱不能重複，最多 50 項。
- `excluded` 最多 200 個 id。
- 拒絕時一律回 `body{}, false`（或 `nil, false`、`errBadIDs`），呼叫端只看 `ok`。

## 改法

1. 共用的小函式：
   - `expectDelim(dec, d) bool`：讀一個符號，必須是 `d`。取代 12 處 `err != nil || tok != json.Delim(X)`。
   - `readKey(dec, seen) (string, bool)`：讀物件的一個欄位名稱。「讀不到或不是字串」和「重複」分成兩個 `if`，各在自己的一行。`readBody`、`readRoles`、`readNewRoles` 共用。
2. `readBody` 拆成：開頭 `expectDelim('{')` → 迴圈 `readKey` → `readField(dec, kind, key, &b) bool` → 結尾 `expectDelim('}')` → 檢查後面沒有東西 → `missingRequired(fields, seen) bool`。
   - `readField` 依 `fieldKind` 分派到每種欄位各自的小函式，例如 `readPositiveField`、`readSignedField`、`readCountField`、`readBoolField`、`readTextField`、`readNullableID` 等。
   - 依欄位名稱寫進 `body` 的大 `switch key` 改成「欄位名稱 → 指向 `body` 欄位的指標」的對照，例如 `func intTarget(b *body, key string) *int64`，讓每個函式的分支數降下來。
3. `readRoles`、`readNewRoles`：外層讀陣列，每一項交給 `readRoleItem`、`readNewRoleItem`。項目的結尾檢查拆成兩行：`expectDelim('}')` 一行，「缺欄位或重複」一行。
4. `parseInt`：`digits := raw; if signed { digits = strings.TrimPrefix(raw, "-") }`，不再用 `signed && len(raw) > 0 && raw[0] == '-'`。長度和前導零、只含數字兩項檢查各抽成小函式。
5. 不改任何呼叫端（17 處），不改 `fieldSet`、`fieldKind`、`body` 的定義，不改 design/api.md。

## 排除清單

`.quality-equivalents.json` 現在 7 項都在 body.go，比對的是「那一行的原文」，這次改動後全部對不上，工具會列成「排除清單有 7 項沒用到」。

- 同一個 PR 裡更新排除清單：刪掉對不上的項目，對新程式碼的抓錯檢查結果重新判斷；真的改了也沒差的，逐項列出（檔案、行號、改法、原文、理由），由人核准後才寫進去。
- 依 BeThere 的 CLAUDE.md，AI 不自己改這個檔案；寫法跟 PR #10 一樣，要明確同意。

## 驗收

1. 行為不變：
   - `go test ./...` 全過，既有測試一行都不改；`internal/api/body_test.go`、`internal/server/api_test.go` 的嚴格解析案例（大小寫、重複、`1.0`、`-0`、前導零等）都在裡面。
   - 對照測試：開發時把現在的 `readBody` 複製到測試檔當作舊版，寫 Go fuzz 測試，對同一個輸入和各個 `fieldSet` 比較新舊兩版的 `(body, ok)`，必須完全相同。每組 `fieldSet` 至少跑 10 分鐘，`go test -fuzz` 沒找到差異才算過。舊版的副本和 fuzz 測試不進 main；結果寫在 PR 描述裡。
2. CRAP：`python tools/quality/quality.py crap --base origin/main` 通過，body.go 每個函式都 ≤ 12。
3. 抓錯檢查：對 body.go 跑 `mutation`（module 模式）。沒抓到的改壞版本只能是逐項判斷過、改了也沒差的；新的排除清單套用後，body.go 的抓錯率不低於現在（以排除後計算）。
4. CI 全過。
5. 這是輸入驗證的程式碼：核准前先做 security-reviewer 和 plan-verifier 審查；做完由獨立的 verifier 對第 1～3 項出具 CONFIRMED，才請你合併。

## 非目標

- 不改 API 的行為和錯誤碼，不改其他檔案的超標函式（`checkOrigin`、`run` 等另案處理）。
- 不換掉 `json.Decoder` 逐符號讀取的做法（design/api.md 第 1 節的理由不變）。
- 不重跑 baseline；合併後要更新 quality-baseline.md 時再跑一次。

## 回復

單一 PR，revert 就回到現在的 body.go 和排除清單。

## 停止條件

- 對照測試找到任何差異，而且無法在不改行為的前提下修好：停下來回報。
- 拆完仍有函式超過 12，又沒辦法不改行為再拆：停下來，列出那個函式和原因。
