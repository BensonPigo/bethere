# ADR-0021：活動中可新增 Cue 並指派角色，被指派的角色會收到變更提示

- 狀態：已接受
- 日期：2026-10-22
- 對應：`design/access-control.md` 第 4 節；`design/data-model.md` 4.2、4.6；`design/sync-and-offline.md` 第 2 節；`design/screen-design.md` 3.3、4.4、6；`product/vision.md` 第 3 節；ADR-0015

## 背景

Live 階段原本允許新增 Cue，但不允許改角色指派（「現場要改由總召口頭通知」）。結果是現場新增的 Cue 永遠沒有指派任何角色，只有總召看得到，與核心價值「變動自動傳到每個人」衝突。新 Cue 的 `seq` 插入位置、`scheduledStart` 與 `offsetMin` 怎麼給，也沒有定義。

另外，平移時取消勾選某些 Cue，可能讓 `effectiveStart` 的先後與 `seq` 的順序不一致，目前只檢查固定 Cue 的衝突，不會發現這種倒序。

## 決定

- **Live 階段可以替 `Pending` 的 Cue 指派或移除角色**；進行中、已完成、已略過的 Cue 不行。
- **新增 Cue**：總召輸入實際要開始的時間、長度、標題，並選擇插在哪個 Cue 之後。系統把輸入的時間存成 `scheduledStart`，`offsetMin = 0`，在插入位置之後的 Cue `seq` 依序加一。
- 指派、移除與新增都寫入 ActionLog，`type` **不以 `cue.` 開頭**（例如 `rundown.insert`、`rundown.assign`），所以不算「復原上一步」的步驟；指派錯了就再改一次。ADR-0015 的 Op 與 payload 版本不受影響。
- **變更提示**：快照的 `shiftNotice` 擴充為通用的變更提示，除了平移，也涵蓋「新指派給你的 Cue」。被指派的角色畫面上方出現短暫橫幅「新增環節：〈標題〉」。
- **倒序警告**：任一 `Pending` Cue 的 `effectiveStart` 早於 `seq` 在它前面的 Cue 時，主控台在該 Cue 旁顯示警告，不阻擋。

## 考慮過的選項

| 選項 | 優點 | 缺點 |
| --- | --- | --- |
| 可指派 Pending 的 Cue、指派不算復原步驟（採用） | 現場變動能送到相關的人；不動 ADR-0015 的復原機制 | 指派錯誤要手動改回；多一種提示 |
| 維持 Live 不能改指派 | 規則最少 | 新增的 Cue 對廠商不可見，違反核心價值 |
| Live 不能新增 Cue | 不會有不可見的 Cue | 現場臨時加環節只能靠口頭；與「現場可新增、編輯 Cue」的範圍不符（`requirements/scope.md`） |
| 指派也算復原步驟 | 誤按可一鍵復原 | 要改 ADR-0015 的 Op 結構與 payload 版本；舊版回退後看不懂新步驟 |

## 後果

- 好處：現場的新增與改派會自動出現在被指派者的畫面上。
- 代價：`seq` 在 Live 階段會變動；變更提示要能分辨「平移」與「新指派」兩種內容。
- 之後要注意：
  - 復原檢查只比對 Op 擁有的欄位（ADR-0015），不含 `seq`，所以插入 Cue 不會讓之前的平移無法復原；實作時以測試確認。
  - 撤銷指派時，被移除的角色不會收到提示，Cue 從他的清單消失；需要時由總召口頭通知。

## 參考

- `design/access-control.md` 第 4 節（Live 階段的有限編輯）
- ADR-0007、ADR-0015
