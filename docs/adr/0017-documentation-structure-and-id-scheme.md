# ADR-0017：文件結構與代號系統

- 狀態：已接受
- 日期：2026-10-22
- 對應：`glossary.md`；`process/development-process.md` 第 8 節

## 背景

到 M1 驗收為止，`docs/` 有 33 份文件、約 3,200 行。整理時發現：

- **同一個決定寫在四到五處**。例如「每 2 秒輪詢、3 秒內更新」同時出現在 clarification E2、sync-and-offline S1、architecture-overview Q1 與 K2、ADR-0004、m1-acceptance。改一處，其他處就過期。
- **代號有 13 組前綴，其中 5 組撞名**：P（MVP 層級／嚴重度／權限決定）、M（里程碑／資料模型決定）、V（產品原則／畫面）、A（需求釐清／API 決定）、Q（品質目標／M1 的 PO 決定）。沒有登錄表；程式碼註解另外引用了約 140 次，分布在 35 個檔案。
- **決定紀錄有 8 種格式**：clarification 的 A～F、設計文件的 M、P、S、U、T、`api.md` 的 A、`tech-stack.md` 的 T-A～F、流程的 D、m1-acceptance 的 Q，另外還有 ADR。
- **歷史與現況混在一起**：正文有 38 個 ✎ 修訂標記；已出現過期內容（`screen-design.md` 仍寫新人儀表板屬 P1）。
- **`architecture/` 已凍結**（`process/development-process.md` 第 8 節），約 750 行會持續與設計脫節。

## 決定

全面整合文件，依以下規則進行，分六個步驟完成（第「執行」節）。

### 1. 每件事只有一個正本

| 內容 | 正本 |
| --- | --- |
| 規則、欄位、數字（是什麼） | `design/` |
| 取捨與理由（為什麼） | `adr/` |
| 要做什麼、做到什麼程度 | `requirements/` |
| 怎麼做（流程） | `process/` |
| 怎麼維運 | `operations/` |

其他地方只以「檔名＋章節」引用，不複述。

### 2. 代號系統

只有會被跨文件引用的東西才編號，其餘用名稱稱呼（例如「總召主控台」而不是 V1）。整合後只保留以下代號：

| 代號 | 意思 | 正本 |
| --- | --- | --- |
| `M1`、`M2a`… | 里程碑 | `process/development-process.md` |
| `P0`、`P1`、`P2` | 功能優先層級，只用於範圍 | `requirements/scope.md` |
| `Sev0`、`Sev1`、`Sev2` | 問題嚴重度 | `process/development-process.md` |
| `V1`～`V5` | 產品原則 | `product/vision.md` |
| `US-<角色>-<n>.<m>`、`.C<m>` | 故事與驗收條件（功能條件／情境條件） | `requirements/stories/` |
| `NFR-<n>` | 非功能需求與品質目標 | `requirements/scope.md` |
| `ADR-<NNNN>` | **唯一的決定編號** | `adr/` |
| `RISK-<n>` | 風險與待驗證項目 | `docs/README.md` 狀態節 |

其餘前綴（clarification 的 A～F、D1、D2，data-model 的 M，access-control 的 P，sync-and-offline 的 S，screen-design 的 V 與 U，tech-stack 的 T、T-x、R，api 的 A，architecture-overview 的 Q、K，m1-acceptance 的 Q，流程的 D-x）**全部退役**。每一條逐條歸位為以下其中一種，並在 `glossary.md` 的對照表記錄去向：

- **規則**：寫進對應設計文件的正文。
- **ADR**：有取捨、值得留下理由的，新增 ADR。
- **需求**：寫進 `requirements/`（NFR-n、範圍）。
- **風險**：寫進 RISK-n。
- **刪除**：已過時或已被其他項目涵蓋。

新增任何代號前綴，須先在 `glossary.md` 登錄。

### 3. 目錄結構

```
docs/
├── README.md                 索引、目前狀態、風險清單（RISK-n）
├── glossary.md               術語、代號規則、舊代號對照表
├── product/vision.md
├── requirements/
│   ├── roles-and-scenarios.md  角色與現場情境；P1、P2 角色的故事作為待辦保留
│   ├── scope.md                功能 × 優先層級 × 里程碑；NFR-n
│   └── stories/<里程碑>.md      各里程碑的故事與驗收條件
├── design/                   只寫現況；狀態圖、循序圖放在它說明的規則旁
├── architecture/overview.md  一頁全貌：系統情境、容器、部署
├── operations/runbook.md     部署、回退、故障重建、當天維運
├── process/development-process.md
├── adr/
└── archive/                  不再維護的原始紀錄（例如需求釐清問答）
```

`architecture/runtime-views.md` 與 `behavior-models.md` 的圖搬進對應的設計文件，兩份檔案移除。

### 4. 只寫現況

正文移除 ✎ 標記與修訂橫幅；變更經過以 git 紀錄與 ADR 保存。

### 5. ADR 的引用更新

已接受的 ADR 仍不改寫決定內容。例外：本次整合時，ADR 中的**舊代號與檔名引用**一次性改為新的代號與位置，這是機械性更新，不改變任何決定。之後的檔名或代號變更，也適用同樣的例外。

### 6. 機器檢查

CI 加一支檢查腳本：文件中引用的檔名必須存在；使用的代號前綴必須已在 `glossary.md` 登錄。

## 執行

| 步驟 | 內容 | 完成條件 |
| --- | --- | --- |
| 1 | 本 ADR、`glossary.md`（含舊代號完整清單）、`CLAUDE.md` 規則 | PO 核准 |
| 2 | 舊代號逐條歸位 | PO 逐條審閱；對照表沒有「待定」 |
| 3 | 重組目錄、合併與拆分文件、搬移圖 | 所有檔名引用有效 |
| 4 | 移除歷史標記、修正過期內容 | 抽查 |
| 5 | 文件與程式碼註解改用新代號 | `go vet`、`go test`、`npm test` 通過 |
| 6 | CI 檢查腳本 | 故意寫錯一個引用時 CI 失敗 |

整合期間新舊代號並存；以對照表為準。

## 考慮過的選項

| 選項 | 優點 | 缺點 |
| --- | --- | --- |
| 全面整合：單一正本、縮減代號、重組目錄（採用） | 移除「同一決定多處複述」的源頭；代號不再撞名 | 一次性工作量大；舊 commit 與討論中的代號要靠對照表查 |
| 只加代號對照表 | 工作量最小，不動任何文件 | 撞名與多處複述仍在，文件會繼續過期 |
| 只把撞名的前綴改名 | 解決撞名 | 8 種決定格式與多處複述仍在 |

## 後果

- 好處：查一件事只有一個地方；代號看了就知道去哪找；過期內容由 CI 擋下一部分。
- 代價：154 個舊代號要逐條歸位；約 140 處程式碼註解要更新；ADR 的引用要改寫一次。
- 之後要注意：
  - 新的決定一律寫成 ADR，不再在設計文件中開決定表。
  - 撤銷 `process/development-process.md` 第 8 節「`architecture/` 與 ADR 凍結」中關於 `architecture/` 的部分：整合後只剩一頁總覽，隨設計更新。

## 參考

- `glossary.md`
- `process/development-process.md` 第 8、9 節
