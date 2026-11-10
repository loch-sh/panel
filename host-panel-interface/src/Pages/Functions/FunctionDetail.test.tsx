import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import {
  FUNCTION_SECRET_KEY,
  FUNCTION_TOKEN,
} from "../../../tests/mocks/fixtures";
import { dialogTitled } from "../../../tests/setup/dialogs";
import { mockApi } from "../../../tests/setup/msw";
import { renderWithProviders } from "../../../tests/setup/render";
import FunctionDetail from "./FunctionDetail";

const render = (name: string, tab?: string) =>
  renderWithProviders(<FunctionDetail />, {
    url: `/functions/${name}${tab ? `?tab=${tab}` : ""}`,
    path: "/functions/:name",
  });

const XSS =
  '<script>window.__pwned = true</script><img src=x onerror="window.__pwned = true">';

describe("FunctionDetail", () => {
  it("shows the internal URL, the pinned image and the limits", async () => {
    render("thumbs");
    expect(
      await screen.findByText(
        "http://fn-thumbs:8080/2015-03-31/functions/thumbs/invocations",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText("mirror.gcr.io/amazon/aws-lambda-nodejs:22-arm64"),
    ).toBeInTheDocument();
    expect(screen.getByText(`sha256:${"a1".repeat(32)}`)).toBeInTheDocument();
    expect(screen.getByText("Node.js 22 (arm64)")).toBeInTheDocument();
    expect(screen.getByText("Memory: 512 MB")).toBeInTheDocument();
  });

  it("explains a modified function and only offers to purge it", async () => {
    render("legacy");
    expect(await screen.findByText(/never runs it again/)).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Test" })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    expect(
      screen.getByRole("button", { name: "Purge legacy" }),
    ).toBeInTheDocument();
  });

  it("invokes with a JSON event and renders the response as text only", async () => {
    const api = mockApi();
    api.state.invoke = () => ({
      statusCode: 200,
      requestId: "req-42",
      durationMs: 37,
      payload: XSS,
      logTail: `INFO ${XSS}\nEND RequestId: req-42`,
    });
    render("thumbs", "test");
    const editor = await screen.findByLabelText("Event (JSON)");
    const invoke = screen.getByRole("button", { name: /Invoke/ });

    // Invalid JSON is refused before anything is sent.
    fireEvent.change(editor, { target: { value: "{not json" } });
    expect(
      await screen.findByText("This is not valid JSON."),
    ).toBeInTheDocument();
    expect(invoke).toBeDisabled();

    fireEvent.change(editor, { target: { value: '{"size": 128}' } });
    await waitFor(() => expect(invoke).toBeEnabled());
    await userEvent.click(invoke);

    const payload = await screen.findByLabelText("Response");
    expect(payload.textContent).toBe(XSS);
    expect(screen.getByLabelText("Logs (end)").textContent).toContain(XSS);
    // Text, never markup: no element was created from the function's output.
    expect(document.querySelector("script")).toBeNull();
    expect(document.querySelector('img[src="x"]')).toBeNull();
    expect((window as { __pwned?: boolean }).__pwned).toBeUndefined();
    expect(screen.getByText("37 ms")).toBeInTheDocument();
    expect(screen.getByText("req-42")).toBeInTheDocument();
    expect(api.calls("POST", "/functions/thumbs/invoke")[0].body).toEqual({
      size: 128,
    });
  });

  it("shows a function error and the API's invocation errors", async () => {
    const api = mockApi();
    api.state.invoke = () => ({
      statusCode: 200,
      functionError: "Unhandled",
      durationMs: 3,
      payload: '{"errorMessage":"boom"}',
      logTail: "",
    });
    render("thumbs", "test");
    await userEvent.click(
      await screen.findByRole("button", { name: /Invoke/ }),
    );
    expect(
      await screen.findByText("The function failed: Unhandled"),
    ).toBeInTheDocument();

    api.on("POST", "/functions/:name/invoke", {
      status: 429,
      body: { code: "FUNCTION_INVOKE_BUSY", error: "" },
    });
    await userEvent.click(screen.getByRole("button", { name: /Invoke/ }));
    expect(
      await screen.findByText(/busy with other invocations/),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("Response")).not.toBeInTheDocument();
  });

  it("rolls back to an older deployment after a confirmation", async () => {
    render("thumbs", "code");
    const older = (
      await screen.findByText("20261101T080000Z-0f0e0d0c")
    ).closest("tr") as HTMLElement;
    const current = screen
      .getByText("20261105T120000Z-1a2b3c4d")
      .closest("tr") as HTMLElement;
    expect(within(current).getByText("Active")).toBeInTheDocument();
    await userEvent.click(
      within(older).getByRole("button", { name: /Roll back/ }),
    );
    const dialog = await dialogTitled(/Roll back to deployment/);
    await userEvent.click(
      within(dialog).getByRole("button", { name: /Roll back/ }),
    );
    await waitFor(() =>
      expect(
        mockApi().calls(
          "POST",
          "/functions/thumbs/deployments/20261101T080000Z-0f0e0d0c/activate",
        ),
      ).toHaveLength(1),
    );
    await waitFor(() =>
      expect(
        within(
          screen
            .getByText("20261101T080000Z-0f0e0d0c")
            .closest("tr") as HTMLElement,
        ).getByText("Active"),
      ).toBeInTheDocument(),
    );
  });

  it("uploads a new zip as a deployment", async () => {
    render("thumbs", "code");
    await screen.findByText("Deploy new code");
    const input =
      document.querySelector<HTMLInputElement>('input[type="file"]');
    await userEvent.upload(
      input!,
      new File([new Uint8Array([0x50, 0x4b])], "v2.zip", {
        type: "application/zip",
      }),
    );
    await waitFor(() =>
      expect(mockApi().calls("POST", "/functions/thumbs/code")).toHaveLength(1),
    );
  });

  it("sets a secret without ever showing its value again", async () => {
    render("report", "secrets");
    // Names only.
    const existing = (await screen.findByText("DB_PASSWORD")).closest(
      "tr",
    ) as HTMLElement;
    expect(within(existing).getByText("Hidden")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /Add a secret/ }));
    const dialog = await dialogTitled("New secret");
    await userEvent.type(within(dialog).getByLabelText("Name"), "STRIPE_KEY");
    await userEvent.type(within(dialog).getByLabelText("Value"), "sk_live_123");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(
        mockApi().calls("PUT", "/functions/report/secrets/STRIPE_KEY"),
      ).toHaveLength(1),
    );
    expect(
      mockApi().calls("PUT", "/functions/report/secrets/STRIPE_KEY")[0].body,
    ).toEqual({ value: "sk_live_123" });
    expect(await screen.findByText("STRIPE_KEY")).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.queryByDisplayValue("sk_live_123")).not.toBeInTheDocument(),
    );
    expect(screen.queryByText("sk_live_123")).not.toBeInTheDocument();

    // Replacing asks for a new value, never shows the current one.
    await userEvent.click(
      screen.getByRole("button", { name: /Replace DB_PASSWORD/ }),
    );
    const replace = await dialogTitled("Replace the value of DB_PASSWORD");
    expect(within(replace).getByLabelText("Value")).toHaveValue("");
  });

  it("rotates the credentials, shows them once and removes them from the page once closed", async () => {
    render("report", "credentials");
    await userEvent.click(
      await screen.findByRole("button", { name: /Rotate the credentials/ }),
    );
    const confirm = await dialogTitled("Rotate the credentials of report?");
    await userEvent.click(
      within(confirm).getByRole("button", { name: /Rotate the credentials/ }),
    );
    const dialog = await dialogTitled("Invoke credentials");
    expect(
      within(dialog).getByText("LOCHFNACCESSKEY00001"),
    ).toBeInTheDocument();
    expect(
      mockApi().calls("POST", "/functions/report/credentials/rotate"),
    ).toHaveLength(1);

    // Revealed, then gone from the page (and its HTML) once closed.
    for (const b of within(dialog).getAllByRole("button", { name: /Show/ })) {
      await userEvent.click(b);
    }
    expect(within(dialog).getByText(FUNCTION_TOKEN)).toBeInTheDocument();
    expect(within(dialog).getByText(FUNCTION_SECRET_KEY)).toBeInTheDocument();
    await userEvent.click(
      within(dialog).getByRole("button", {
        name: "I have copied the credentials",
      }),
    );
    await waitFor(() =>
      expect(document.body.innerHTML).not.toContain(FUNCTION_TOKEN),
    );
    expect(document.body.innerHTML).not.toContain(FUNCTION_SECRET_KEY);
  });

  it("lists the scheduled invocations of the function", async () => {
    mockApi().state.cronJobs = [
      {
        id: "j1",
        name: "nightly",
        schedule: "0 3 * * *",
        type: "function_invoke" as never,
        enabled: true,
        once: false,
        functionInvoke: { function: "thumbs", event: '{"bucket":"media"}' },
        lastStatus: "success" as never,
        createdAt: "2026-11-01T00:00:00Z",
        updatedAt: "2026-11-01T00:00:00Z",
      },
    ];
    render("thumbs", "triggers");
    expect(await screen.findByText("nightly")).toBeInTheDocument();
    expect(screen.getByText('{"bucket":"media"}')).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: /Schedule an invocation/ }),
    );
    expect(screen.getByTestId("location")).toHaveTextContent("/cron");
  });

  it("renders every string from the API or the function as text", async () => {
    const api = mockApi();
    const thumbs = api.state.functions[0];
    thumbs.state = "unverified";
    thumbs.problem = XSS;
    thumbs.env = [{ name: "HTML", value: XSS }];
    render("thumbs");
    expect(await screen.findByText(XSS)).toBeInTheDocument();
    expect(document.querySelector("script")).toBeNull();
    expect(document.querySelector('img[src="x"]')).toBeNull();

    // The variable value is the value of an input, never markup.
    await userEvent.click(screen.getByRole("tab", { name: "Configuration" }));
    expect(
      await screen.findByRole("textbox", { name: "Value of variable 1" }),
    ).toHaveValue(XSS);

    // A function error.
    thumbs.state = "running";
    api.state.invoke = () => ({
      statusCode: 200,
      functionError: XSS,
      durationMs: 1,
      payload: "",
      logTail: "",
    });
    await userEvent.click(screen.getByRole("tab", { name: "Test" }));
    await userEvent.click(
      await screen.findByRole("button", { name: /Invoke/ }),
    );
    expect(
      await screen.findByText(`The function failed: ${XSS}`),
    ).toBeInTheDocument();
    expect(document.querySelector("script")).toBeNull();
    expect(document.querySelector('img[src="x"]')).toBeNull();
    expect((window as { __pwned?: boolean }).__pwned).toBeUndefined();
  });

  it("toggles the MCP guard-rail with the deploy scope note", async () => {
    render("report", "configuration");
    const toggle = await screen.findByRole("switch", {
      name: "Invocable by MCP clients",
    });
    expect(toggle).toHaveAttribute("aria-checked", "true");
    expect(screen.getByText(/not an access control/)).toBeInTheDocument();
    expect(screen.getAllByText(/functions:deploy/).length).toBeGreaterThan(0);
    await userEvent.click(toggle);
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    const dialog = await dialogTitled("Save the configuration of report?");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(mockApi().calls("PATCH", "/functions/report")).toHaveLength(1),
    );
    expect(mockApi().calls("PATCH", "/functions/report")[0].body).toEqual({
      mcpInvocable: false,
    });
  });
});
