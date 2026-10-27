// The preparation checklist on the draft console (US-M-18): every tick
// comes from the snapshot, so nothing is stored and every device agrees.
import type { MasterRole, MasterSnapshot } from "./api";

export type Prep = {
  rundown: boolean; // at least one cue
  roles: boolean; // a role besides the Master
  links: boolean; // every such role has a live link
  firstWithoutLink: MasterRole | null;
  firstRole: MasterRole | null;
};

export function prep(snap: MasterSnapshot): Prep {
  const roles = snap.roles.filter((r) => r.type !== "Master");
  const firstWithoutLink = roles.find((r) => !r.hasLink) ?? null;
  return {
    rundown: snap.cues.length > 0,
    roles: roles.length > 0,
    links: roles.length > 0 && firstWithoutLink === null,
    firstWithoutLink,
    firstRole: roles[0] ?? null,
  };
}

export const prepDone = (p: Prep) => p.rundown && p.roles && p.links;
