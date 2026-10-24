// The colour theme (screen-design.md section 2): dark by default, light
// chosen on this device for glare. Only this device's convenience, so it
// lives in local storage and falls back to dark when storage is blocked.
import { useState } from "preact/hooks";

export type Theme = "dark" | "light";
const KEY = "theme";

export function readTheme(store: Storage | null): Theme {
  try {
    return store?.getItem(KEY) === "light" ? "light" : "dark";
  } catch {
    return "dark";
  }
}

export function saveTheme(store: Storage | null, t: Theme): void {
  try {
    store?.setItem(KEY, t);
  } catch {
    /* blocked storage: the choice lasts until the page closes */
  }
}

export function deviceStorage(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

export function applyTheme(t: Theme): void {
  document.documentElement.dataset.theme = t;
}

// ThemeToggle names the theme it switches to.
export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(() => readTheme(deviceStorage()));
  const next: Theme = theme === "dark" ? "light" : "dark";
  return (
    <button
      class="secondary theme-toggle"
      onClick={() => {
        applyTheme(next);
        saveTheme(deviceStorage(), next);
        setTheme(next);
      }}
    >
      {next === "light" ? "☀ 亮色" : "☾ 暗色"}
    </button>
  );
}
