import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { ReactElement } from "react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";

/** Shows the current location, for tests asserting a navigation. */
function LocationProbe() {
  return <div data-testid="location">{useLocation().pathname}</div>;
}

/**
 * Renders a component with the providers of the app (React Query without
 * retries, a router at the given URL). With a path, the element is mounted
 * on that route pattern (so that useParams works).
 */
export function renderWithProviders(
  ui: ReactElement,
  { url = "/", path }: { url?: string; path?: string } = {},
) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[url]}>
        <LocationProbe />
        {path ? (
          <Routes>
            <Route path={path} element={ui} />
            <Route path="*" element={null} />
          </Routes>
        ) : (
          ui
        )}
      </MemoryRouter>
    </QueryClientProvider>,
  );
}
