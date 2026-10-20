# BeThere

> Real-time wedding day operations and dynamic rundown coordination system.
> 精準掌控現場節奏，讓所有工作人員與廠商在對的時間出現在對的位置。

---

## 核心痛點 (The Problem)

婚宴當天是一場「高度多方依賴、強時間約束、零容錯交付」的實體上線（Go-Live）現場。傳統以紙本、PDF 或通訊軟體傳遞 Rundown，常面臨：
- **時間延誤骨牌效應**：前段梳化延誤 15 分鐘，無法即時通知並連動後續所有廠商與時程。
- **資訊過載與干擾**：廠商需在幾十頁的流程中肉眼尋找與自己相關的節點。
- **總召單點故障 (SPOF)**：所有人遇到問題第一時間找總召，導致通訊堵塞與資訊不對稱。

## 解決方案 (The Solution)

`BeThere` 提供極低門檻、免註冊/免安裝 App 的現場即時協同系統：
1. **Master Console (總召主控台)**：雙欄時間軸設計，支援大按鈕推進環節與全域動態時間平移（+N 分鐘）。
2. **Role-Filtered View (角色過濾視圖)**：透過 Token-based 專屬 URL，廠商（新秘、婚攝、音控、飯店領班）只看與自己相關的倒數與動線。
3. **Real-time Sync (即時狀態廣播)**：後台異動時程或狀態，所有連線終端零延遲自動更新與高亮。
4. **Physical-Friendly Design**：
   - 支援螢幕常亮（Screen Wake Lock API，便於新秘瞄化妝鏡）。
   - 暗色主題（Dark Mode，適配昏暗宴會廳）。
   - 離線快取（Service Worker / LocalStorage，適配弱網與地下室環境）。

---

## 領域模型 (Core Domain Entities)

- **TimelineItem (Cue)**: 流程節點、表定時間、實際時間、狀態（Pending / InProgress / Completed）。
- **Stakeholder (Role)**: 角色名稱、通訊方式、專屬 Access Token。
- **RoleCueMapping**: 角色在特定 Cue 的專屬任務、音軌檔名或拍攝動線備註。
- **EventState**: 全域時間偏移量（`GlobalOffsetMinutes`）、當前 Active Cue。
- **TaskQueue**: 現場機動待辦事項與認領狀態。

---

## 快速開始 (Getting Started)

*(待定：根據選定的技術棧補充本地啟動方式)*

```bash
# Clone the repository
git clone [https://github.com/](https://github.com/)<your-username>/bethere.git
cd bethere