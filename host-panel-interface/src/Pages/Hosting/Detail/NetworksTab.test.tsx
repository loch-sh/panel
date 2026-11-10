import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { hostingStack } from "../../../../tests/mocks/fixtures";
import { mockApi } from "../../../../tests/setup/msw";
import { renderWithProviders } from "../../../../tests/setup/render";
import {
  HostingSpec,
  HostingStackDetail,
  HostingStackSummary,
} from "../../../Services/Hosting/Models/hosting.model";
import NetworksTab from "./NetworksTab";

/** The detail of the stack "shop", declared on the given networks. */
function shop(
  networks: string[],
  extra: Partial<HostingStackSummary> = {},
): HostingStackDetail & { spec: HostingSpec } {
  const s = hostingStack("shop");
  return {
    ...s,
    spec: {
      name: s.name,
      template: s.template,
      phpVersion: s.phpVersion,
      site: s.site,
      documentRoot: "web",
      domains: s.domains,
      https: true,
      redirectHttps: true,
      resources: { memoryMB: 512, cpus: 1 },
      env: [],
      networks,
    },
    snippets: { phpIni: "", vhost: "" },
    containers: [],
    ...extra,
  };
}

function render(stack: HostingStackDetail & { spec: HostingSpec }) {
  mockApi().state.stacks = [hostingStack("shop")];
  const onOperationStarted = vi.fn();
  renderWithProviders(
    <NetworksTab stack={stack} onOperationStarted={onOperationStarted} />,
  );
  return onOperationStarted;
}

const rowOf = async (name: string) =>
  (await screen.findByRole("link", { name })).closest("tr") as HTMLElement;

/** A dialog by its title (antd dialogs share their aria-labelledby id). */
async function dialogTitled(title: string): Promise<HTMLElement> {
  const matches = await screen.findAllByText(title);
  const dialog = matches
    .map((m) => m.closest('[role="dialog"]'))
    .find((d): d is HTMLElement => d !== null);
  if (!dialog) throw new Error(`No dialog titled ${title}`);
  return dialog;
}

describe("Hosting NetworksTab", () => {
  it("lists the site's networks with its address and state", async () => {
    const api = mockApi();
    // shop is on backend now, with an address.
    const member = api.state.networks[1].members.find(
      (m) => m.name === "shop",
    )!;
    Object.assign(member, { present: true, ipv4: "10.20.0.7" });
    api.state.networks[1].reservedOverlap = "10.20.0.0/16";
    render(shop(["backend"]));

    const backend = await rowOf("backend");
    expect(within(backend).getByRole("link")).toHaveAttribute(
      "href",
      "/networks/backend",
    );
    expect(await within(backend).findByText("Private")).toBeInTheDocument();
    expect(within(backend).getByText("10.20.0.0/24")).toBeInTheDocument();
    expect(await within(backend).findByText("10.20.0.7")).toBeInTheDocument();
    expect(within(backend).getByText("Connected")).toBeInTheDocument();
    expect(within(backend).getByText("Reserved range")).toBeInTheDocument();

    // The other Docker networks of its container (read-only).
    const traefik = await rowOf("traefik");
    expect(within(traefik).getByRole("link")).toHaveAttribute(
      "href",
      `/networks/docker/${"1".repeat(64)}`,
    );
    expect(within(traefik).getByText("Compose: proxy")).toBeInTheDocument();
    expect(within(traefik).getByText("172.18.0.5")).toBeInTheDocument();
  });

  it("says when a declared network is not connected", async () => {
    render(shop(["backend"]));
    const backend = await rowOf("backend");
    expect(
      await within(backend).findByText("Declared but not connected"),
    ).toBeInTheDocument();
  });

  it("connects a network and follows the redeployment", async () => {
    const api = mockApi();
    const onOperationStarted = render(shop(["backend"]));
    await userEvent.click(
      await screen.findByRole("button", { name: /Connect a network/ }),
    );
    const dialog = await dialogTitled("Connect shop to a network");
    // backend is already declared: only services is offered.
    await userEvent.click(within(dialog).getByRole("combobox"));
    expect(
      screen.queryByText("backend", {
        selector: ".ant-select-item-option-content *",
      }),
    ).not.toBeInTheDocument();
    await userEvent.click(
      await screen.findByText("services", {
        selector: ".ant-select-item-option-content *",
      }),
    );
    expect(
      within(dialog).getByText(/The site shop is redeployed/),
    ).toBeInTheDocument();
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Connect" }),
    );
    await waitFor(() =>
      expect(onOperationStarted).toHaveBeenCalledWith("op-attach-shop"),
    );
    expect(api.calls("POST", "/networks/services/members")[0].body).toEqual({
      kind: "site",
      name: "shop",
    });
  });

  it("disconnects a network after confirmation", async () => {
    const api = mockApi();
    const onOperationStarted = render(shop(["backend"]));
    const backend = await rowOf("backend");
    await userEvent.click(
      within(backend).getByRole("button", { name: /Disconnect/ }),
    );
    const dialog = await dialogTitled("Detach shop from backend?");
    expect(
      within(dialog).getByText(/a few seconds of downtime/),
    ).toBeInTheDocument();
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Disconnect" }),
    );
    await waitFor(() =>
      expect(onOperationStarted).toHaveBeenCalledWith("op-detach-shop"),
    );
    expect(
      api.calls("DELETE", "/networks/backend/members/site/shop"),
    ).toHaveLength(1);
  });

  it("offers no change on a stack edited by hand or busy", async () => {
    render(shop(["backend"], { modified: true }));
    expect(
      await screen.findAllByText(/edited by hand: the networks cannot/),
    ).not.toHaveLength(0);
    expect(
      screen.getByRole("button", { name: /Connect a network/ }),
    ).toBeDisabled();
    const backend = await rowOf("backend");
    expect(
      within(backend).getByRole("button", { name: /Disconnect/ }),
    ).toBeDisabled();
  });
});
