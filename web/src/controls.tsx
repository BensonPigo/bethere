// Small controls shared by the Master's dialogs: the event-day lock, the
// once-shown link and the 24-hour time field.
import { useState } from "preact/hooks";
import type { NewLink } from "./session";

// dayStarted: from 00:00 of the event day on, replacing the rundown and
// deleting a role are locked (ADR-0032).
export function dayStarted(eventDate: string, today: string): boolean {
  return today >= eventDate.slice(0, 10);
}

// The link shown once after it is made: text and copy, with its QR code
// for a phone held next to the tablet; never a link that opens here
// (api.md section 3).
export function LinkResult({ link }: { link: NewLink }) {
  const url = `${location.origin}/#t=${link.token}`;
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  };
  return (
    <div class="link-result">
      <div>新連結（只顯示這一次，請用 LINE 傳給對方{link.qr ? "，或讓對方掃描" : ""}）：</div>
      <input class="link-text" readOnly value={url} onFocus={(e) => (e.currentTarget as HTMLInputElement).select()} />
      <button class="confirm" onClick={() => void copy()}>
        {copied ? "已複製 ✔" : "複製"}
      </button>
      {link.qr && <img class="qr" src={link.qr} alt="連結的 QR Code" width={220} height={220} />}
    </div>
  );
}


// ClockInput is a plain 24-hour time field: the same on every browser,
// with the number pad on a phone or tablet (see parseClock).
export function ClockInput(props: { value: string; disabled?: boolean; onValue: (v: string) => void }) {
  const set = (e: Event) => props.onValue((e.currentTarget as HTMLInputElement).value);
  return (
    <input
      class="time"
      type="text"
      inputMode="numeric"
      autoComplete="off"
      aria-label="開始時間"
      placeholder="09:20"
      maxLength={5}
      value={props.value}
      disabled={props.disabled}
      onInput={set}
      onChange={set}
    />
  );
}
