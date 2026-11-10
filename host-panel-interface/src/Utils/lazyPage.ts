import { lazy, type ComponentType } from "react";

const RELOAD_KEY = "lazyPage.reloadedAt";

/**
 * lazy() for the pages, recovering from a chunk that can no longer be
 * fetched: after a deployment (or a dev server rebuild), a tab still running
 * the previous version asks for files that no longer exist. The app reloads
 * once to get the current version; if loading fails again within a minute,
 * the error goes to the ErrorBoundary instead of reloading in a loop.
 */
export function lazyPage<T extends ComponentType<object>>(
  load: () => Promise<{ default: T }>,
) {
  return lazy(async () => {
    try {
      const mod = await load();
      return mod;
    } catch (err) {
      let last = 0;
      try {
        last = Number(sessionStorage.getItem(RELOAD_KEY) ?? 0);
      } catch {
        // storage unavailable: never reload, show the error
        last = Date.now();
      }
      if (Date.now() - last > 60_000) {
        try {
          sessionStorage.setItem(RELOAD_KEY, String(Date.now()));
        } catch {
          // ignored: the reload still happens once
        }
        window.location.reload();
        // Never resolves: the page is being reloaded.
        return new Promise<{ default: T }>(() => {});
      }
      throw err;
    }
  });
}
