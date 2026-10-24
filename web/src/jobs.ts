// Pure helpers of the four jobs' panels (api.md section 9).
import type { CheckItem, Guest, Task } from "./api";

export const MAX_GUEST_RESULTS = 30;

// searchGuests matches any part of a name or table, one character is
// enough (US-RC-1.6); Latin letters ignore case. Empty input lists nobody.
export function searchGuests(guests: Guest[], query: string): Guest[] {
  const q = query.trim().toLowerCase();
  if (q === "") return [];
  return guests
    .filter((g) => g.name.toLowerCase().includes(q) || g.table.toLowerCase().includes(q))
    .slice(0, MAX_GUEST_RESULTS);
}

// tableText adds 桌 to a table number only: 主桌 or Head Table stay as written.
export function tableText(label: string): string {
  return /^\d+$/.test(label) ? `${label} 桌` : label;
}

export function arrivedCount(guests: Guest[]): { arrived: number; total: number } {
  return { arrived: guests.filter((g) => g.arrived).length, total: guests.length };
}

export function doneCount(items: CheckItem[]): { done: number; total: number } {
  return { done: items.filter((i) => i.done).length, total: items.length };
}

export const TASK_STATE_TEXT: Record<Task["state"], string> = { open: "待認領", claimed: "處理中", done: "完成" };

// The text a reception's "call a runner" button sends (US-RC-2.1).
export function callRunnerTitle(roleName: string): string {
  return `${roleName}需要機動組支援`.slice(0, 100);
}
