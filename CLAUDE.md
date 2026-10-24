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
