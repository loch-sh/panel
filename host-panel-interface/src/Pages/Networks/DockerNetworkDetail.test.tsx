import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderWithProviders } from "../../../tests/setup/render";
import DockerNetworkDetail from "./DockerNetworkDetail";

const render = (id: string) =>
  renderWithProviders(<DockerNetworkDetail />, {
    url: `/networks/docker/${id}`,
    path: "/networks/docker/:id",
  });

describe("DockerNetworkDetail", () => {
  it("shows the network and its containers, read-only", async () => {
    render("1".repeat(64));
    expect(
      await screen.findByRole("heading", { name: "traefik" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Compose: proxy")).toBeInTheDocument();
    expect(
      screen.getByText(/not managed by the panel: it is shown read-only/),
    ).toBeInTheDocument();
    expect(screen.getByText("172.18.0.0/16")).toBeInTheDocument();
    expect(screen.getByText("2 containers")).toBeInTheDocument();

    const site = (await screen.findByRole("link", { name: "shop" })).closest(
      "tr",
    ) as HTMLElement;
    expect(within(site).getByRole("link")).toHaveAttribute(
      "href",
      "/hosting/shop",
    );
    expect(within(site).getByText("Site")).toBeInTheDocument();
    expect(within(site).getByText("172.18.0.5")).toBeInTheDocument();
    // Docker declares nothing: no "connected but not declared" warning.
    expect(
      within(site).queryByText("Connected but not declared"),
    ).not.toBeInTheDocument();
    const proxy = screen.getByText("proxy-traefik-1").closest("tr")!;
    expect(within(proxy).getByText("Not managed")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Detach|Disconnect/ })).toBe(
      null,
    );
  });

  it("warns about a reserved range overlap", async () => {
    render("2".repeat(64));
    expect(
      await screen.findByText(/overlaps the reserved range 192\.168\.1\.0\/24/),
    ).toBeInTheDocument();
    expect(
      screen.getByText("No container on this network."),
    ).toBeInTheDocument();
  });

  it("says when the network does not exist", async () => {
    render("f".repeat(64));
    expect(
      await screen.findByText(/This Docker network does not exist/),
    ).toBeInTheDocument();
  });
});
