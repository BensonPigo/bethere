import { COMPANIES, createCompany, saveState, setUp, tearDown } from "./target";

// One test event per route, so the routes never see each other's changes:
// eight on today, and one on tomorrow for the preparation route (ADR-0032).
export const ROUTE_EVENTS = { today: 8, later: 2 };

export default async function globalSetup() {
  await tearDown(); // a run that was interrupted
  saveState(await setUp(ROUTE_EVENTS));
  // Two companies for the company routes, made as the developer does (US-O-11.1).
  for (const c of [COMPANIES.login, COMPANIES.events]) await createCompany(c.name, c.email);
}
