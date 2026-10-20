# BeThere 專案指引

## Git

- commit message 與 PR 描述**不要加** `Co-Authored-By:` 或 `Generated with Claude Code` 等署名行。
- commit message 使用英文，格式為 `type: summary`（例如 `docs: ...`）。
- 未經明確要求不要 push。

## 文件

- `docs/` 下的檔名使用英文小寫加連字號，內容使用繁體中文。
- 需求在 `docs/requirements/`，設計在 `docs/design/`，索引在 `docs/README.md`。新增文件時同步更新索引。
- 文件之間以檔名互相引用；改檔名時要一併更新引用。
