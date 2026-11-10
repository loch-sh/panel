import { render, screen } from "@testing-library/react";
import { Suspense } from "react";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import { lazyPage } from "../../Utils/lazyPage";
import ErrorBoundary from "./ErrorBoundary";

function Broken(): never {
  throw new Error("boom");
}

describe("ErrorBoundary", () => {
  it("shows a message instead of a blank page", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    render(
      <MemoryRouter>
        <p>menu</p>
        <ErrorBoundary>
          <Broken />
        </ErrorBoundary>
      </MemoryRouter>,
    );
    expect(screen.getByText("This page failed to display")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();
    // The rest of the layout stays.
    expect(screen.getByText("menu")).toBeInTheDocument();
  });
});

describe("lazyPage", () => {
  it("reloads once when a page chunk cannot be fetched, then shows the error", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const reload = vi.fn();
    vi.stubGlobal("location", { ...window.location, reload });
    const failing = () =>
      Promise.reject(
        new TypeError(
          "Failed to fetch dynamically imported module: /src/Pages/X.tsx",
        ),
      );

    const First = lazyPage(failing);
    render(
      <MemoryRouter>
        <ErrorBoundary>
          <Suspense fallback={<p>loading</p>}>
            <First />
          </Suspense>
        </ErrorBoundary>
      </MemoryRouter>,
    );
    await vi.waitFor(() => expect(reload).toHaveBeenCalledOnce());

    // Failing again right after the reload: no loop, a message.
    const Second = lazyPage(failing);
    render(
      <MemoryRouter>
        <ErrorBoundary>
          <Suspense fallback={<p>loading</p>}>
            <Second />
          </Suspense>
        </ErrorBoundary>
      </MemoryRouter>,
    );
    expect(
      await screen.findByText("A new version is available"),
    ).toBeInTheDocument();
    expect(reload).toHaveBeenCalledOnce();
    vi.unstubAllGlobals();
  });
});
