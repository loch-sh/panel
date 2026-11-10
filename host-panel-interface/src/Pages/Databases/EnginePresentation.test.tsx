import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderWithProviders } from "../../../tests/setup/render";
import { mockApi } from "../../../tests/setup/msw";
import Databases from "./Databases";

const S3_PRESENTATION =
  /Loch S3 is object storage compatible with the Amazon S3 API/;
const POSTGRES_PRESENTATION =
  /PostgreSQL® is an open-source relational database/;

describe("Databases (presentation of an engine without resources)", () => {
  it("presents the engine and offers to create the first instance", async () => {
    // The fixtures have no Loch S3 instance.
    renderWithProviders(<Databases />, {
      url: "/services/s3",
      path: "/services/:engine",
    });
    expect(await screen.findByText(S3_PRESENTATION)).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Loch S3", level: 5 }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();

    await userEvent.click(
      screen.getByRole("button", { name: /Create an instance/ }),
    );
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent(
        "/services/s3/new",
      ),
    );
  });

  it("uses the vocabulary of a shared engine", async () => {
    mockApi().state.instances = mockApi().state.instances.filter(
      (i) => i.engine !== "postgres",
    );
    renderWithProviders(<Databases />, {
      url: "/services/postgres",
      path: "/services/:engine",
    });
    expect(await screen.findByText(POSTGRES_PRESENTATION)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Create a database/ }),
    ).toBeInTheDocument();
  });

  it("is not shown when the engine has instances", async () => {
    renderWithProviders(<Databases />, {
      url: "/services/postgres",
      path: "/services/:engine",
    });
    expect(await screen.findByRole("table")).toBeInTheDocument();
    await waitFor(() =>
      expect(mockApi().calls("GET", "/services/instances")).toHaveLength(1),
    );
    expect(
      await screen.findByRole("cell", { name: /PostgreSQL® 17/ }),
    ).toBeInTheDocument();
    expect(screen.queryByText(POSTGRES_PRESENTATION)).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Create a database/ }),
    ).not.toBeInTheDocument();
  });

  it("is not shown when the instances cannot be loaded", async () => {
    mockApi().on("GET", "/services/instances", {
      status: 500,
      body: { code: "INTERNAL", error: "boom" },
    });
    renderWithProviders(<Databases />, {
      url: "/services/s3",
      path: "/services/:engine",
    });
    await waitFor(() =>
      expect(mockApi().calls("GET", "/services/instances")).toHaveLength(1),
    );
    expect(await screen.findByRole("table")).toBeInTheDocument();
    expect(screen.queryByText(S3_PRESENTATION)).not.toBeInTheDocument();
  });
});
