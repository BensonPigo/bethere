import { tearDown } from "./target";

export default async function globalTeardown() {
  await tearDown();
}
