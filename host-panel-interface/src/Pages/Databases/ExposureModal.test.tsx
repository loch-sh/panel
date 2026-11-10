import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import {
  serviceInstances,
  servicesConfig,
} from "../../../tests/mocks/fixtures";
import { mockApi } from "../../../tests/setup/msw";
import { renderWithProviders } from "../../../tests/setup/render";
import type { ServiceInstanceModel } from "../../Services/Databases/Models/databases.model";
import ExposureModal from "./ExposureModal";

const config = servicesConfig();
const engineOf = (id: string) => config.engines!.find((e) => e.id === id);

function render(instance: ServiceInstanceModel, onClose = vi.fn()) {
  renderWithProviders(
    <ExposureModal
      instance={instance}
      engine={engineOf(instance.engine)}
      limits={config.exposure}
      onClose={onClose}
    />,
  );
  return onClose;
}

const postgres = () => serviceInstances()[0];
const address = () => screen.getByRole("combobox", { name: "Address" });
const port = () => screen.getByRole("spinbutton", { name: "Port" });
const publish = () =>
  userEvent.click(screen.getByRole("button", { name: "Publish" }));

async function fill(addr: string, p: string) {
  await userEvent.clear(address());
  await userEvent.type(address(), addr);
  await userEvent.clear(port());
  await userEvent.type(port(), p);
}

describe("ExposureModal", () => {
  it("validates the address and port against the server policy", async () => {
    render(postgres());
    expect(await screen.findByText(/Not published/)).toBeInTheDocument();
    await fill("198.51.100.7", "8080");
    expect(
      await screen.findByText(
        "This address is not allowed by the server configuration.",
      ),
    ).toBeInTheDocument();
    expect(
      await screen.findByText("This port is reserved on this server."),
    ).toBeInTheDocument();
    await userEvent.clear(port());
    await userEvent.type(port(), "80");
    expect(
      await screen.findByText("The port must be between 1024 and 32767."),
    ).toBeInTheDocument();
    await publish();
    expect(
      mockApi().calls("PUT", "/services/instances/postgres-17/exposure"),
    ).toHaveLength(0);
  });

  it("warns that TLS becomes required, and offers optional TLS", async () => {
    const onClose = render(postgres());
    await fill("203.0.113.10", "5417");
    expect(
      await screen.findByText(/TLS will be required from every client/),
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("switch", { name: "TLS optional" }));
    await publish();
    const api = mockApi();
    await waitFor(() =>
      expect(
        api.calls("PUT", "/services/instances/postgres-17/exposure"),
      ).toHaveLength(1),
    );
    expect(
      api.calls("PUT", "/services/instances/postgres-17/exposure")[0].body,
    ).toEqual({
      address: "203.0.113.10",
      port: 5417,
      tlsOptional: true,
    });
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("offers no TLS option on a loopback address", async () => {
    render(postgres());
    await fill("127.0.0.1", "15432");
    expect(await screen.findByText(/Loopback address/)).toBeInTheDocument();
    expect(
      screen.queryByRole("switch", { name: "TLS optional" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByText(/TLS will be required/)).not.toBeInTheDocument();
  });

  it("keeps an out-of-support version on loopback", async () => {
    render({ ...postgres(), deprecated: true });
    expect(
      (await screen.findAllByText(/This version is out of support/)).length,
    ).toBeGreaterThan(0);
    await fill("203.0.113.10", "5417");
    await waitFor(() =>
      expect(
        screen.getAllByText(/This version is out of support/),
      ).toHaveLength(2),
    );
  });

  it("shows the server error and the fingerprint; closes the port", async () => {
    const api = mockApi();
    api.on("PUT", "/services/instances/postgres-17/exposure", {
      status: 409,
      body: { code: "SERVICE_PORT_IN_USE", error: "in use", field: "port" },
    });
    const onClose = render({
      ...postgres(),
      exposure: {
        address: "203.0.113.10",
        port: 5417,
        tls: true,
        fingerprint: "AB:CD:EF:01",
      },
    });
    expect(await screen.findByText("AB:CD:EF:01")).toBeInTheDocument();
    await publish();
    expect(
      await screen.findByText(
        "This port is already used by another instance on this address.",
      ),
    ).toBeInTheDocument();

    await userEvent.click(
      screen.getByRole("button", { name: "Close the port" }),
    );
    const confirm = (
      await screen.findAllByText("Close the port of svc-postgres-17?")
    )[0].closest('[role="dialog"]') as HTMLElement;
    await userEvent.click(
      within(confirm).getByRole("button", { name: "Close the port" }),
    );
    await waitFor(() =>
      expect(
        api.calls("DELETE", "/services/instances/postgres-17/exposure"),
      ).toHaveLength(1),
    );
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });
});
