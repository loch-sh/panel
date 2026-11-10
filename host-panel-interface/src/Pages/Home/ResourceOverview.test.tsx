import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  apiKey,
  container,
  domainEntry,
  functions,
  hostingStack,
  networks,
  restrictedUser,
  sftpUser,
} from "../../../tests/mocks/fixtures";
import { mockApi } from "../../../tests/setup/msw";
import { renderWithProviders } from "../../../tests/setup/render";
import Home from "./Home";

/** The tiles of the card, by accessible name, in order. */
async function tileNames() {
  const card = (await screen.findByText("Resource overview")).closest(
    ".ant-card",
  ) as HTMLElement;
  return within(card)
    .getAllByRole("link")
    .map((link) => link.getAttribute("aria-label"));
}

describe("Home: resource overview", () => {
  it("shows the count of every resource an admin can see", async () => {
    const api = mockApi();
    api.state.containers = [
      container("shop-web-1", "shop"),
      container("shop-db-1", "shop"),
      container("blog-web-1", "blog"),
      container("standalone"),
    ];
    api.state.stacks = [hostingStack("alpha"), hostingStack("beta")];
    api.state.sftpConfig = { enabled: true, initDirs: [] };
    api.state.sftpUsers = [sftpUser("ann", "alpha"), sftpUser("bob", "alpha")];
    api.state.domains = [
      domainEntry("shop-web-1", ["shop.example.com", "www.shop.example.com"]),
      domainEntry("blog-web-1", ["blog.example.com", "shop.example.com"]),
    ];
    api.state.apiKeys = [apiKey("one")];

    renderWithProviders(<Home />);

    expect(
      await screen.findByRole("link", { name: "SFTP users: 2" }),
    ).toBeInTheDocument();
    await waitFor(async () =>
      expect(await tileNames()).toEqual([
        "Containers: 4",
        "Stacks: 2",
        "Hosted sites: 2",
        // postgres-17 holds one database; redis-cache is one dedicated instance.
        "Databases: 1",
        "Cache: 1",
        "Queues: 0",
        "Storage: 0",
        `Functions: ${functions().length}`,
        "Scheduled tasks: 0",
        `Private networks: ${networks().length}`,
        "SFTP users: 2",
        "Domains: 3",
        "API keys: 1",
      ]),
    );
    // Each category leads to its first enabled engine.
    expect(screen.getByRole("link", { name: "Databases: 1" })).toHaveAttribute(
      "href",
      "/services/postgres",
    );
    expect(screen.getByRole("link", { name: "Queues: 0" })).toHaveAttribute(
      "href",
      "/services/elasticmq",
    );
    expect(screen.getByRole("link", { name: "Functions: 3" })).toHaveAttribute(
      "href",
      "/functions",
    );
  });

  it("shows a restricted user only what the menu shows them", async () => {
    const api = mockApi();
    api.state.me = restrictedUser;
    api.state.containers = [container("shop-web-1", "shop")];

    renderWithProviders(<Home />);

    await waitFor(async () =>
      expect(await tileNames()).toEqual([
        "Containers: 1",
        "Stacks: 1",
        "Scheduled tasks: 0",
        "API keys: 0",
      ]),
    );
    // Admin-only endpoints are never called.
    for (const path of [
      "/services/config",
      "/services/instances",
      "/hosting/stacks",
      "/functions",
      "/networks",
      "/domains",
      "/sftp/users",
    ]) {
      expect(api.calls("GET", path), path).toHaveLength(0);
    }
  });

  it("hides the tiles of disabled modules", async () => {
    const api = mockApi();
    api.state.servicesConfig = { enabled: false };
    api.state.hostingConfig = { ...api.state.hostingConfig, enabled: false };
    api.state.functionsConfig = {
      ...api.state.functionsConfig,
      enabled: false,
    };

    renderWithProviders(<Home />);

    await waitFor(async () =>
      expect(await tileNames()).toEqual([
        "Containers: 0",
        "Stacks: 0",
        "Scheduled tasks: 0",
        `Private networks: ${networks().length}`,
        "Domains: 0",
        "API keys: 0",
      ]),
    );
    expect(api.calls("GET", "/services/instances")).toHaveLength(0);
    expect(api.calls("GET", "/sftp/users")).toHaveLength(0);
  });

  it("shows a dash when a count cannot be read", async () => {
    const api = mockApi();
    const failure = { status: 500, body: { code: "INTERNAL", error: "boom" } };
    api.on("GET", "/users/me/api-keys", failure);
    api.on("GET", "/services/instances", failure);

    renderWithProviders(<Home />);

    const keys = await screen.findByRole("link", {
      name: "API keys: unavailable",
    });
    expect(keys).toHaveTextContent("—");
    expect(
      await screen.findByRole("link", { name: "Databases: unavailable" }),
    ).toHaveTextContent("—");
    // The other tiles still show their count.
    expect(
      await screen.findByRole("link", { name: "Containers: 0" }),
    ).toBeInTheDocument();
  });
});
