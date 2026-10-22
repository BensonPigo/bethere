// End-to-end routes (docs/process/e2e-test-routes.md): run by hand after a
// deployment, never in CI. `npm run e2e` runs them locally;
// `E2E_TARGET=vm E2E_BASE_URL=https://… npm run e2e` against the VM's test service.
import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "e2e",
  // One route at a time, in file order: the restart route stops the server.
  workers: 1,
  fullyParallel: false,
  retries: 0,
  timeout: 4 * 60 * 1000,
  expect: { timeout: 10 * 1000 },
  reporter: [["list"], ["html", { open: "never", outputFolder: "e2e-report" }]],
  globalSetup: "./e2e/global-setup.ts",
  globalTeardown: "./e2e/global-teardown.ts",
  use: {
    browserName: "webkit", // Safari's engine, as on iPhone and iPad
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  outputDir: "e2e-results",
});
