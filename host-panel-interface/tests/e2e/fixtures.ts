/**
 * Playwright fixtures: every request to the API base URL is answered by the
 * in-memory MockApi (no real server, no network), and the WebSocket is
 * accepted without a server. A test fails if the app called a route that is
 * not mocked, unless it opts out with `test.use({ strictApi: false })`.
 */
import { expect, test as base } from "@playwright/test";
import { API_URL, WS_URL } from "../mocks/fixtures";
import { APP_ORIGIN } from "./constants";
import { createMockApi, type MockApi } from "../mocks/api";

interface Fixtures {
  api: MockApi;
  strictApi: boolean;
}

export const test = base.extend<Fixtures>({
  strictApi: [true, { option: true }],
  // auto: the API is mocked even in tests that do not use the fixture.
  api: [
    async ({ page, strictApi }, use) => {
      const api = createMockApi();
      // Nothing leaves the test: a request to any other origin is refused and
      // reported (registered first, so the API route below takes precedence).
      await page.route("**/*", async (route) => {
        const url = new URL(route.request().url());
        if (url.origin === APP_ORIGIN) return route.fallback();
        api.unhandled.push(
          `${route.request().method()} ${url.origin}${url.pathname}`,
        );
        return route.abort("blockedbyclient");
      });
      await page.route(`${API_URL}/**`, async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        let body: unknown;
        const raw = request.postData();
        if (raw) {
          try {
            body = JSON.parse(raw);
          } catch {
            body = raw;
          }
        }
        const res = api.handle({
          method: request.method(),
          path: url.pathname,
          query: url.searchParams,
          body,
        });
        await route.fulfill({
          status: res.status,
          contentType: res.contentType ?? "application/json",
          body:
            res.text ??
            (res.body === undefined ? "" : JSON.stringify(res.body)),
          // Credentialed CORS, as the real API answers the front origin.
          headers: {
            "Access-Control-Allow-Origin": APP_ORIGIN,
            "Access-Control-Allow-Credentials": "true",
          },
        });
      });
      // The terminal and logs use a WebSocket: accept it, answer nothing.
      await page.routeWebSocket(new RegExp(`^${WS_URL}`), () => {});
      await use(api);
      if (strictApi) {
        expect(api.unhandled, "API routes called but not mocked").toEqual([]);
      }
    },
    { auto: true },
  ],
});

export { expect };
