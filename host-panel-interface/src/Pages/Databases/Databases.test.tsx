import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { restrictedUser } from "../../../tests/mocks/fixtures";
import { renderWithProviders } from "../../../tests/setup/render";
import { mockApi } from "../../../tests/setup/msw";
import Databases from "./Databases";

describe("Databases (list of a service)", () => {
  it("lists the dedicated instances of an engine from the API", async () => {
    renderWithProviders(<Databases />, {
      url: "/services/redis",
      path: "/services/:engine",
    });
    expect(
      await screen.findByRole("heading", { name: "Redis® instances" }),
    ).toBeInTheDocument();
    // The host column is only shown from the md breakpoint (not in jsdom).
    expect(
      await screen.findByRole("cell", { name: /cache/ }),
    ).toBeInTheDocument();
    expect(mockApi().calls("GET", "/services/instances")).toHaveLength(1);
  });

  it("opens the edit page of an instance", async () => {
    renderWithProviders(<Databases />, {
      url: "/services/redis",
      path: "/services/:engine",
    });
    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent(
        "/services/redis/cache/edit",
      ),
    );
  });

  it("is forbidden to restricted users, without loading the services", async () => {
    mockApi().state.me = restrictedUser;
    renderWithProviders(<Databases />, {
      url: "/services/redis",
      path: "/services/:engine",
    });
    await waitFor(() =>
      expect(mockApi().calls("GET", "/users/me")).toHaveLength(1),
    );
    expect(await screen.findByText(/admin/i)).toBeInTheDocument();
    expect(mockApi().calls("GET", "/services/config")).toHaveLength(0);
  });
});
