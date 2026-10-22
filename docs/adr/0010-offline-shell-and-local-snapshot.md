# ADR-0010：離線：Service Worker 只快取外殼、快照存本機；LINE 視為受限環境

- 狀態：已接受（iOS 已實機驗證，Android 待驗證）
- 日期：2026-10-20（實機驗證 2026-10-21）
- 對應：`sync-and-offline.md` S4；`tech-stack.md` T18、T20、T22、R2；需求 E3

## 背景

宴會廳常在地下室，進場瞬間斷網時頁面不能是空白的（E3）。伺服器故障時（ADR-0009），各端也要能繼續看到最後的時程。多數人第一次是從 LINE 點連結，而 LINE 內建瀏覽器有自己的限制。

## 決定

- **外殼**：Service Worker（`vite-plugin-pwa`，Workbox `generateSW`）只預先快取打包產物與三個入口；`/api/` 一律不攔截、不快取。
- **資料**：每次收到快照就整份存入 `localStorage`，只留最近一份；鍵為 `snap:` + Token 的 SHA-256，不存明文 Token。開頁時先用本機快照渲染，再背景輪詢。
- **清除**：只有伺服器明確回「無效」或「已結束」才刪該 Token 的快照；其他錯誤（逾時、5xx、會場 Wi-Fi 登入頁）一律視為離線並保留快照。
- **LINE 內建瀏覽器**視為受限環境：從 User-Agent 的 `Line/` 辨識，畫面引導「以 Safari／Chrome 開啟」。

## 考慮過的選項

| 選項 | 優點 | 缺點 |
| --- | --- | --- |
| 外殼與資料分開快取（採用） | API 回應永遠是最新的；資料依 Token 隔離 | 兩套快取機制 |
| Service Worker 也快取 API 回應 | 一套機制 | 容易回到過期資料；無法依 Token 隔離與清除 |
| 不做離線 | 最簡單 | 違反 E3；地下室與伺服器故障時全場空白 |
| IndexedDB 存快照 | 容量大 | 快照只有數 KB，`localStorage` 同步 API 足夠 |

## 後果

- 好處：斷網、伺服器故障時仍可唯讀顯示，倒數照跑。
- 代價：各瀏覽環境的本機儲存互不共用；iOS Safari 對 7 天沒互動的網站會清掉資料，所以活動前一天要請所有人再開一次（`operations/runbook.md` 第 3 節）。
- 實機結果：iOS 的 Safari 與主畫面模式可離線開啟；LINE 內建瀏覽器**不受 Service Worker 控制、離線打不開**，引導改用 Safari 是必要的（`tech-stack.md` 5.1）。

## 參考

- `sync-and-offline.md` 第 6 節；`access-control.md` 3.3
