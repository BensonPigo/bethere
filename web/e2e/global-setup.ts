import { saveState, setUp, tearDown } from "./target";

// One test event per route, so the routes never see each other's changes:
// six on today, and one on tomorrow for the preparation route (ADR-0032).
export const ROUTE_EVENTS = { today: 6, later: 1 };

export default async function globalSetup() {
  await tearDown(); // a run that was interrupted
  saveState(await setUp(ROUTE_EVENTS));
}
