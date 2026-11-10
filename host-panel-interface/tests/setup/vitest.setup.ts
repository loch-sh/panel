import "@testing-library/jest-dom/vitest";
import { cleanup, configure } from "@testing-library/react";
import { Modal } from "antd";
import { afterAll, afterEach, beforeAll, beforeEach, expect } from "vitest";
import i18n from "../../src/i18n";
import { resetMockApi, server, mockApi } from "./msw";

// The first test of a file pays the cold Ant Design render (CSS-in-JS) and
// chained requests (e.g. /users/me, then /services/config): about 300 ms on a
// workstation, over the 1 s default of findBy*/waitFor on a busy CI runner.
configure({ asyncUtilTimeout: 3000 });

// Ant Design relies on browser APIs jsdom does not provide.
Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }),
});
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
};

beforeAll(() => server.listen({ onUnhandledFrame: "error" }));
beforeEach(async () => {
  resetMockApi();
  localStorage.clear();
  await i18n.changeLanguage("en");
});
afterEach(() => {
  cleanup();
  // Confirmations (Modal.confirm) live outside the rendered tree.
  Modal.destroyAll();
  server.resetHandlers();
  // A route the component called but the mock does not serve.
  expect(mockApi().unhandled, "API routes called but not mocked").toEqual([]);
});
afterAll(() => server.close());
