# BeThere 專案指引

## Git

- commit message 與 PR 描述**不要加** `Co-Authored-By:` 或 `Generated with Claude Code` 等署名行。
- commit message 使用英文，格式為 `type: summary`（例如 `docs: ...`）。
- 功能分支可以自行 push（讓 CI 執行）；合併進 `main` 與 push `main` 須經明確要求。

## 文件

- `docs/` 下的檔名使用英文小寫加連字號，內容使用繁體中文。
- 開發流程在 `docs/process/development-process.md`；工作依其里程碑與規則進行。強制的審查與核准流程只適用於該文件第 5 節的三個風險邊界（Token 權限與資料外洩、資料不遺失、當天維運），其他工作視為一般工作。
- 產品願景在 `docs/product/`，需求在 `docs/requirements/`，設計在 `docs/design/`，架構總覽在 `docs/architecture/`，維運手冊在 `docs/operations/`，架構決策紀錄在 `docs/adr/`，不再維護的紀錄在 `docs/archive/`，術語與代號在 `docs/glossary.md`，索引在 `docs/README.md`。新增文件時同步更新索引。
- ADR 檔名為 `NNNN-short-title.md`，格式見 `docs/adr/0000-template.md`，並更新 `docs/adr/README.md` 的索引。已接受的 ADR 不改寫內容；推翻時新增一份 ADR，舊的只改狀態。例外：只改代號或檔名引用的機械性更新（ADR-0017）。
- 文件結構與代號依 `docs/adr/0017-documentation-structure-and-id-scheme.md`：每件事只有一個正本，其他地方只引用；新的決定一律寫成 ADR，不在設計文件開決定表；新增代號前綴先登錄在 `docs/glossary.md`。
- 圖一律用 Mermaid 寫在 Markdown 裡。
- 文件之間以檔名互相引用；改檔名時要一併更新引用。
