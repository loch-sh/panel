import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ContainerInfo } from "dockerode";
import { describe, expect, it, vi } from "vitest";
import { container } from "../../../tests/mocks/fixtures";
import { renderWithProviders } from "../../../tests/setup/render";
import { groupByStack } from "./stackGroup";
import StackSidePanel from "./StackSidePanel";

/** A container of the "shop" Compose project, as Docker lists it. */
function service(
  name: string,
  svc: string,
  extra: Partial<ContainerInfo> = {},
  labels: Record<string, string> = {},
): ContainerInfo {
  const c = container(name, "shop");
  return {
    ...c,
    Labels: {
      ...c.Labels,
      "com.docker.compose.service": svc,
      "com.docker.compose.project.working_dir": "/srv/shop",
      "com.docker.compose.project.config_files":
        "/srv/shop/compose.yaml,/srv/shop/compose.override.yaml",
      "com.docker.compose.version": "2.40.3",
      ...labels,
    },
    ...extra,
  };
}

const web = service(
  "shop-web-1",
  "web",
  {
    Ports: [
      { IP: "0.0.0.0", PrivatePort: 80, PublicPort: 8080, Type: "tcp" },
      { IP: "::", PrivatePort: 80, PublicPort: 8080, Type: "tcp" },
    ],
    NetworkSettings: {
      Networks: {
        shop_default: { IPAddress: "172.21.0.3" },
      },
    } as unknown as ContainerInfo["NetworkSettings"],
  },
  { "com.docker.compose.depends_on": "db:service_healthy:false" },
);
const db = service("shop-db-1", "db", {
  State: "exited",
  Status: "Exited (1) 5 minutes ago",
  Mounts: [
    {
      Name: "shop_data",
      Type: "volume",
      Source: "/var/lib/docker/volumes/shop_data/_data",
      Destination: "/var/lib/postgresql/data",
      Driver: "local",
      Mode: "z",
      RW: true,
      Propagation: "",
    },
  ],
  NetworkSettings: {
    Networks: { shop_default: { IPAddress: "" } },
  } as unknown as ContainerInfo["NetworkSettings"],
});

function render(onOpenContainer = vi.fn()) {
  const [stack] = groupByStack([web, db], "No stack");
  renderWithProviders(
    <StackSidePanel
      stack={stack}
      onClose={() => {}}
      onOpenContainer={onOpenContainer}
    />,
  );
  return onOpenContainer;
}

describe("StackSidePanel", () => {
  it("summarizes the stack and lists its services", async () => {
    const user = userEvent.setup();
    const onOpen = render();
    const drawer = await screen.findByRole("dialog");
    expect(within(drawer).getByText("shop")).toBeInTheDocument();
    expect(within(drawer).getByText("1/2 active")).toBeInTheDocument();
    expect(within(drawer).getByText("Stopped with an error")).toBeInTheDocument();
    expect(within(drawer).getByText("1/2")).toBeInTheDocument();

    const webRow = within(drawer).getByText("web").closest("tr")!;
    expect(within(webRow).getByText("Running")).toBeInTheDocument();
    // The IPv4 and IPv6 bindings of one port count once.
    expect(within(webRow).getAllByText("8080→80/tcp")).toHaveLength(1);
    expect(within(webRow).getByText("db")).toBeInTheDocument();
    const dbRow = within(drawer).getAllByText("db")[0].closest("tr")!;
    expect(within(dbRow).getByText("Exited")).toBeInTheDocument();

    await user.click(
      within(webRow).getByRole("button", { name: "Container details" }),
    );
    expect(onOpen).toHaveBeenCalledWith(web.Id);
  });

  it("shows where the stack comes from, its networks and its mounts", async () => {
    const user = userEvent.setup();
    render();
    const drawer = await screen.findByRole("dialog");
    await user.click(within(drawer).getByRole("tab", { name: /Compose/ }));
    expect(await within(drawer).findByText("/srv/shop")).toBeInTheDocument();
    expect(within(drawer).getByText("/srv/shop/compose.yaml")).toBeInTheDocument();
    expect(
      within(drawer).getByText("/srv/shop/compose.override.yaml"),
    ).toBeInTheDocument();
    expect(within(drawer).getByText("2.40.3")).toBeInTheDocument();

    await user.click(within(drawer).getByRole("tab", { name: /Networks/ }));
    expect(await within(drawer).findByText("shop_default")).toBeInTheDocument();
    expect(within(drawer).getByText("172.21.0.3")).toBeInTheDocument();

    await user.click(within(drawer).getByRole("tab", { name: /Mounts/ }));
    expect(
      await within(drawer).findByText("/var/lib/postgresql/data"),
    ).toBeInTheDocument();
    expect(within(drawer).getByText("shop_data")).toBeInTheDocument();
  });

  it("links a hosted stack to its site", async () => {
    const site = container("hosted-blog-web-1", "hosted-blog");
    site.Labels["sh.loch.managed"] = "true";
    const [stack] = groupByStack([site], "No stack");
    expect(stack.hosted).toBe(true);
    renderWithProviders(
      <StackSidePanel stack={stack} onClose={() => {}} onOpenContainer={vi.fn()} />,
    );
    const drawer = await screen.findByRole("dialog");
    expect(within(drawer).getByText("blog")).toBeInTheDocument();
    expect(
      within(drawer).getByRole("link", { name: /Open the site/ }),
    ).toHaveAttribute("href", "/hosting/blog");
  });
});
