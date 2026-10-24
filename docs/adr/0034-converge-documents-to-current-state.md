# ADR-0034：文件再收斂：只留現況，舊代號表與流程歷史歸檔

- 狀態：提議中
- 日期：2026-10-24
- 對應：`glossary.md`；`process/development-process.md`；`archive/retired-ids.md`、`archive/process-history.md`；ADR-0017、ADR-0033

## 背景

ADR-0033 收斂了範圍，但文件沒有跟著改。2026-10-24 從頭審視整個專案，程式與測試狀況良好（`go vet`、`go test ./...`、`npm test` 全數通過，`api.md` 的端點與程式一一對應），問題集中在文件：

- **彼此矛盾**：`product/vision.md` 說第一版處理賓客，ADR-0033 已暫停；`requirements/scope.md` 把暫停的功能列在 P0（「一定要有」）；`process/development-process.md` 的嚴重度仍用 P0～P2，與 `glossary.md` 的 Sev0～Sev2 衝突；新人儀表板在 `design/access-control.md` 寫「就位率」，在 `scope.md` 寫「刻意不做」。
- **過期**：里程碑表的目標日期（M1「2026-11 中」）早於實際已驗收；仍要求已取消的小型實測；`architecture/overview.md` 的備份 bucket 寫「規劃中」；`screen-design.md` 把已完成的匯入與預覽標為 P1、排序寫成拖曳；文件引用的審查 agent 名稱已不存在。
- **歷史佔篇幅**：`glossary.md` 約 350 行，其中約 290 行是退役代號的對照表；流程文件的第 7～9 節是改版前的紀錄。

## 決定

- 正文一律改成現況；已由 ADR 決定的事，照 ADR 改寫正文，不新增決定。
- `glossary.md` 第 3 節（舊代號對照表）移到 `archive/retired-ids.md`，`glossary.md` 只留術語與代號規則。
- `process/development-process.md` 第 7～9 節移到 `archive/process-history.md`；里程碑表改為各里程碑的狀態或目標日期。
- 暫停的功能在 `scope.md` 另列一節，不再放在 P0。
- 已驗收的 `stories/m1.md`～`m2c.md` 保留原樣，作為驗收紀錄；測試名稱與程式碼仍引用其中的代號。
- 設計文件不放審查經過的狀態橫幅，經過見 git 紀錄。

## 考慮過的選項

| 選項 | 優點 | 缺點 |
| --- | --- | --- |
| 改成現況並把歷史歸檔（採用） | 每份文件只說現在的事；篇幅變小 | 要查舊代號多開一份歸檔 |
| 只修矛盾，不搬動 | 改動最少 | 歷史仍佔一半篇幅，下次又會過期 |
| 已驗收的故事檔也歸檔 | 文件更少 | 測試名稱與程式碼註解約上百處引用要改，收益小 |

## 後果

- 好處：PO 讀文件時不會先讀到過時或互相矛盾的內容；`glossary.md` 縮到一頁。
- 代價：ADR-0017 的目錄說明中「`glossary.md` 含舊代號對照表」不再成立，以本 ADR 為準。
- 之後要注意：範圍或流程的決定改變時，同一個 commit 內更新 `vision.md`、`scope.md`、`development-process.md` 中受影響的段落。

## 參考

- `archive/retired-ids.md`
- `archive/process-history.md`
