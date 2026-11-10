import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { hostingStack } from "../../../tests/mocks/fixtures";
import { mockApi } from "../../../tests/setup/msw";
import { renderWithProviders } from "../../../tests/setup/render";
import NetworkDetail from "./NetworkDetail";

const render = (name: string) =>
  renderWithProviders(<NetworkDetail />, {
    url: `/networks/${name}`,
    path: "/networks/:name",
  });

const memberRow = async (name: string) =>
  (await screen.findByRole("link", { name })).closest("tr") as HTMLElement;

/**
 * A dialog by its title (antd dialogs all get the same aria-labelledby id
 * under test, so the accessible name is not reliable).
 */
async function dialogTitled(title: string): Promise<HTMLElement> {
  const matches = await screen.findAllByText(title);
  const dialog = matches
    .map((m) => m.closest('[role="dialog"]'))
    .find((d): d is HTMLElement => d !== null);
  if (!dialog) throw new Error(`No dialog titled ${title}`);
  return dialog;
}

/** Opens an antd Select and picks an option by its text. */
async function choose(combobox: HTMLElement, option: RegExp) {
  await userEvent.click(combobox);
  const item = await screen.findByText(option, {
    selector: ".ant-select-item-option-content *",
  });
  await userEvent.click(item);
}

describe("NetworkDetail", () => {
  it("shows the information and the members with their addresses", async () => {
    render("backend");
    expect(
      await screen.findByRole("heading", { name: "backend" }),
    ).toBeInTheDocument();
    expect(await screen.findByText("loch-net-backend")).toBeInTheDocument();
    expect(screen.getByText("10.20.0.0/24")).toBeInTheDocument();
    expect(screen.getByText("Shop and its cache")).toBeInTheDocument();

    const redis = await memberRow("redis-cache");
    expect(within(redis).getByText("Service instance")).toBeInTheDocument();
    expect(within(redis).getByText("10.20.0.2")).toBeInTheDocument();
    expect(within(redis).getByText("svc-redis-cache")).toBeInTheDocument();
    expect(within(redis).getByRole("link")).toHaveAttribute(
      "href",
      "/services/redis",
    );

    // Declared by the site's compose file, but not on the network.
    const shop = await memberRow("shop");
    expect(
      within(shop).getByText("Declared but not connected"),
    ).toBeInTheDocument();
    expect(within(shop).getByRole("link")).toHaveAttribute(
      "href",
      "/hosting/shop",
    );
  });

  it("disconnects an unmanaged container after confirmation", async () => {
    const api = mockApi();
    render("backend");
    expect(
      await screen.findByText(
        "1 container not managed by the panel is on this network",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("ci-runner-1")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Disconnect/ }));
    const dialog = await dialogTitled("Disconnect ci-runner-1?");
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Disconnect" }),
    );
    await waitFor(() =>
      expect(api.calls("POST", "/networks/backend/disconnect")).toHaveLength(1),
    );
    expect(api.calls("POST", "/networks/backend/disconnect")[0].body).toEqual({
      containerId: "9923456789ab",
    });
    await waitFor(() =>
      expect(screen.queryByText("ci-runner-1")).not.toBeInTheDocument(),
    );
  });

  it("keeps an instance on at least one network", async () => {
    render("services");
    const pg = await memberRow("postgres-17");
    // postgres-17 is only on the shared network.
    await waitFor(() =>
      expect(within(pg).getByRole("button", { name: /Detach/ })).toBeDisabled(),
    );
    const redis = await memberRow("redis-cache");
    expect(within(redis).getByRole("button", { name: /Detach/ })).toBeEnabled();
    // The system network cannot be deleted.
    expect(screen.getByRole("button", { name: /Delete/ })).toBeDisabled();
  });

  it("shows a function member and attaches another function", async () => {
    const api = mockApi().withFunctionNetworks();
    render("backend");
    const report = await memberRow("report");
    expect(within(report).getByText("Function")).toBeInTheDocument();
    expect(within(report).getByText("fn-report")).toBeInTheDocument();
    expect(within(report).getByRole("link")).toHaveAttribute(
      "href",
      "/functions/report",
    );

    await userEvent.click(
      screen.getByRole("button", { name: /Attach a resource/ }),
    );
    const dialog = await dialogTitled("Attach a resource to backend");
    await choose(within(dialog).getByRole("combobox"), /^thumbs$/);
    expect(
      within(dialog).getByText(/The function thumbs is recreated/),
    ).toBeInTheDocument();
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Attach" }),
    );
    await waitFor(() =>
      expect(api.calls("POST", "/networks/backend/members")).toHaveLength(1),
    );
    expect(api.calls("POST", "/networks/backend/members")[0].body).toEqual({
      kind: "function",
      name: "thumbs",
    });
    expect(await memberRow("thumbs")).toBeInTheDocument();
    expect(api.state.functions[0].networks).toEqual(["backend", "fn-thumbs"]);
  });

  it("attaches a service instance and says which container restarts", async () => {
    const api = mockApi();
    api.state.networks[1].members = [];
    render("backend");
    await userEvent.click(
      await screen.findByRole("button", { name: /Attach a resource/ }),
    );

    const dialog = await dialogTitled("Attach a resource to backend");

    await choose(within(dialog).getByRole("combobox"), /^PostgreSQL® 17$/);
    expect(
      within(dialog).getByText(/svc-postgres-17 restarts/),
    ).toBeInTheDocument();
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Attach" }),
    );
    await waitFor(() =>
      expect(api.calls("POST", "/networks/backend/members")).toHaveLength(1),
    );
    expect(api.calls("POST", "/networks/backend/members")[0].body).toEqual({
      kind: "instance",
      name: "postgres-17",
    });
    const pg = await memberRow("postgres-17");
    expect(within(pg).getByText("10.20.0.2")).toBeInTheDocument();
  });

  it("refuses an engine without authentication on the shared network", async () => {
    const api = mockApi();
    api.state.instances.push({
      ...api.state.instances[1],
      key: "elasticmq-jobs",
      engine: "elasticmq",
      version: "1.7",
      project: "svc-elasticmq-jobs",
      name: "jobs",
      unauthenticated: true,
      networks: ["backend"],
    });
    render("services");
    await userEvent.click(
      await screen.findByRole("button", { name: /Attach a resource/ }),
    );
    const dialog = await dialogTitled("Attach a resource to services");
    await userEvent.click(within(dialog).getByRole("combobox"));
    const option = (
      await screen.findByText(/cannot join the shared network/)
    ).closest(".ant-select-item-option");
    expect(option).toHaveClass("ant-select-item-option-disabled");
  });

  it("attaches a site: redeployed, then reported", async () => {
    const api = mockApi();
    api.state.stacks = [hostingStack("blog")];
    render("backend");
    await userEvent.click(
      await screen.findByRole("button", { name: /Attach a resource/ }),
    );
    const dialog = await dialogTitled("Attach a resource to backend");
    await choose(within(dialog).getByRole("combobox"), /^blog$/);
    expect(
      within(dialog).getByText(/The site blog is redeployed/),
    ).toBeInTheDocument();
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Attach" }),
    );
    await waitFor(() =>
      expect(api.calls("POST", "/networks/backend/members")[0]?.body).toEqual({
        kind: "site",
        name: "blog",
      }),
    );
    expect(await memberRow("blog")).toBeInTheDocument();
  });
});
