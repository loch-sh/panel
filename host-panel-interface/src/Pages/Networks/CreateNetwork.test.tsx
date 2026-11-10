import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { hostingStack } from "../../../tests/mocks/fixtures";
import { mockApi } from "../../../tests/setup/msw";
import { renderWithProviders } from "../../../tests/setup/render";
import { Route, Routes } from "react-router";
import CreateNetwork from "./CreateNetwork";
import NetworkDetail from "./NetworkDetail";

const render = () =>
  renderWithProviders(<CreateNetwork />, { url: "/networks/new" });

const nameInput = () => screen.findByRole("textbox", { name: "Name" });
const submit = () =>
  userEvent.click(
    screen.getAllByRole("button", { name: "Create the network" })[0],
  );

describe("CreateNetwork", () => {
  it("validates the name while typing", async () => {
    render();
    const input = await nameInput();
    await userEvent.type(input, "Shop_1");
    expect(
      await screen.findByText(/1 to 24 lowercase letters/),
    ).toBeInTheDocument();
    await userEvent.clear(input);
    await userEvent.type(input, "services");
    expect(
      await screen.findByText("This name is reserved."),
    ).toBeInTheDocument();
    await userEvent.clear(input);
    // Loaded from GET /networks.
    await waitFor(() =>
      expect(mockApi().calls("GET", "/networks")).toHaveLength(1),
    );
    await userEvent.type(input, "backend");
    expect(
      await screen.findByText("A network with this name already exists."),
    ).toBeInTheDocument();
    await userEvent.clear(input);
    await userEvent.type(input, "front");
    // The Docker name follows the name (form help and summary).
    expect(
      (await screen.findAllByText(/loch-net-front/)).length,
    ).toBeGreaterThan(0);
    expect(mockApi().calls("POST", "/networks")).toHaveLength(0);
  });

  it("checks a manual subnet against the API rules", async () => {
    render();
    await userEvent.click(
      await screen.findByRole("radio", { name: /Manual subnet/ }),
    );
    const subnet = screen.getByRole("textbox", { name: "Subnet (CIDR)" });
    await userEvent.type(subnet, "10.20.0.1/24");
    expect(
      await screen.findByText(/with its network address: 10\.20\.0\.0\/24/),
    ).toBeInTheDocument();
    await userEvent.clear(subnet);
    await userEvent.type(subnet, "10.0.0.0/8");
    expect(
      await screen.findByText(/between \/16 and \/28/),
    ).toBeInTheDocument();
    await userEvent.clear(subnet);
    await userEvent.type(subnet, "8.8.0.0/16");
    expect(
      await screen.findByText(/The range must be private/),
    ).toBeInTheDocument();
    await userEvent.clear(subnet);
    await userEvent.type(subnet, "10.20.0.0/24");
    expect(await screen.findByText("253 usable addresses")).toBeInTheDocument();
  });

  it("creates the network and opens its page", async () => {
    const api = mockApi();
    render();
    await userEvent.type(await nameInput(), "front");
    await userEvent.type(
      screen.getByRole("textbox", { name: "Description" }),
      "Public sites",
    );
    await userEvent.click(screen.getByRole("radio", { name: /Manual subnet/ }));
    await userEvent.type(
      screen.getByRole("textbox", { name: "Subnet (CIDR)" }),
      "192.168.40.0/24",
    );
    await submit();
    await waitFor(() => expect(api.calls("POST", "/networks")).toHaveLength(1));
    expect(api.calls("POST", "/networks")[0].body).toEqual({
      name: "front",
      description: "Public sites",
      subnet: "192.168.40.0/24",
    });
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent(
        "/networks/front",
      ),
    );
  });

  it("sends no subnet in automatic mode and refuses an empty name", async () => {
    const api = mockApi();
    render();
    await nameInput();
    await submit();
    expect(await screen.findByText("Enter a name.")).toBeInTheDocument();
    expect(api.calls("POST", "/networks")).toHaveLength(0);
    await userEvent.type(await nameInput(), "jobs");
    await submit();
    await waitFor(() => expect(api.calls("POST", "/networks")).toHaveLength(1));
    expect(api.calls("POST", "/networks")[0].body).toEqual({ name: "jobs" });
  });

  it("refuses a manual subnet in a reserved range and shows the ranges", async () => {
    render();
    // The ranges of GET /networks/config.
    expect(await screen.findByText("10.8.0.0/16")).toBeInTheDocument();
    expect(screen.getByText("100.64.0.0/10")).toBeInTheDocument();
    expect(await screen.findByText("1 / 20")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("radio", { name: /Manual subnet/ }));
    const subnet = screen.getByRole("textbox", { name: "Subnet (CIDR)" });
    await userEvent.type(subnet, "10.8.4.0/24");
    expect(
      await screen.findByText(/overlaps the reserved range 10\.8\.0\.0\/16/),
    ).toBeInTheDocument();
    await userEvent.clear(subnet);
    // A larger range containing a reserved one overlaps it too.
    await userEvent.type(subnet, "192.168.0.0/16");
    expect(
      await screen.findByText(/overlaps the reserved range 192\.168\.1\.0\/24/),
    ).toBeInTheDocument();
    await userEvent.type(await nameInput(), "front");
    await submit();
    expect(mockApi().calls("POST", "/networks")).toHaveLength(0);
  });

  it("blocks the creation once the limit is reached", async () => {
    mockApi().state.networksConfig.max = 1;
    render();
    expect(
      await screen.findByText(/The limit of 1 private networks is reached/),
    ).toBeInTheDocument();
    for (const b of screen.getAllByRole("button", {
      name: "Create the network",
    })) {
      expect(b).toBeDisabled();
    }
  });

  it("attaches the chosen sites once the network is created", async () => {
    const api = mockApi();
    api.state.stacks = [hostingStack("blog"), hostingStack("shop")];
    render();
    await userEvent.type(await nameInput(), "front");
    const members = screen.getByRole("combobox", {
      name: "Sites and service instances",
    });
    for (const site of ["blog", "shop"]) {
      await userEvent.click(members);
      await userEvent.click(
        await screen.findByText(site, {
          selector: ".ant-select-item-option-content *",
        }),
      );
    }
    expect(
      await screen.findByText(/Each selected member is recreated/),
    ).toBeInTheDocument();
    await submit();
    await waitFor(() =>
      expect(api.calls("POST", "/networks/front/members")).toHaveLength(2),
    );
    expect(api.calls("POST", "/networks")).toHaveLength(1);
    expect(
      api.calls("POST", "/networks/front/members").map((c) => c.body),
    ).toEqual([
      { kind: "site", name: "blog" },
      { kind: "site", name: "shop" },
    ]);
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent(
        "/networks/front",
      ),
    );
  });

  it("reports the members that could not be attached", async () => {
    const api = mockApi();
    api.state.stacks = [hostingStack("blog")];
    api.on("POST", "/networks/:name/members", {
      status: 409,
      body: { code: "HOSTING_OPERATION_RUNNING", error: "busy" },
    });
    renderWithProviders(
      <Routes>
        <Route path="/networks/new" element={<CreateNetwork />} />
        <Route path="/networks/:name" element={<NetworkDetail />} />
      </Routes>,
      { url: "/networks/new" },
    );
    await userEvent.type(await nameInput(), "front");
    await userEvent.click(
      screen.getByRole("combobox", { name: "Sites and service instances" }),
    );
    await userEvent.click(
      await screen.findByText("blog", {
        selector: ".ant-select-item-option-content *",
      }),
    );
    await submit();
    // The network exists: its page says which member failed.
    const alert = (
      await screen.findByText("1 member could not be attached")
    ).closest(".ant-alert") as HTMLElement;
    expect(within(alert).getByText("blog")).toBeInTheDocument();
    expect(
      within(alert).getByText(/attach them again with "Attach a resource"/),
    ).toBeInTheDocument();
    expect(api.calls("POST", "/networks")).toHaveLength(1);
  });
});
