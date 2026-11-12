import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { container, containerInspect } from "../../../tests/mocks/fixtures";
import { mockApi } from "../../../tests/setup/msw";
import { renderWithProviders } from "../../../tests/setup/render";
import ContainerSidePanel from "./ContainerSidePanel";

const web = container("web", "shop");

function render(id = web.Id) {
  mockApi().state.containers = [web];
  return renderWithProviders(
    <ContainerSidePanel open onClose={() => {}} id={id} />,
  );
}

describe("ContainerSidePanel", () => {
  it("shows the container at a glance: state, health, stack and figures", async () => {
    render();
    const drawer = await screen.findByRole("dialog");
    expect(await within(drawer).findByText("web")).toBeInTheDocument();
    expect(
      within(drawer).getAllByText("mirror.gcr.io/library/nginx:1.30-alpine")
        .length,
    ).toBeGreaterThan(0);
    expect(within(drawer).getByText("Running")).toBeInTheDocument();
    expect(within(drawer).getByText("healthy")).toBeInTheDocument();
    expect(within(drawer).getByText("Stack shop")).toBeInTheDocument();
    // Figures: up for 2 hours, 3 restarts, 1 published port, 1 mount.
    expect(within(drawer).getByText("Up for")).toBeInTheDocument();
    expect(within(drawer).getByText("2 hours")).toBeInTheDocument();
    expect(within(drawer).getByText("3")).toBeInTheDocument();
    // Overview tab: the short id, copyable in full.
    expect(within(drawer).getByText(web.Id.substring(0, 12))).toBeInTheDocument();
  });

  it("lists the networks and the published ports, then the mounts", async () => {
    const user = userEvent.setup();
    render();
    const drawer = await screen.findByRole("dialog");
    await user.click(await within(drawer).findByRole("tab", { name: /Network/ }));
    expect(await within(drawer).findByText("loch-services")).toBeInTheDocument();
    expect(within(drawer).getByText("172.20.0.5")).toBeInTheDocument();
    expect(within(drawer).getByText("8080")).toBeInTheDocument();
    await user.click(within(drawer).getByRole("tab", { name: /Mounts/ }));
    expect(await within(drawer).findByText("/data")).toBeInTheDocument();
  });

  it("explains why the terminal is not available", async () => {
    const user = userEvent.setup();
    render();
    const drawer = await screen.findByRole("dialog");
    await user.click(await within(drawer).findByRole("tab", { name: /Terminal/ }));
    expect(await within(drawer).findByText(/tty:true/)).toBeInTheDocument();
    expect(
      within(drawer).queryByRole("button", { name: /Open a terminal/ }),
    ).not.toBeInTheDocument();
  });

  it("shows a stopped container: exit code, time since it stopped, no terminal", async () => {
    const user = userEvent.setup();
    mockApi().state.inspects[web.Id] = containerInspect(web, {
      running: false,
      tty: true,
      exitCode: 137,
    });
    render();
    const drawer = await screen.findByRole("dialog");
    expect(await within(drawer).findByText("Exited")).toBeInTheDocument();
    expect(within(drawer).getByText("Exit code 137")).toBeInTheDocument();
    expect(within(drawer).getByText("Stopped for")).toBeInTheDocument();
    expect(
      within(drawer).queryByRole("button", { name: "Console only" }),
    ).not.toBeInTheDocument();
    await user.click(within(drawer).getByRole("tab", { name: /Terminal/ }));
    expect(
      await within(drawer).findByText(/The container is not running/),
    ).toBeInTheDocument();
  });

  it("copes with what Docker leaves empty: no ports, mounts, labels nor dates", async () => {
    const user = userEvent.setup();
    const bare = container("bare");
    const info = containerInspect(bare);
    info.NetworkSettings.Ports = null as never;
    info.Mounts = null as never;
    info.Config.Labels = null as never;
    info.State.StartedAt = "0001-01-01T00:00:00Z";
    info.Created = "0001-01-01T00:00:00Z";
    mockApi().state.containers = [bare];
    mockApi().state.inspects[bare.Id] = info;
    renderWithProviders(
      <ContainerSidePanel open onClose={() => {}} id={bare.Id} />,
    );
    const drawer = await screen.findByRole("dialog");
    expect(
      await within(drawer).findByText("Standalone container"),
    ).toBeInTheDocument();
    expect(within(drawer).getAllByText("—").length).toBeGreaterThanOrEqual(3);
    await user.click(within(drawer).getByRole("tab", { name: /Network/ }));
    expect(await within(drawer).findByText("None")).toBeInTheDocument();
    await user.click(within(drawer).getByRole("tab", { name: /Mounts/ }));
    expect(await within(drawer).findAllByText("None")).not.toHaveLength(0);
  });

  it("has no data for an unknown container", async () => {
    renderWithProviders(
      <ContainerSidePanel open onClose={() => {}} id={"f".repeat(64)} />,
    );
    await waitFor(() =>
      expect(screen.getByText("No data for this container")).toBeInTheDocument(),
    );
  });
});
