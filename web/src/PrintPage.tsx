// Print-only page standing in for the backup rundown (tech-stack.md 2.2「備援 Rundown」).

const SAMPLE = [
  ["09:30", "新秘到場", "新秘、總召"],
  ["10:00", "一進造型", "新秘"],
  ["11:25", "一進預備", "新秘、攝影、音控"],
  ["12:00", "開席（固定）", "全體"],
];

export function PrintPage() {
  const canPrint = typeof window.print === "function";
  return (
    <main class="print">
      <div class="no-print">
        <a href={`/${location.hash}`}>← 回驗證頁</a>
        <button onClick={() => window.print()} disabled={!canPrint}>列印／另存 PDF</button>
        <p class="hint">按下後若沒有出現列印畫面，就是這個環境不支援列印。</p>
      </div>
      <h1>備援 Rundown（範例）</h1>
      <p>匯出於 {new Date().toLocaleString()}・版本 0</p>
      <table>
        <thead><tr><th>時間</th><th>環節</th><th>角色</th></tr></thead>
        <tbody>
          {SAMPLE.map(([t, title, roles]) => (
            <tr key={t}><td>{t}</td><td>{title}</td><td>{roles}</td></tr>
          ))}
        </tbody>
      </table>
    </main>
  );
}
