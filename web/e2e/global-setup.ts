import { saveState, setUp, tearDown } from "./target";

// One test event per route, so the routes never see each other's changes.
export const ROUTE_EVENTS = 6;

export default async function globalSetup() {
  await tearDown(); // a run that was interrupted
  saveState(await setUp(ROUTE_EVENTS));
}
