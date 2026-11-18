# 安全問題回報 (Security Policy)

BeThere 用連結當作身分：拿到連結的人就有那個角色的權限。所以和連結、權限有關的問題，我們都當作安全問題處理。

## 怎麼回報

請**不要**開公開的 issue。到本專案 GitHub 頁面的 **Security** 分頁，按 **Report a vulnerability** 私下回報，並附上：

- 問題是什麼、會造成什麼影響
- 重現的步驟（在自己電腦上執行的版本即可，見 [README](README.md#在自己的電腦上執行)）
- 你測試的版本或 commit

這是個人專案，會盡力在一週內回覆。問題修好並部署後，再公開說明。

## 特別在意的問題

- 拿不到連結的人看到或改到活動資料
- 一個角色做到超出權限的事，例如工作人員改動流程表
- 連結、復原碼、登入信的連結外流到網址列以外的地方（日誌、本機儲存、第三方）
- 已撤銷、已重發或已過期的連結還能使用
- 資料遺失，或從備份還原後出現不該有的權限

## 請不要做的事

- 不要對正式網站做測試、壓力測試或自動掃描；正式網站上是真實婚禮的資料。
- 不要存取、修改或保留不屬於你的資料。

---

**English:** Please do not open a public issue. Report privately via the repository's **Security** tab → **Report a vulnerability**. Test against a local build only, never the production site.
