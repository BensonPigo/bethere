# BeThere 專案指引

## Git

- commit message 與 PR 描述**不要加** `Co-Authored-By:` 或 `Generated with Claude Code` 等署名行。
- commit message 使用英文，格式為 `type: summary`（例如 `docs: ...`）。
- 功能分支可以自行 push（讓 CI 執行）；合併進 `main` 與 push `main` 須經明確要求。

## 文件

- `docs/` 下的檔名使用英文小寫加連字號，內容使用繁體中文。
- 開發流程在 `docs/process/development-process.md`；工作依其里程碑與規則進行。強制的審查與核准流程只適用於該文件第 5 節的三個風險邊界（Token 權限與資料外洩、資料不遺失、當天維運），其他工作視為一般工作。
- 產品願景在 `docs/product/`，需求在 `docs/requirements/`（故事與驗收條件都在 `stories.md`），設計在 `docs/design/`，架構總覽在 `docs/architecture/`，維運手冊在 `docs/operations/`，現行決定在 `docs/decisions.md`，術語與代號在 `docs/glossary.md`，索引在 `docs/README.md`。新增文件時同步更新索引。
- **文件只寫現在是什麼**：不寫修訂經過、日期、審查紀錄、「某版追加」「原為」這類歷程，也不保留歸檔；歷程只在 git 紀錄。內容改變時直接改寫正文。
- 決定寫在 `docs/decisions.md`，編號 `ADR-NNNN`，每條只寫目前的決定與一句理由；決定改變時直接改寫該條，新的決定接在最後，編號不重複使用。不在設計文件開決定表。
- 每件事只有一個正本，其他地方只引用（ADR-0017）；新增代號前綴先登錄在 `docs/glossary.md`。引用的 `.md` 檔是否存在由 `internal/docscheck` 的測試在 CI 檢查（`go test ./...`）。
- 圖一律用 Mermaid 寫在 Markdown 裡。
- 文件之間以檔名互相引用；改檔名時要一併更新引用。

## 品質規則（三道檢查）

改完程式後，執行下面這一行，通過才算完成。它依 `.referee.json` 的 `gates` 設定，依序跑已啟用的關，第一個沒過就停：

```bash
python tools/referee/referee.py check --base origin/main --skip mutation
```

- 第一關：單元測試。
- 第二關：CRAP，改到的函式不能超過門檻。超標的函式後面會標改善方向。
- 第三關：抓錯檢查，改到的行，抓錯率和測到率都要達到門檻。這一關太慢，由 CI 執行，不在上面這一行裡。要在本機跑時，執行 `python tools/referee/referee.py mutation --base origin/main`。

規則：

- 不准修改 `.referee.json`、`.referee-equivalents.json`、`.gitmodules`、`CODEOWNERS`，以及 `tools/referee/`、`.gitea/`、`.github/`、`.claude/` 底下的檔案，也不准切換 `tools/referee` 的版本。
- 不准刪除測試，也不准把測試註解掉或標成略過。
- 測試的正確答案要照需求規格寫死，不准呼叫正式程式碼算出來。
- 測試失敗時，修正程式，不要改測試去配合程式。
- CRAP 超標時，照工具標的方向處理：標「要拆函式」就拆函式，標「覆蓋率要到 X%」就補測試。
- 抓錯檢查有「沒抓到」的改壞版本時，先補測試，把它抓出來。輸出的每一行寫著檔案、行號和改了什麼，例如「把 `>=` 改成 `>`」，表示這個邊界值沒有測到。
- 抓錯檢查有「測試沒跑到」的改壞版本時，表示那一行沒有測試，補測試。
- 認為某個改壞的版本改了也沒差（怎麼改，程式的結果都一樣），不要為它寫奇怪的測試，也不准自己編輯 `.referee-equivalents.json`。在回報裡逐項列出：檔案、行號、改法（`把 X 改成 Y`，或 `--json` 報告裡 `mutator` 欄位的名稱）、那一行的原文、為什麼改了也沒差。由人判斷後寫進排除清單。
- 過不了的時候，說明卡在哪裡、試過什麼，等人處理，不要想辦法繞過檢查。
