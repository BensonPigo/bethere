# 執行期視圖 (Runtime Views) — P0 範圍

> 以 UML 循序圖描述關鍵情境。規則細節以 `docs/design/` 為準；元件名稱見 `architecture-overview.md` 第 5 節。
> 圖中「快照」指該角色可見的完整狀態（`sync-and-offline.md` 第 2 節）。

---

## 1. 開啟連結與輪詢

對應：`access-control.md` 3.2、3.3；`sync-and-offline.md` 第 1、5、6 節；ADR-0002、ADR-0004、ADR-0010。

```mermaid
sequenceDiagram
    autonumber
    actor U as 角色工作人員
    participant B as 瀏覽器（SPA）
    participant L as localStorage
    participant S as bethere
    participant D as SQLite

    U->>B: 點 LINE 裡的連結（Token 在網址片段）
    Note over B: 網址片段不會送到伺服器
    B->>B: 從網址讀 Token，計算 SHA-256
    B->>L: 讀取 snap:雜湊
    alt 有本機快照
        L-->>B: 最後一份快照
        B->>U: 立刻渲染（標示資料時間）
    end
    loop 每 2 秒（上一次未完成就跳過，逾時 8 秒）
        B->>S: GET 快照（Token 在標頭）
        S->>D: 以 Token 雜湊查詢（唯讀連線池，3 秒逾時）
        alt 查無有效 Token
            S-->>B: result = 無效
            B->>L: 刪除該 Token 的快照
            B->>U: 「連結已失效，請向總召索取新連結」，停止輪詢
        else 已過 endsAt
            S-->>B: result = 已結束（不附活動資料）
            B->>L: 刪除該 Token 的快照
            B->>U: 「活動已結束，感謝你的協助」
        else 有效
            S->>S: 更新 lastSeen（記憶體）
            S->>D: 依角色組裝快照
            S-->>B: result = 有效、快照、serverTime
            B->>B: 更新時鐘偏差（中位數）
            B->>L: 覆寫 snap:雜湊
            B->>U: 重繪
        else 逾時、5xx、Wi-Fi 登入頁、無法解析
            B->>U: 超過約 10 秒沒有成功回應即顯示「離線中」，保留快照、倒數照跑
        end
    end
```

## 2. 總召推進與版本衝突

對應：`sync-and-offline.md` 第 3 節；`data-model.md` 4.1；ADR-0005。

```mermaid
sequenceDiagram
    autonumber
    actor M1 as 總召（平板）
    actor M2 as 總召（手機）
    participant S as bethere
    participant D as SQLite

    Note over M1,M2: 兩台都看到 eventVersion = 41
    M1->>S: POST 推進（version = 41）
    S->>S: 驗 Token、capability = 推進
    S->>D: 交易：比對版本、改 Cue 狀態、寫 ActionLog、version = 42
    S-->>M1: 成功 + 新快照（version 42）
    M2->>S: POST 平移 +10（version = 41）
    S->>D: 交易：比對版本
    D-->>S: 目前版本 42，不符
    S-->>M2: 拒絕 + 最新快照（version 42）
    M2->>M2: 提示「畫面已更新，請重新確認」並刷新
    Note over M2: 總召看過最新狀態後再決定是否平移
```

## 3. 時間平移：預覽、套用、各端更新

對應：`data-model.md` 4.2；`screen-design.md` 3.3、4.4；ADR-0006、ADR-0007。

```mermaid
sequenceDiagram
    autonumber
    actor M as 總召
    participant V1 as 主控台 V1
    participant S as bethere
    participant D as SQLite
    participant V2 as 新秘 V2a

    M->>V1: 點「+10」
    V1->>S: POST 平移試算（起點、+10、排除清單）
    S->>D: 讀 Cue（唯讀）
    S->>S: 同一套平移演算法，不寫入
    S-->>V1: 受影響 Cue、固定 Cue 衝突、負值檢查結果
    V1->>M: 預覽面板（可取消勾選）
    M->>V1: 取消勾選一個 Cue
    V1->>S: 再次試算
    S-->>V1: 新預覽
    M->>V1: 確認
    V1->>S: POST 平移（version、起點、+10、排除清單）
    S->>D: 單一交易：更新 offsetMin、ActionLog 記下前後狀態、version + 1
    S-->>V1: 新快照（含衝突標記）
    Note over V2: 最多 2 秒後的下一次輪詢
    V2->>S: GET 快照
    S-->>V2: 新的 effectiveStart、shiftNotice
    V2->>V2: 倒數重算、背景色重評估、橫幅「時程已更新 +10 分」
```

## 4. 撤銷與重發角色連結

對應：`access-control.md` 3.4；`screen-design.md` V4。

```mermaid
sequenceDiagram
    autonumber
    actor M as 總召
    participant S as bethere
    participant D as SQLite
    participant Old as 持舊連結的裝置
    actor R as 該角色

    M->>S: POST 重發（角色）
    S->>S: 驗 Master、目標不是 Master
    S->>D: 交易：舊 Token 填 revokedAt、建立新 Token 雜湊、寫 ActionLog
    S-->>M: 新連結與 QR Code（PNG data URI，明文只出現這一次）
    Old->>S: 下一次輪詢（舊 Token）
    S-->>Old: result = 無效
    Old->>Old: 刪除該 Token 的快照，顯示「連結已失效」
    M-->>R: 以 LINE 傳新連結（系統不代發）
```

## 5. 以復原碼換發 Master 連結

對應：`access-control.md` 第 2 節；ADR-0003。

```mermaid
sequenceDiagram
    autonumber
    actor M as 當天總召
    participant B as /recover 頁面
    participant S as bethere
    participant D as SQLite

    M->>B: 開啟 /recover（不需 Token）
    M->>B: 從紙本輸入復原碼
    B->>S: POST 換發 Master（復原碼只在本文）
    S->>S: 正規化（忽略空白、連字號與大小寫）後計算雜湊
    S->>D: 以復原碼雜湊查詢活動
    alt 不符
        S-->>B: 失敗（no-store）
    else 符合
        S->>D: 交易：舊 Master Token 撤銷、建立新的、寫 ActionLog（actor = 復原碼）
        S-->>B: 新 Master 連結（no-store）
        Note over M: 外流情況下，到 V4 檢查並重發可疑的角色連結
    end
```

## 6. 部署與回退

對應：`tech-stack.md` 第 3 節「服務啟動」「部署與回退」；`ops/gcp/`。

```mermaid
sequenceDiagram
    autonumber
    actor Dev as 開發者
    participant Mk as make release
    participant Rel as GCS 發行版 bucket
    participant Meta as VM metadata
    participant VM as VM（systemd）
    participant Prep as prepare.sh

    Dev->>Mk: ops/gcp/release.sh
    Mk->>Mk: 建前端、跑測試、交叉編譯 linux/amd64
    Mk->>Rel: 上傳 bethere/版本/bethere（--no-clobber，不覆寫）
    Dev->>Meta: ops/gcp/deploy.sh 版本 → 設定 bethere-version
    Dev->>VM: 經 IAP ssh：systemctl restart bethere
    VM->>Prep: ExecStartPre
    Prep->>Meta: 讀版本號（讀不到就失敗，不啟動）
    alt 與已安裝版本相同
        Prep->>Prep: 跳過下載（GCS 不通也能啟動）
    else 不同
        Prep->>Rel: 下載到 .new
        Prep->>Prep: 原子替換，寫入已安裝版本標記
    end
    Prep-->>VM: 成功
    VM->>VM: 啟動 bethere serve
    Dev->>VM: GET /api/ping 確認 version
    Note over Dev,VM: 回退 = 以上一版的版本號再跑一次 deploy.sh
```

## 7. VM 故障重建與維護模式

對應：`tech-stack.md` 3.1 程序 2（要求草案）；ADR-0012、ADR-0013。正式版才有 Litestream 與維護模式，原型尚未實作。

```mermaid
sequenceDiagram
    autonumber
    actor Op as 備援操作者
    participant GCE as Compute Engine
    participant VM as 新 VM
    participant Bak as GCS 備份 bucket
    actor M as 總召
    participant All as 其他角色裝置

    Note over All: 伺服器中斷：離線快取唯讀顯示、倒數照跑
    Op->>GCE: 刪除舊 VM（保留舊磁碟），確認一台都不剩
    Op->>GCE: 建立新 VM：新空磁碟、同靜態 IP、目前版本號、maintenance = 1
    GCE->>VM: 開機
    VM->>Bak: litestream restore（失敗就不啟動）
    VM->>VM: 找不到資料庫就拒絕啟動（不建空庫）
    VM->>VM: bethere serve（維護模式）
    Op->>VM: 檢查：恰好 1 台、新磁碟、/healthz、HTTPS 憑證
    All->>VM: 輪詢
    VM-->>All: 伺服器錯誤（視為離線，保留快照）
    M->>VM: 以 Master 連結輪詢
    VM-->>M: 快照 + 維護旗標
    M->>M: 看到「維護中」橫幅，核對 Rundown 與 Cue 狀態
    alt 資料略舊
        M->>VM: 在維護模式下補登
    else 資料明顯錯誤或是空的
        Note over Op,All: 不解除維護，當天改靠離線快取與紙本
    end
    Op->>GCE: maintenance = 0，重啟服務
    All->>VM: 下一次輪詢
    VM-->>All: 有效快照，自動恢復
```
