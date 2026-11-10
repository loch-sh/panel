import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { dialogTitled } from "../../../tests/setup/dialogs";
import { mockApi } from "../../../tests/setup/msw";
import { renderWithProviders } from "../../../tests/setup/render";
import FunctionsList from "./FunctionsList";

const render = () =>
  renderWithProviders(<FunctionsList />, { url: "/functions" });

const row = async (name: string) =>
  (await screen.findByRole("link", { name })).closest("tr") as HTMLElement;

describe("FunctionsList", () => {
  it("lists the functions with their runtime and state (limits on wider screens)", async () => {
    render();
    const thumbs = await row("thumbs");
    expect(within(thumbs).getByText("Node.js 22")).toBeInTheDocument();
    expect(within(thumbs).getByText("Running")).toBeInTheDocument();

    const report = await row("report");
    expect(within(report).getByText("Python 3.13")).toBeInTheDocument();
    expect(within(report).getByText("Stopped")).toBeInTheDocument();

    // Edited outside the panel: purge only.
    const legacy = await row("legacy");
    expect(
      within(legacy).getByText("Edited outside the panel"),
    ).toBeInTheDocument();
    expect(within(legacy).getByText("Purge only")).toBeInTheDocument();
    expect(
      within(legacy).getByRole("button", { name: "Purge legacy" }),
    ).toBeInTheDocument();
    expect(
      within(legacy).getByRole("button", { name: "Test legacy" }),
    ).toBeDisabled();
    expect(screen.getByText(/3 \/ 50 functions/)).toBeInTheDocument();
    // Compatibility wording and the trademark notice, never an affiliation.
    expect(
      screen.getByText(/handlers written for AWS Lambda™/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Loch\.sh is not affiliated with or endorsed by Amazon/),
    ).toBeInTheDocument();
  });

  it("stops a function after a confirmation", async () => {
    render();
    const thumbs = await row("thumbs");
    await userEvent.click(
      within(thumbs).getByRole("button", { name: "Stop thumbs" }),
    );
    const dialog = await dialogTitled("Stop thumbs?");
    expect(mockApi().calls("POST", "/functions/thumbs/stop")).toHaveLength(0);
    await userEvent.click(within(dialog).getByRole("button", { name: "Stop" }));
    await waitFor(() =>
      expect(mockApi().calls("POST", "/functions/thumbs/stop")).toHaveLength(1),
    );
  });

  it("purges a modified function once its name is typed", async () => {
    render();
    const legacy = await row("legacy");
    await userEvent.click(
      within(legacy).getByRole("button", { name: "Purge legacy" }),
    );
    const dialog = await dialogTitled("Purge the function legacy?");
    const submit = within(dialog).getByRole("button", { name: "Purge" });
    expect(submit).toBeDisabled();
    await userEvent.type(
      within(dialog).getByRole("textbox", { name: /Type the name/ }),
      "legacy",
    );
    await userEvent.click(submit);
    await waitFor(() =>
      expect(mockApi().calls("DELETE", "/functions/legacy")).toHaveLength(1),
    );
    expect(
      mockApi().calls("DELETE", "/functions/legacy")[0].query.get("purge"),
    ).toBe("true");
    await waitFor(() =>
      expect(
        screen.queryByRole("link", { name: "legacy" }),
      ).not.toBeInTheDocument(),
    );
  });

  it("presents the module when there is no function yet", async () => {
    mockApi().state.functions = [];
    render();
    expect(await screen.findByText("No function yet")).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: /Create a function/ }),
    );
    expect(screen.getByTestId("location")).toHaveTextContent("/functions/new");
  });

  it("says when the module is disabled", async () => {
    mockApi().state.functionsConfig = { enabled: false };
    render();
    expect(
      await screen.findByText(/functions module is disabled/),
    ).toBeInTheDocument();
    expect(mockApi().calls("GET", "/functions")).toHaveLength(0);
  });

  it("renders network names as text", async () => {
    const xss = '<img src=x onerror="window.__pwned = true">';
    mockApi().state.functions[0].networks = [xss];
    render();
    await row("thumbs");
    expect(document.querySelector('img[src="x"]')).toBeNull();
    expect((window as { __pwned?: boolean }).__pwned).toBeUndefined();
  });
});
