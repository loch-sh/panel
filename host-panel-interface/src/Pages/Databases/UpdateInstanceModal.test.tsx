import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import {
  serviceInstances,
  servicesConfig,
} from "../../../tests/mocks/fixtures";
import { mockApi } from "../../../tests/setup/msw";
import { renderWithProviders } from "../../../tests/setup/render";
import type {
  ServiceInstanceModel,
  ServiceVersionModel,
} from "../../Services/Databases/Models/databases.model";
import Databases from "./Databases";
import UpdateInstanceModal from "./UpdateInstanceModal";

// No ToastContainer in the tests: the toasts are checked on the calls.
const toast = vi.hoisted(() => ({
  success: vi.fn(),
  info: vi.fn(),
  warning: vi.fn(),
  error: vi.fn(),
}));
vi.mock("react-toastify", () => ({ toast }));

const redis = () => serviceInstances()[1];
const engine = () => servicesConfig().engines!.find((e) => e.id === "redis");
const UPDATE_PATH = "/services/instances/redis-cache/update";

const version = (v: string): ServiceVersionModel => ({
  version: v,
  image: `mirror.gcr.io/library/redis:${v}-alpine`,
  platform: "",
  deprecated: false,
});

function render(instance: ServiceInstanceModel, onClose = vi.fn()) {
  renderWithProviders(
    <UpdateInstanceModal
      instance={instance}
      engine={engine()}
      engineName="Redis®"
      onClose={onClose}
    />,
  );
  return onClose;
}

const submit = () =>
  userEvent.click(screen.getByRole("button", { name: "Update" }));

describe("UpdateInstanceModal", () => {
  it("offers the latest image first and sends no version for it", async () => {
    const onClose = render(redis());
    const image = await screen.findByRole("radio", {
      name: /Latest image of Redis® 8\.4/,
    });
    expect(image).toBeChecked();
    expect(
      screen.getByText("mirror.gcr.io/library/redis:8.4.1-alpine"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("radio", { name: /Redis® 8\.6/ }),
    ).not.toBeChecked();
    // Same version: no warning about going back.
    expect(screen.queryByText(/There is no way back/)).not.toBeInTheDocument();
    expect(screen.getByText(/The instance restarts/)).toBeInTheDocument();

    await submit();
    const api = mockApi();
    await waitFor(() => expect(api.calls("POST", UPDATE_PATH)).toHaveLength(1));
    expect(api.calls("POST", UPDATE_PATH)[0].body).toEqual({});
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(toast.success).toHaveBeenCalledWith("cache updated (Redis® 8.4).");
  });

  it("moves to a newer version, with its support dates and a warning", async () => {
    const onClose = render(redis());
    const newer = await screen.findByRole("radio", { name: /Redis® 8\.6/ });
    // Current version and the newer one both show their end of support.
    expect(screen.getAllByText(/^Security: /)).toHaveLength(2);
    await userEvent.click(newer);
    expect(
      await screen.findByText(
        /There is no way back: data written by Redis® 8\.6/,
      ),
    ).toBeInTheDocument();

    await submit();
    const api = mockApi();
    await waitFor(() => expect(api.calls("POST", UPDATE_PATH)).toHaveLength(1));
    expect(api.calls("POST", UPDATE_PATH)[0].body).toEqual({ version: "8.6" });
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("never offers the current or an older version", async () => {
    render({
      ...redis(),
      update: {
        image: false,
        versions: [version("8.6"), version("8.4"), version("8.2")],
      },
    });
    const radios = await screen.findAllByRole("radio");
    expect(radios).toHaveLength(1);
    expect(radios[0]).toHaveAccessibleName(/Redis® 8\.6/);
  });

  it("never preselects a version change: it must be picked", async () => {
    const onClose = render({
      ...redis(),
      update: { image: false, versions: [version("9.0"), version("8.6")] },
    });
    const radios = await screen.findAllByRole("radio");
    expect(radios).toHaveLength(2);
    for (const radio of radios) expect(radio).not.toBeChecked();
    const ok = screen.getByRole("button", { name: "Update" });
    expect(ok).toBeDisabled();
    expect(screen.queryByText(/There is no way back/)).not.toBeInTheDocument();
    await userEvent.click(ok);
    const api = mockApi();
    expect(api.calls("POST", UPDATE_PATH)).toHaveLength(0);

    await userEvent.click(screen.getByRole("radio", { name: /Redis® 8\.6/ }));
    expect(ok).toBeEnabled();
    expect(
      await screen.findByText(/There is no way back: data written by Redis® 8\.6/),
    ).toBeInTheDocument();
    await submit();
    await waitFor(() => expect(api.calls("POST", UPDATE_PATH)).toHaveLength(1));
    expect(api.calls("POST", UPDATE_PATH)[0].body).toEqual({ version: "8.6" });
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("says when there is nothing to update", async () => {
    render({ ...redis(), update: { image: false, versions: [] } });
    expect(
      await screen.findByRole("dialog", { name: "Update cache" }),
    ).toBeInTheDocument();
    expect(screen.queryAllByRole("radio")).toHaveLength(0);
    expect(screen.getByRole("button", { name: "Update" })).toBeDisabled();
  });

  it("shows the translated error and keeps the modal open", async () => {
    mockApi().on("POST", UPDATE_PATH, {
      status: 502,
      body: { code: "SERVICE_INSTANCE_START_FAILED", error: "x" },
    });
    const onClose = render(redis());
    await screen.findByRole("radio", { name: /Latest image/ });
    await submit();
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        "The instance did not start with the update: the previous version was restored.",
      ),
    );
    expect(onClose).not.toHaveBeenCalled();
  });
});

describe("Databases: updates", () => {
  it("flags the instance and opens the update modal from its row", async () => {
    renderWithProviders(<Databases />, {
      url: "/services/redis",
      path: "/services/:engine",
    });
    expect(await screen.findByText("Update available")).toBeInTheDocument();
    expect(screen.getByText(/^Support: /)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Update" }));
    expect(
      await screen.findByRole("dialog", { name: "Update cache" }),
    ).toBeInTheDocument();
  });

  it("checks for updates and refreshes the config and the instances", async () => {
    renderWithProviders(<Databases />, {
      url: "/services/redis",
      path: "/services/:engine",
    });
    expect(
      await screen.findByText(/Versions checked .*ago/),
    ).toBeInTheDocument();
    const api = mockApi();
    await userEvent.click(
      screen.getByRole("button", { name: /Check for updates/ }),
    );
    await waitFor(() =>
      expect(api.calls("POST", "/services/updates/check")).toHaveLength(1),
    );
    await waitFor(() =>
      expect(api.calls("GET", "/services/config")).toHaveLength(2),
    );
    expect(toast.success).toHaveBeenCalledWith("Versions checked.");
  });

  it("warns about engines whose last check was incomplete", async () => {
    mockApi().state.servicesConfig.catalog = {
      checkedAt: new Date().toISOString(),
      failed: ["redis", "nats"],
    };
    renderWithProviders(<Databases />, {
      url: "/services/redis",
      path: "/services/:engine",
    });
    expect(
      await screen.findByText(/Last check incomplete for: Redis®, NATS®/),
    ).toBeInTheDocument();
  });

  it("says when a check just ran (throttled)", async () => {
    mockApi().on("POST", "/services/updates/check", {
      status: 200,
      body: { checkedAt: new Date().toISOString(), throttled: true },
    });
    renderWithProviders(<Databases />, {
      url: "/services/redis",
      path: "/services/:engine",
    });
    await userEvent.click(
      await screen.findByRole("button", { name: /Check for updates/ }),
    );
    await waitFor(() =>
      expect(toast.info).toHaveBeenCalledWith(
        "A check just ran: try again in a few seconds.",
      ),
    );
  });
});
