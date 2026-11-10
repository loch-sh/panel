import "@testing-library/jest-dom/vitest";
import { cleanup, configure } from "@testing-library/react";
import { ConfigProvider, Modal } from "antd";
import { createElement } from "react";
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

// Static dialogs (Modal.confirm) render in their own React root, outside the
// tested tree. jsdom never ends a CSS transition, so a closing dialog would
// wait for rc-motion's fallback deadline before unmounting: that late React
// work can land after the environment is torn down ("window is not
// defined"). Without motion they close and unmount at once.
ConfigProvider.config({
  holderRender: (children) =>
    createElement(
      ConfigProvider,
      { theme: { token: { motion: false } } },
      children,
    ),
});

beforeAll(() => server.listen({ onUnhandledFrame: "error" }));
beforeEach(async () => {
  resetMockApi();
  localStorage.clear();
  await i18n.changeLanguage("en");
});
afterEach(async () => {
  cleanup();
  // Confirmations (Modal.confirm) live outside the rendered tree.
  Modal.destroyAll();
  // An update outside act (e.g. a mocked API response landing at the end of
  // a test) makes React schedule its passive effects on the scheduler
  // (setImmediate). Let them run while jsdom is still there: run after the
  // environment is torn down, they fail with "window is not defined", an
  // unhandled error that fails the whole run (seen on the slower CI runner).
  // A timer fires after the immediates already queued.
  await new Promise((resolve) => setTimeout(resolve, 0));
  server.resetHandlers();
  // A route the component called but the mock does not serve.
  expect(mockApi().unhandled, "API routes called but not mocked").toEqual([]);
});
afterAll(() => server.close());
