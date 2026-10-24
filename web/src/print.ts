// Rows of the backup rundown (sync-and-offline.md 9; ADR-0014): pure, so the
// content is tested apart from the page.
import { isHostSnapshot, isMasterSnapshot } from "./api";
import type { Snapshot } from "./api";

export type PrintRow = {
  id: string | number;
  start: string; // effectiveStart, the time in force when printed
  durationMin: number;
  title: string;
  isFixed: boolean;
  done: boolean; // completed or skipped
  notePublic: string | null;
  roles: { name: string; note: string | null }[];
};

// For the Master, every cue, or only those assigned to roleId; for another
// role, its own cues; the hosts' snapshot has no cues to print.
export function printRows(snap: Snapshot, roleId: string | number | null = null): PrintRow[] {
  if (isHostSnapshot(snap)) return [];
  if (isMasterSnapshot(snap)) {
    return [...snap.cues]
      .sort((a, b) => a.seq - b.seq)
      .filter((c) => roleId === null || c.roles.some((r) => String(r.roleId) === String(roleId)))
      .map((c) => ({
        id: c.id,
        start: c.effectiveStart,
        durationMin: c.durationMin,
        title: c.title,
        isFixed: c.isFixed,
        done: c.status === "Completed" || c.status === "Skipped",
        notePublic: c.notePublic,
        roles: c.roles
          .filter((r) => roleId === null || String(r.roleId) === String(roleId))
          .map((r) => ({ name: r.name, note: r.roleNote })),
      }));
  }
  return [...snap.cues]
    .sort((a, b) => a.seq - b.seq)
    .map((c) => ({
      id: c.id,
      start: c.effectiveStart,
      durationMin: c.durationMin,
      title: c.title,
      isFixed: c.isFixed,
      done: c.status === "Completed" || c.status === "Skipped",
      notePublic: c.notePublic,
      roles: [{ name: snap.role.name, note: c.roleNote }],
    }));
}
