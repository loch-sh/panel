import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { restrictedUser } from "../../../tests/mocks/fixtures";
import { mockApi } from "../../../tests/setup/msw";
import { renderWithProviders } from "../../../tests/setup/render";
import NetworksList from "./NetworksList";

const rowOf = async (name: string) =>
  (await screen.findByRole("link", { name })).closest("tr") as HTMLElement;

describe("NetworksList", () => {
  it("lists the networks with their subnet, members and tags", async () => {
    renderWithProviders(<NetworksList />);
    expect(
      await screen.findByRole("heading", { name: "Private networks" }),
    ).toBeInTheDocument();
    const shared = await rowOf("services");
    expect(within(shared).getByText("172.30.0.0/16")).toBeInTheDocument();
    expect(within(shared).getByText("Shared")).toBeInTheDocument();
    expect(within(shared).getByText("No Internet access")).toBeInTheDocument();
    expect(within(shared).getByText("OK")).toBeInTheDocument();
    const backend = await rowOf("backend");
    expect(within(backend).getByText("Private")).toBeInTheDocument();
    // Members: containers on the network (the declared site is not there).
    expect(
      within(backend).getByRole("cell", { name: "2" }),
    ).toBeInTheDocument();
  });

  it("never offers to delete the shared network", async () => {
    renderWithProviders(<NetworksList />);
    const shared = await rowOf("services");
    expect(
      within(shared).getByRole("button", { name: "Delete" }),
    ).toBeDisabled();
  });

  it("shows why a network is compromised", async () => {
    mockApi().state.networks[1].state = "compromised";
    mockApi().state.networks[1].problem = "not_internal";
    renderWithProviders(<NetworksList />);
    const backend = await rowOf("backend");
    await userEvent.hover(within(backend).getByText("Compromised"));
    expect(
      await screen.findByText(/its members could reach the Internet/),
    ).toBeInTheDocument();
  });

  it("deletes an empty network after confirmation", async () => {
    const api = mockApi();
    api.state.networks[1].members = [];
    renderWithProviders(<NetworksList />);
    const backend = await rowOf("backend");
    await userEvent.click(
      within(backend).getByRole("button", { name: "Delete" }),
    );
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Delete" }),
    );
    await waitFor(() =>
      expect(api.calls("DELETE", "/networks/backend")).toHaveLength(1),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("link", { name: "backend" }),
      ).not.toBeInTheDocument(),
    );
  });

  it("is forbidden to restricted users, without loading the networks", async () => {
    mockApi().state.me = restrictedUser;
    renderWithProviders(<NetworksList />);
    expect(await screen.findByText(/administrators/)).toBeInTheDocument();
    expect(mockApi().calls("GET", "/networks")).toHaveLength(0);
  });

  it("lists the other Docker networks read-only", async () => {
    renderWithProviders(<NetworksList />, { url: "/networks?tab=docker" });
    expect(
      await screen.findByText(/Docker networks the panel does not manage/),
    ).toBeInTheDocument();
    const traefik = await rowOf("traefik");
    expect(within(traefik).getByRole("link")).toHaveAttribute(
      "href",
      `/networks/docker/${"1".repeat(64)}`,
    );
    expect(within(traefik).getByText("Compose: proxy")).toBeInTheDocument();
    expect(within(traefik).getByText("172.18.0.0/16")).toBeInTheDocument();
    expect(
      within(traefik).getByRole("cell", { name: "2" }),
    ).toBeInTheDocument();
    expect(within(traefik).getByText("Internet access")).toBeInTheDocument();
    const ci = await rowOf("ci_default");
    expect(within(ci).getByText("Reserved range")).toBeInTheDocument();
    const bridge = await rowOf("bridge");
    expect(within(bridge).getByText("Docker")).toBeInTheDocument();
    // Read-only: no action.
    expect(within(traefik).queryByRole("button")).not.toBeInTheDocument();
  });

  it("switches between the panel and the Docker networks", async () => {
    const api = mockApi();
    renderWithProviders(<NetworksList />);
    await rowOf("backend");
    expect(api.calls("GET", "/networks/docker")).toHaveLength(0);
    await userEvent.click(
      screen.getByRole("tab", { name: "Other Docker networks" }),
    );
    expect(await rowOf("traefik")).toBeInTheDocument();
    expect(api.calls("GET", "/networks/docker")).toHaveLength(1);
  });

  it("flags a panel network overlapping a reserved range", async () => {
    mockApi().state.networks[1].reservedOverlap = "10.20.0.0/16";
    renderWithProviders(<NetworksList />);
    const backend = await rowOf("backend");
    await userEvent.hover(within(backend).getByText("Reserved range"));
    expect(
      await screen.findByText(/overlaps the reserved range 10\.20\.0\.0\/16/),
    ).toBeInTheDocument();
  });
});
