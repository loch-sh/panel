import { defineConfig, mergeConfig } from "vitest/config";
import viteConfig from "./vite.config";

/**
 * Unit and component tests (jsdom), on top of the app's Vite config. The API
 * is mocked with MSW from the same in-memory MockApi as the end-to-end tests
 * (tests/mocks).
 */
export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: "jsdom",
      include: ["src/**/*.test.{ts,tsx}"],
      setupFiles: ["tests/setup/vitest.setup.ts"],
      env: {
        VITE_API_URL: "http://api.test",
        VITE_WS_URL: "ws://api.test",
      },
      css: false,
      restoreMocks: true,
      // The CI runner is shared with the e2e job (same stage) and runs the
      // form tests about 10x slower than a workstation: allow more time
      // per test and use fewer workers there, instead of failing at random.
      testTimeout: process.env.CI ? 20_000 : 5_000,
      maxWorkers: process.env.CI ? 2 : undefined,
      // `yarn coverage`: Cobertura for the GitLab merge request diff and the
      // coverage history, text summary for the job coverage regex.
      coverage: {
        provider: "v8",
        include: ["src/**/*.{ts,tsx}"],
        exclude: ["src/**/*.test.{ts,tsx}", "src/**/*.d.ts", "src/i18n/locales/**"],
        reporter: ["text-summary", "cobertura", "html"],
        reportsDirectory: "coverage",
      },
    },
  }),
);
