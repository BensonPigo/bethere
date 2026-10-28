// The support address (US-O-8.3): shown on the way in and to the Master,
// read from /support.json. serve has checked it; the href is built from it,
// so anything but one plain address still shows nothing.
import { useEffect, useState } from "preact/hooks";

const plain = /^[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}$/;

export function supportEmail(body: unknown): string | null {
  const email = (body as { email?: unknown } | null)?.email;
  return typeof email === "string" && plain.test(email) ? email : null;
}

export function SupportNote() {
  const [email, setEmail] = useState<string | null>(null);
  useEffect(() => {
    fetch("/support.json", { cache: "no-store" })
      .then((r) => (r.ok ? r.json() : null))
      .then((b) => setEmail(supportEmail(b)))
      .catch(() => {});
  }, []);
  if (!email) return null;
  return (
    <p class="support-note">
      使用上有問題？寫信到 <a href={`mailto:${email}`}>{email}</a>
    </p>
  );
}
