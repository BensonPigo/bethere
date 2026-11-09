# 資料模型 (Data Model)

> 邏輯模型，不綁定資料庫或技術棧。範圍：P0（Rundown、推進 Cue、時間平移、Token 角色視圖、新秘倒數、就位打卡）。
> P1 實體只預留位置，列在最後。

---

## 1. 設計原則

1. **有效時間 = 表定時間 + 該 Cue 的累計偏移**，由伺服器計算後下發，客戶端不自行推算平移。
2. **時間平移是一次「有紀錄的操作」**，記錄影響到哪些 Cue，所以可以精確復原（ADR-0006、ADR-0015）。
3. **伺服器時間為準**（`sync-and-offline.md` 第 4 節），所有時間戳記以 UTC 儲存，顯示時轉為活動時區。
4. **權限跟著 Token 走**，Token 與 Role 分開存放，才能單獨撤銷與重發（`access-control.md` 3.4）。
5. **過濾在伺服器端**（ADR-0007）：每個角色視圖只回傳該角色可見的欄位與 Cue。

## 2. 實體關係

```mermaid
erDiagram
    Event ||--o{ Cue : has
    Event ||--o{ Role : has
    Role ||--o{ AccessToken : "has (一個有效)"
    Cue ||--o{ CueRole : "assigned to"
    Role ||--o{ CueRole : "sees"
    Role ||--o{ CheckIn : "reports"
    Event ||--o{ ActionLog : audits
    Organization ||--o{ Event : owns
    Organization ||--o{ Member : has
    Member |o--o{ LoginMail : "is mailed"
    Organization ||--o{ LoginMail : "counts"
    Member ||--o{ MemberSession : "logs in"
    Member ||--o{ ConsoleToken : opens
    Event ||--o{ ConsoleToken : "as Master"
```

## 3. 實體定義

### Event（活動）
| 欄位 | 型別 | 說明 |
| --- | --- | --- |
| id | ID | |
| name | text | 例：「2027/06/06 婚宴」 |
| eventDate | date | 年份限 2000～2199，Cue 時間同樣限在這個範圍，確保以奈秒儲存與日期運算不會溢位 |
| timezone | text | 預設 `Asia/Taipei`；只接受程式內的時區清單（目前只有 `Asia/Taipei`），確保在任何主機都讀得回來 |
| status | enum | 儲存 `Draft` / `Live`（Live 後只有總召可異動）；`Ended` 不儲存，見下方衍生值 |
| liveStartedAt | timestamp? | 活動開始（活動日當天第一次推進）的時間 |
| activeCueId | ID? | 目前進行中的 Cue。**不儲存**，由狀態為 `InProgress` 的 Cue 推得，避免兩份真相 |
| recoveryCodeHash | text | 復原碼的雜湊（見 `access-control.md` 第 2 節） |
| organizationId | ID? | 所屬的婚顧公司；用邀請碼或指令建立的活動為空（ADR-0039）。建立後不可修改 |

`timezone` 建立後不可修改；`eventDate` 只能以復原碼、在 Draft 階段修改（見 `access-control.md` 第 2 節）。活動只能在 `eventDate` 當天（依 `timezone`）開始：Draft 狀態下的第一次推進在同一筆交易內把活動改為 Live 並開始第一個 Cue；復原這次推進時活動仍維持 Live（ADR-0020）。

**衍生值（不存）**
- `endsAt` = `eventDate` 隔天 04:00（依 `timezone` 換算為 UTC）。
- 現在 ≥ `endsAt` 時，活動視為 `Ended`：所有 Token 回應「已結束」，資料於 `endsAt` 後 80 天刪除，伺服器端所有副本 90 天內消失（ADR-0011）。結束判斷不需要排程器；刪除由伺服器內的每日排程執行（`tech-stack.md` 2.1「資料刪除」）。Live 之後日期不能再改，所以結束與刪除時間在活動開始後就固定。


### Cue（流程節點）
| 欄位 | 型別 | 說明 |
| --- | --- | --- |
| id | ID | |
| eventId | ID | |
| seq | int | 順序，唯一、連續 |
| title | text | 例：「一進預備」 |
| scheduledStart | timestamp | 表定開始時間，**建立後只有編輯 Rundown 才會改，平移不動它** |
| durationMin | int | 預計長度，用來偵測 Fixed 衝突 |
| isFixed | bool | 平移時跳過（4.2） |
| offsetMin | int | 累計偏移（可為負），預設 0 |
| status | enum | `Pending` / `InProgress` / `Completed` / `Skipped` |
| actualStart | timestamp? | 實際開始，供事後檢討 |
| actualEnd | timestamp? | 實際結束 |
| notePublic | text? | 所有被指派角色都可見的說明 |

**衍生值（不存）**
- `effectiveStart = scheduledStart + offsetMin`
- `effectiveEnd = effectiveStart + durationMin`

### Role（角色）
| 欄位 | 型別 | 說明 |
| --- | --- | --- |
| id | ID | |
| eventId | ID | |
| name | text | 例：「新秘」、「婚攝 A」 |
| type | enum | `Master` / `Stylist` / `Media` / `StageAV` / `VenueLead` / `Reception` / `Cashier` / `Runner` / `Host` |
| displayOrder | int | |
| view | enum? | 使用哪種畫面：`HandsFree`（免手持）／`Glance`（快速瞄）／`Dashboard`（平安儀表板）／`Console`（主控台）。未設定時依類型預設：Master → Console、Host → Dashboard、Stylist → HandsFree、其他 → Glance。必須與類型相容：Master 只能 Console、Host 只能 Dashboard，其他類型只能 HandsFree 或 Glance。在建立角色時設定（ADR-0025） |
| checksIn | bool? | 是否需要就位打卡（ADR-0031）。未設定時依類型：Stylist、Media、StageAV 打卡，其他不打卡；只能設為「不打卡」，不能讓 Host 或 Master 打卡。總召在畫面上新增角色時設定，之後不可修改 |

`type` 決定可做的動作與快照內容，建立後不可修改。每個活動只有一個 `Master` 角色，在建立活動時產生，不能新增、刪除。一般角色只能在 Draft 階段刪除，刪除時在同一交易內撤銷其 Token 並移除 CueRole。**一個 Role 是一個「位子」**，同一位子可被多支手機同時使用；兩位攝影師就建兩個 Role。

### AccessToken（存取憑證）
| 欄位 | 型別 | 說明 |
| --- | --- | --- |
| id | ID | |
| roleId | ID | |
| tokenHash | text | 只存雜湊，明文僅在產生時顯示一次 |
| createdAt | timestamp | |
| revokedAt | timestamp? | 撤銷時間（`access-control.md` 3.4） |
| lastSeenAt | timestamp? | 最近一次使用，供總召查看連結是否在使用。**不存資料庫**，只存在伺服器記憶體，重啟後歸零（`tech-stack.md` 2.1「lastSeenAt」） |

規則：每個 Role 同一時間最多一個有效 Token；重發 = 撤銷舊的並建立新的。Token 沒有自己的到期時間，跟著 `Event.endsAt`。

### CueRole（角色與 Cue 的關聯）
| 欄位 | 型別 | 說明 |
| --- | --- | --- |
| cueId, roleId | ID | 複合唯一 |
| roleNote | text? | 該角色專屬的任務備註 |
| leadMin | int? | 預警分鐘數（P1 推播用，P0 先預留） |

**可見性規則**：角色只看得到有 CueRole 的 Cue。`Master` 與 `Host` 為例外（Master 全看；Host 只看儀表板彙總，不看 Cue 細節）。

### CheckIn（就位打卡）
| 欄位 | 型別 | 說明 |
| --- | --- | --- |
| id | ID | |
| roleId | ID | |
| at | timestamp | |
| byRoleId | ID | 本人打卡 = roleId；總召代打 = Master 的 roleId |
| undoneAt | timestamp? | 誤按可取消 |

一個 Role 目前是否就位 = 存在未取消的 CheckIn。

### CueAck（回覆收到）
| 欄位 | 型別 | 說明 |
| --- | --- | --- |
| cueId, roleId | ID | 複合主鍵；Cue 或角色刪除時一起刪除 |
| at | timestamp | 第一次回覆的時間 |

只有 VenueLead 寫入；取消回覆、或該角色被移除這個 Cue 的指派時，刪除該列（`api.md` 9.3）。

### Task（任務）
| 欄位 | 型別 | 說明 |
| --- | --- | --- |
| id | ID | |
| eventId | ID | |
| title | text | 1～100 字 |
| createdByRoleId | ID? | 建立者；角色刪除時為空 |
| createdAt | timestamp | |
| claimedByRoleId | ID? | 認領者；`open` 時為空，角色刪除時為空 |
| state | enum | `open`、`claimed`、`done` |
| updatedAt | timestamp | 最近一次狀態改變 |

id 永不重用（AUTOINCREMENT）。規則見 `api.md` 9.4。

### Guest（賓客）
| 欄位 | 型別 | 說明 |
| --- | --- | --- |
| id | ID | |
| eventId | ID | |
| seq | int | 匯入順序 |
| name | text | 1～50 字 |
| tableLabel | text | 桌號，1～20 字 |
| note | text? | 最多 100 字 |
| arrivedAt | timestamp? | 報到時間；取消報到時清空 |

只有姓名、桌號與備註，隨活動刪除（NFR-5）；id 永不重用。規則見 `api.md` 9.5。

### ChecklistItem（檢核項目）
| 欄位 | 型別 | 說明 |
| --- | --- | --- |
| id | ID | |
| eventId | ID | |
| seq | int | 順序 |
| title | text | 1～100 字 |
| doneAt | timestamp? | |
| doneByRoleId | ID? | 完成者；角色刪除時為空 |

id 永不重用。規則見 `api.md` 9.6。

### Invite（邀請碼）
| 欄位 | 型別 | 說明 |
| --- | --- | --- |
| id | ID | 永不重用；建立活動的操作紀錄以它記下是哪一組 |
| codeHash | text | 邀請碼的雜湊，規則與復原碼相同（`access-control.md` 第 2 節） |
| expiresAt | timestamp | 產生後 30 天 |

不屬於任何活動。建立活動時在同一交易內刪除，過期的由每日刪除清掉，所以表中只有還能用的邀請碼（ADR-0036）。

### 管理員連結與發碼紀錄（ADR-0038）
| 實體 | 欄位 | 說明 |
| --- | --- | --- |
| AdminToken | tokenHash | 管理員連結的雜湊；最多一列，產生新的時刪掉舊的 |
| AdminInvite | at | 系統後台每發一組邀請碼記一列發出時間，只增不刪，計算 24 小時內的上限；不記邀請碼或它的編號 |

都不屬於任何活動，每日刪除不碰它們。

### 婚顧公司（ADR-0039）
| 實體 | 欄位 | 說明 |
| --- | --- | --- |
| Organization | id, name, createdAt | 一家婚顧公司；由系統後台建立。名稱規則同活動名稱 |
| Member | id, organizationId, email, createdAt | 一位成員。`email` 去除前後空白、轉小寫後儲存，全系統唯一（一個 email 只屬於一家公司）；移除時刪除這一列，連同他的 MemberSession 與 ConsoleToken（他的 LoginMail 留著，不再連到他）；id 刪除後不再配發（操作紀錄以它記下是誰） |
| LoginMail | id, memberId, organizationId, tokenHash, createdAt, expiresAt, usedAt | 一次寄登入信的紀錄，寄出或寄信失敗都留著（`api.md` 10.2）。帶登入信連結的雜湊，15 分鐘後失效，用掉時填 `usedAt`。每位成員、每家公司與全系統的寄信上限都數這裡的列（以 `createdAt`、只用下界），所以列保留 24 小時，之後由每日刪除清掉；最多每天 400 列。成員被移除時 `memberId` 設為空（`ON DELETE SET NULL`），連結隨之失效，列留著讓公司與全系統的計數不變；`organizationId` 連到永不刪除的 Organization；從備份還原時只清空 `tokenHash`。不是成員或超過上限的要求什麼都不寫 |
| MemberSession | id, memberId, tokenHash, createdAt, expiresAt, verified | 登入連結；只存雜湊，30 天後失效，登出時刪除；每位成員最多 20 列，再登入時刪掉最舊的。`verified` 為 1 才接受，空的是只需 email 的版本產生的（`api.md` 10.2） |
| ConsoleToken | id, memberId, eventId, tokenHash, createdAt, verified | 成員對某場活動的主控台連結；驗證時解析成那場活動的 Master 角色，操作者記為這位成員（`access-control.md` 第 7 節）。每位成員對每場活動最多 10 列，再開啟時刪掉最舊的；以復原碼換發 Master 連結時刪除這場活動的全部。id 以 AUTOINCREMENT 產生，刪除後不再配發（操作紀錄以它記下是哪一條） |

MemberSession 與 ConsoleToken 以外鍵連到 Member（`ON DELETE CASCADE`），LoginMail 以 `ON DELETE SET NULL` 連到 Member，ConsoleToken 也連到 Event（活動刪除時一併刪除）。Organization、Member、LoginMail、MemberSession 不屬於任何活動，每日刪除不碰它們，只清掉過期或 `verified` 為空的 MemberSession、`verified` 為空的 ConsoleToken，與建立超過 24 小時的 LoginMail。公司與它的活動不會被刪除（活動到結束後 80 天才刪），所以每家公司每天 20 場、系統後台每天 5 家的上限直接以 `Event.createdAt`、`Organization.createdAt` 計算。

### 平移紀錄
不另建表。平移與推進、略過一樣是「Cue 操作」，寫入 ActionLog（`type` 為 `cue.advance`、`cue.skip`、`cue.shift`），`payload` 只記下每個**實際被改到**的 Cue 在操作前後的狀態（`status`、`offsetMin`、`actualStart`、`actualEnd`），不含標題等可編輯文字；平移另記總召的要求（起點、平移量、取消勾選的 Cue），供事後檢討。復原就是把這些 Cue 還原成操作前的狀態；某次平移的量等於前後 `offsetMin` 之差。理由見 `decisions.md`。

### ActionLog（操作紀錄）
`id, eventId, actor, at, type, payload, undoneAt`。Cue 操作（`cue.*`）的 `payload` 含前後狀態，`undoneAt` 在被復原時填入；「復原上一步」找最近一筆 Cue 操作，已復原過就不能再復原（4.1）。`actor` 為 Role、成員（以主控台連結操作時記成員編號與主控台連結的 id，`byTokenId` 為 0；在公司頁面建立活動時只記成員編號）、「復原碼」，或建立活動時的「伺服器」（以邀請碼建立時 `payload` 只記邀請碼的 id）。涵蓋推進、略過、平移、復原、代打卡、Token 重發與撤銷、以復原碼進行的操作。事後檢討與除錯用；活動結束後可憑復原碼唯讀匯出（P1，`requirements/scope.md`）。

## 4. 核心行為規則

### 4.1 狀態轉換

活動的生命週期：

```mermaid
stateDiagram-v2
    [*] --> Draft: 開發者 create-event
    Draft --> Draft: 編輯 Rundown、略過與復原（Master）<br/>改活動日期（復原碼，新舊 endsAt 都晚於現在）
    Draft --> Live: 第一次推進（Master，僅活動日當天）
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
        編輯限縮為標題、備註、新增、略過、
        Pending Cue 的角色指派
    end note
```

Cue 的狀態：

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
        同一時間最多一個（activeCueId 由此推得）
        平移不移動進行中的 Cue
    end note
```

- 同一時間只有一個 `InProgress` 的 Cue（`Event.activeCueId`）。若現場要重疊進行，列為後續需求。
- 推進為 InProgress 時填 `actualStart`；Completed 時填 `actualEnd`。
- 主控台的「推進」在同一筆交易內把目前 Cue 改為 Completed、下一個 Pending 改為 InProgress；復原時兩者一起回退（US-M-3.2、US-M-4.1）。
- 「復原上一步」只能回退最近一次操作，並寫入 ActionLog。略過也可以復原，Cue 回到 `Pending`。

### 4.2 時間平移演算法
輸入：`fromCueId`、`deltaMin`、`excludedCueIds`（總召取消勾選的）。

1. 取得 `seq >= from.seq` 的 Cue。
2. 只保留：`status = Pending`、`isFixed = false`、不在 `excludedCueIds`；`deltaMin < 0` 時，另外排除平移前 `effectiveStart` 就已早於「現在」的 Cue（已逾時）。
3. **負值檢查**（只在 `deltaMin < 0` 時進行）：若任一保留的 Cue 平移後的 `effectiveStart` 早於「現在」，整次操作拒絕並說明是哪個 Cue。正值平移不做這項檢查，因為延誤時平移後仍早於現在是正常情況。
4. 對每個保留的 Cue：`offsetMin += deltaMin`，並在 ActionLog 記下該 Cue 平移前後的狀態。
5. **Fixed 衝突檢查**：平移後，若某 Cue 的 `effectiveEnd` 晚於其後最近一個 Fixed Cue 的 `scheduledStart`，**不阻擋**，但標記衝突並在主控台顯示。
   **時間倒序檢查**：任一 Pending Cue 的 `effectiveStart` 早於 `seq` 在它前面最近一個未略過 Cue 的 `effectiveStart` 時，同樣不阻擋、只標記（取消勾選平移或活動中新增 Cue 都可能造成，ADR-0021）。
6. 整個過程是單一交易，完成後 `eventVersion + 1`，各端在下一次輪詢取得新狀態。

`InProgress` 的 Cue 不被平移（它已經開始了）；若總召想延長目前環節，用平移其下一個 Cue 起算。

從預覽到各端更新的流程（預覽與套用用同一套演算法，ADR-0007）：

```mermaid
sequenceDiagram
    autonumber
    actor M as 總召
    participant C as 總召主控台
    participant S as bethere
    participant D as SQLite
    participant ST as 新秘（免手持畫面）

    M->>C: 點「+10」
    C->>S: POST 平移試算（起點、+10、排除清單）
    S->>D: 讀 Cue（唯讀）
    S->>S: 同一套平移演算法，不寫入
    S-->>C: 受影響 Cue、固定 Cue 衝突、負值檢查結果
    C->>M: 預覽面板（可取消勾選）
    M->>C: 取消勾選一個 Cue
    C->>S: 再次試算
    S-->>C: 新預覽
    M->>C: 確認
    C->>S: POST 平移（version、起點、+10、排除清單）
    S->>D: 單一交易：更新 offsetMin、ActionLog 記下前後狀態、version + 1
    S-->>C: 新快照（含衝突標記）
    Note over ST: 最多 2 秒後的下一次輪詢
    ST->>S: GET 快照
    S-->>ST: 新的 effectiveStart、shiftNotice
    ST->>ST: 倒數重算、背景色重評估、橫幅「時程已更新 +10 分」
```

### 4.3 「全域偏移」、超時與延誤的顯示值
- 各畫面顯示的「+N 分」= **目前 InProgress（或下一個 Pending）Cue 的 `offsetMin`**。
- **超時分鐘** = `max(0, 現在 − 下一個 Pending Cue 的 effectiveStart)`，向下取整。以下一個待辦 Cue 為準，因為進行中的 Cue 不被平移（4.2）；總召平移後續後超時即歸零。超時達 5 分鐘時，主控台提示平移，預填量為超時分鐘以 5 為單位無條件進位（ADR-0022）。
- 新人儀表板的**延誤** = 下一個 Pending Cue 的 `offsetMin` + 超時分鐘，也就是下一個環節比原表晚多少；沒有待辦 Cue 時用上述「+N 分」的值。未滿 5 分鐘顯示「一切順利」（ADR-0026）。
- 超時與延誤由伺服器在快照中算好（ADR-0007）。

### 4.4 角色視圖的資料範圍
| 角色 | 可讀 | 可寫（動作） |
| --- | --- | --- |
| Master | 全部 | 推進、略過、平移、復原、代打卡、重發非 Master 的 Token、編輯 Rundown（Live 時限縮，見 `access-control.md` 第 4 節） |
| Stylist / Media / StageAV | 自己有 CueRole 的 Cue + 該 Cue 的 `roleNote` | 自己的就位打卡 |
| VenueLead | 同上 | 打卡、對自己的 Cue 回覆收到 |
| Runner | 同上 + 未完成的任務 | 打卡、認領、放回、完成任務 |
| Reception | 同上 + 全部賓客 + 自己建立的任務 | 打卡、賓客報到、建立任務（呼叫機動組） |
| Cashier | 同上 + 檢核表 | 打卡、勾選檢核項目 |

### 4.5 領域服務

Cue 操作的實作在 `internal/domain`。只列與行為有關的欄位與操作。

```mermaid
classDiagram
    direction LR

    class Event {
        +ID id
        +Date eventDate
        +string timezone
        +EventStatus status
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
    class ActionLog {
        +string actor
        +string type
        +json payload
        +Time undoneAt
    }
    class Op {
        +OpKind kind
        +bool undone
        +Change[] changes
        +applyTo(cues) Cue[]
    }
    class Change {
        +Cue before
        +Cue after
    }

    class CueRules {
        <<service>>
        +PlanShift(cues, req, now) ShiftPlan
        +Conflicts(cues) Conflict[]
        +Advance(cues, now) Op
        +Skip(cues, id) Op
        +Undo(cues, op) Op
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
    Event "1" *-- "many" ActionLog
    Op "1" *-- "many" Change
    Change --> Cue
    ActionLog ..> Op : payload

    CueRules ..> Cue : 讀取
    CueRules ..> Op : 產生
    SnapshotBuilder ..> CueRole : 依角色過濾
    TokenVerifier ..> AccessToken
    TokenVerifier ..> Event : endsAt
    TokenVerifier ..> Result
```

| 服務 | 規則 | 測試重點 |
| --- | --- | --- |
| CueRules（`internal/domain`） | `PlanShift` 同時供預覽與正式寫入；Op 由 `internal/store` 在單一交易內寫入並使 `eventVersion + 1` | 固定 Cue、排除勾選、進行中不動、負值時排除已逾時並拒絕早於現在、Fixed 衝突只警告、復原精確 |
| SnapshotBuilder | 只含該角色可見的 Cue 與欄位；Master 帶完整 CueRole 與維護旗標 | 每種角色的鍵集合；Host 不含 Cue |
| TokenVerifier | 依序判斷無效 → 已結束 → 有效；儲存層錯誤回錯誤而非「無效」 | 撤銷、過期、時區邊界、資料庫錯誤 |

### 4.6 活動中新增 Cue 與改派角色

Live 階段的規則（ADR-0021）：

- **新增 Cue**：輸入實際要開始的時間、`durationMin`、標題與插入位置（插在哪個 Cue 之後）。輸入的時間存成 `scheduledStart`，`offsetMin = 0`，狀態為 `Pending`；插入位置之後的 Cue `seq` 依序加一，維持唯一、連續。
- **改派角色**：只能對 `Pending` 的 Cue 新增或移除 CueRole（含 `roleNote`）。
- 以上都寫入 ActionLog，`type` 不以 `cue.` 開頭（例如 `rundown.insert`、`rundown.assign`），不算「復原上一步」的步驟（ADR-0015）。
- 新指派的角色在下一次快照收到變更提示（`sync-and-offline.md` 第 2 節）。

## 5. P1 預留（P0 不實作）

| 實體 | 用途 | 備註 |
| --- | --- | --- |
| AssetRef | 音控檔名對照 | 可先放在 `CueRole.roleNote`，需要時再拆 |
| SupportRequest | 求助訊息（只路由給 Master 與 Runner） | 伺服器端過濾的對象（ADR-0007） |
