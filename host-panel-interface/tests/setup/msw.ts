/**
 * MSW server serving the in-memory MockApi to the Vitest tests. Each test
 * gets a fresh MockApi (reset in vitest.setup.ts), reachable with mockApi().
 */
import { http, HttpResponse } from "msw/http";
import { setupServer } from "msw/node";
import { createMockApi, type MockApi } from "../mocks/api";
import { API_URL } from "../mocks/fixtures";

let current: MockApi = createMockApi();

export function resetMockApi(): MockApi {
  current = createMockApi();
  return current;
}

/** The MockApi of the running test. */
export function mockApi(): MockApi {
  return current;
}

export const server = setupServer(
  http.all(`${API_URL}/*`, async ({ request }) => {
    // Bound before any await: a late request of a finished test must not
    // land in the next test's mock.
    const api = current;
    // jsdom enforces CORS: answer like the API, for the page origin and with
    // credentials (the session cookie).
    const cors = {
      "Access-Control-Allow-Origin": request.headers.get("origin") ?? "*",
      "Access-Control-Allow-Credentials": "true",
      "Access-Control-Allow-Methods": "GET, POST, PUT, PATCH, DELETE",
      "Access-Control-Allow-Headers": "Content-Type, Accept",
    };
    if (request.method === "OPTIONS") {
      return new HttpResponse(null, { status: 204, headers: cors });
    }
    const url = new URL(request.url);
    const raw = request.method === "GET" ? "" : await request.text();
    let body: unknown;
    try {
      body = raw ? JSON.parse(raw) : undefined;
    } catch {
      body = raw;
    }
    const res = api.handle({
      method: request.method,
      path: url.pathname,
      query: url.searchParams,
      body,
    });
    if (res.text !== undefined) {
      return new HttpResponse(res.text, {
        status: res.status,
        headers: { ...cors, "Content-Type": res.contentType ?? "text/plain" },
      });
    }
    return HttpResponse.json(res.body ?? null, {
      status: res.status,
      headers: cors,
    });
  }),
);
