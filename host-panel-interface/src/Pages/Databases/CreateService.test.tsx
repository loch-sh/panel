import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderWithProviders } from "../../../tests/setup/render";
import { mockApi } from "../../../tests/setup/msw";
import CreateService from "./CreateService";

const editRedis = () =>
  renderWithProviders(<CreateService />, {
    url: "/services/redis/cache/edit",
    path: "/services/:engine/:name/edit",
  });

describe("CreateService (edition of a dedicated instance)", () => {
  it("shows the version of the instance even when it is no longer offered", async () => {
    // The discovered catalog moved on: 8.4, run by the instance, is gone.
    const redis = mockApi().state.servicesConfig.engines!.find(
      (e) => e.id === "redis",
    )!;
    redis.versions = [
      {
        version: "8.10",
        image: "mirror.gcr.io/library/redis:8.10-alpine",
        platform: "",
        deprecated: false,
      },
    ];
    editRedis();

    // The version card is the instance's own, selected, with its support dates.
    const versions = await screen.findByRole("radiogroup", { name: "Version" });
    const radios = within(versions).getAllByRole("radio");
    expect(radios).toHaveLength(1);
    expect(radios[0]).toHaveTextContent("Redis® 8.4");
    expect(radios[0]).toHaveAttribute("aria-checked", "true");
    expect(radios[0]).toHaveTextContent(/Security: /);
    expect(screen.queryByText("Redis® 8.10")).not.toBeInTheDocument();

    // The form still works: an option change is saved.
    await userEvent.click(await screen.findByText("512 MB"));
    await userEvent.click(screen.getAllByRole("button", { name: "Save" })[0]);
    await userEvent.click(
      await within(await screen.findByRole("dialog")).findByRole("button", {
        name: "Save",
      }),
    );
    await waitFor(() =>
      expect(
        mockApi().calls("PATCH", "/services/instances/redis-cache"),
      ).toHaveLength(1),
    );
    expect(
      mockApi().calls("PATCH", "/services/instances/redis-cache")[0].body,
    ).toEqual({ options: { memory: "512" } });
  });

  it("shows the offered version entry when the instance version is offered", async () => {
    editRedis();
    const versions = await screen.findByRole("radiogroup", { name: "Version" });
    const radios = within(versions).getAllByRole("radio");
    expect(radios).toHaveLength(1);
    expect(radios[0]).toHaveTextContent("Redis® 8.4");
  });
});
