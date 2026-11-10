import { defineConfig, devices } from "@playwright/test";
import { E2E_PORT as PORT } from "./tests/e2e/constants";

/**
 * End-to-end tests of the interface against a mocked API (tests/mocks): the
 * app is built with an API URL that resolves nowhere (http://api.test), every
 * call being answered by page.route(). A production build served by
 * `vite preview`, not the dev server: no dependency re-optimization reloading
 * a page in the middle of a test.
 */
export default defineConfig({
  testDir: "tests/e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI
    ? [["list"], ["junit", { outputFile: "test-results/e2e-junit.xml" }]]
    : "list",
  use: {
    baseURL: `http://localhost:${PORT}`,
    locale: "en-US",
    timezoneId: "Europe/Paris",
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: `yarn vite build --outDir dist-e2e --emptyOutDir && yarn vite preview --outDir dist-e2e --port ${PORT} --strictPort`,
    url: `http://localhost:${PORT}`,
    reuseExistingServer: !process.env.CI,
    env: {
      VITE_API_URL: "http://api.test",
      VITE_WS_URL: "ws://api.test",
      VITE_APP_NAME: "Loch.sh Panel",
    },
  },
});
