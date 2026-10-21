# 狀態與領域模型 (Behavior Models) — P0 範圍

> 以 UML 狀態圖與類別圖描述領域行為。欄位定義以 `data-model.md` 為準；這裡補上「誰能在什麼條件下改變狀態」與各類別的職責。

---

## 1. 活動（Event）生命週期

對應：`data-model.md` Event、M9；`access-control.md` 第 2 節；ADR-0011。

```mermaid
stateDiagram-v2
    [*] --> Draft: 開發者 create-event
    Draft --> Draft: 編輯 Rundown（Master）<br/>改活動日期（復原碼，新舊 endsAt 都晚於現在）
    Draft --> Live: 開始活動（Master，僅活動日當天）
    Draft --> Ended: 現在 ≥ endsAt
    Live --> Ended: 現在 ≥ endsAt（活動日隔天 04:00）
    Ended --> Deleted: 每日排程，endsAt 後 80 天
    Deleted --> [*]

    note right of Ended
        不儲存，即時算出
        所有 Token 回「已結束」
        復原碼可唯讀匯出
    end note
    note right of Live
        只有 Master 可異動
        編輯限縮為標題、備註、新增、略過（P8）
    end note
```

## 2. Cue 狀態

對應：`data-model.md` 4.1、M3、M7、M10。

```mermaid
stateDiagram-v2
    [*] --> Pending
    Pending --> InProgress: 推進（填 actualStart）
    InProgress --> Completed: 推進（填 actualEnd）
    Pending --> Skipped: 略過
    InProgress --> Pending: 復原上一步
    Skipped --> Pending: 復原上一步
    Completed --> InProgress: 復原上一步

    note right of InProgress
        同一時間最多一個（activeCueId）
        平移不移動進行中的 Cue
    end note
```

- 主控台的「推進：完成並開始下一個」在同一筆交易內把目前 Cue 改為 Completed、下一個 Pending 改為 InProgress；復原時兩者一起回退（依 V1 按鈕語意推定，`data-model.md` 未明寫）。
- 只有 Pending 且非 Fixed 的 Cue 會被平移（`data-model.md` 4.2）。
- 略過可以用「復原上一步」回到 Pending（`data-model.md` M10，2026-10-21 確認）。

## 3. 角色連結（AccessToken）

對應：`access-control.md` 3.1、3.4；`data-model.md` AccessToken。

```mermaid
stateDiagram-v2
    [*] --> Active: 產生（Master 或 create-event）
    Active --> Revoked: 撤銷，或重發（同時建立新的 Active）
    Active --> EventEnded: 現在 ≥ endsAt
    Revoked --> [*]: 隨活動刪除
    EventEnded --> [*]: 隨活動刪除

    note right of Active
        同一 Role 最多一個 Active
        驗證結果：有效
    end note
    note right of Revoked
        驗證結果：無效
        與「Token 錯誤」不區分
    end note
```

Master 的 Token 只能以復原碼換發，Master 連結本身不能重發 Master（P5）。

## 4. 客戶端連線狀態

對應：`sync-and-offline.md` 第 5、6 節；`access-control.md` 3.3；`screen-design.md` V6。

```mermaid
stateDiagram-v2
    [*] --> Starting: 開啟頁面
    Starting --> Online: 第一次輪詢成功
    Starting --> Offline: 約 10 秒沒有成功回應（有本機快照就先顯示）
    Online --> Offline: 約 10 秒沒有成功回應
    Offline --> Online: 任一次輪詢成功（以伺服器版本覆蓋本機）
    Offline --> OfflineStale: 離線超過 5 分鐘
    OfflineStale --> Online: 輪詢成功
    Online --> Invalid: result = 無效
    Offline --> Invalid: result = 無效
    Online --> Ended: result = 已結束
    Offline --> Ended: result = 已結束
    Invalid --> [*]: 停止輪詢、刪除本機快照
    Ended --> [*]: 刪除本機快照

    note right of Offline
        「離線中，資料更新於 HH:MM」
        總召停用所有寫入
        倒數依最後的時鐘偏差繼續
        維護模式下的非 Master 也在這裡
    end note
    note right of OfflineStale
        追加「倒數可能不準」
    end note
```

只有可解析且 `result` 明確為「無效」或「已結束」的回應會進入終止狀態；HTTP 401、403、5xx、逾時、會場 Wi-Fi 登入頁都只算離線。

## 5. 新秘模式的常亮提示層

對應：`screen-design.md` V2a（✎ 2026-10-21）；`tech-stack.md` 5.1、R3。

```mermaid
stateDiagram-v2
    [*] --> Unsupported: 瀏覽器沒有 Wake Lock
    [*] --> Prompt: 支援 Wake Lock
    Unsupported --> [*]: 一次性提示：調整手機自動鎖定

    Prompt --> Requesting: 點畫面任何位置
    Requesting --> Held: 取得常亮
    Requesting --> Prompt: 被拒（例如 NotAllowedError）
    Held --> Prompt: 系統釋放常亮

    note right of Prompt
        全螢幕「點一下，保持螢幕常亮」
        首次載入時與就位打卡詢問合併
    end note
    note right of Held
        提示層消失，只剩倒數
        iOS 切到別的 App 再回來仍保持
        關掉 App 重開後需再點一次
    end note
```

## 6. 領域類別圖

對應：`data-model.md` 第 2～4 節。只列與行為有關的欄位與操作；方法名稱是設計層級的示意，實作時以 Go 慣例命名。

```mermaid
classDiagram
    direction LR

    class Event {
        +ID id
        +Date eventDate
        +string timezone
        +EventStatus status
        +ID activeCueId
        +string recoveryCodeHash
        +endsAt() Time
        +isEnded(now) bool
        +start(now) error
    }
    class Cue {
        +int seq
        +Time scheduledStart
        +int durationMin
        +bool isFixed
        +int offsetMin
        +CueStatus status
        +effectiveStart() Time
        +effectiveEnd() Time
    }
    class Role {
        +string name
        +RoleType type
        +capabilities() Capability[]
    }
    class AccessToken {
        +string tokenHash
        +Time revokedAt
        +isActive() bool
    }
    class CueRole {
        +string roleNote
        +int leadMin
    }
    class CheckIn {
        +Time at
        +ID byRoleId
        +Time undoneAt
    }
    class ShiftLog {
        +int deltaMin
        +ID fromCueId
        +Time undoneAt
    }
    class ShiftLogEntry {
        +int appliedDeltaMin
    }
    class ActionLog {
        +string actor
        +string type
        +json payload
    }

    class ShiftPlanner {
        <<service>>
        +preview(cues, from, delta, excluded, now) ShiftPlan
        +apply(plan) ShiftLog
        +undo(log) void
    }
    class SnapshotBuilder {
        <<service>>
        +build(event, role, now) Snapshot
    }
    class TokenVerifier {
        <<service>>
        +verify(token, now) Result
    }
    class Result {
        <<enumeration>>
        Invalid
        Ended
        Valid
    }

    Event "1" *-- "many" Cue
    Event "1" *-- "many" Role
    Role "1" *-- "0..1 有效" AccessToken
    Cue "1" -- "many" CueRole
    Role "1" -- "many" CueRole
    Role "1" -- "many" CheckIn
    Event "1" *-- "many" ShiftLog
    ShiftLog "1" *-- "many" ShiftLogEntry
    ShiftLogEntry --> Cue
    Event "1" *-- "many" ActionLog

    ShiftPlanner ..> Cue : 改 offsetMin
    ShiftPlanner ..> ShiftLog : 建立
    SnapshotBuilder ..> CueRole : 依角色過濾
    TokenVerifier ..> AccessToken
    TokenVerifier ..> Event : endsAt
    TokenVerifier ..> Result
```

| 服務 | 規則 | 測試重點 |
| --- | --- | --- |
| ShiftPlanner | `preview` 與 `apply` 用同一套計算；`apply` 是單一交易並使 `eventVersion + 1` | 固定 Cue、排除勾選、進行中不動、負值時排除已逾時並拒絕早於現在、Fixed 衝突只警告、復原精確 |
| SnapshotBuilder | 只含該角色可見的 Cue 與欄位；Master 帶完整 CueRole 與維護旗標 | 每種角色的鍵集合；Host 不含 Cue |
| TokenVerifier | 依序判斷無效 → 已結束 → 有效；儲存層錯誤回錯誤而非「無效」 | 撤銷、過期、時區邊界、資料庫錯誤 |
