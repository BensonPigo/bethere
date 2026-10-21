# BeThere 專案指引

## Git

- commit message 與 PR 描述**不要加** `Co-Authored-By:` 或 `Generated with Claude Code` 等署名行。
- commit message 使用英文，格式為 `type: summary`（例如 `docs: ...`）。
- 未經明確要求不要 push。

## 文件

- `docs/` 下的檔名使用英文小寫加連字號，內容使用繁體中文。
- 需求在 `docs/requirements/`，設計在 `docs/design/`，架構視圖在 `docs/architecture/`，架構決策紀錄在 `docs/adr/`，索引在 `docs/README.md`。新增文件時同步更新索引。
- ADR 檔名為 `NNNN-short-title.md`，格式見 `docs/adr/0000-template.md`，並更新 `docs/adr/README.md` 的索引。已接受的 ADR 不改寫內容；推翻時新增一份 ADR，舊的只改狀態。
- 圖一律用 Mermaid 寫在 Markdown 裡。
- 文件之間以檔名互相引用；改檔名時要一併更新引用。
