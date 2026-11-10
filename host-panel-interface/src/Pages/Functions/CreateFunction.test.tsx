import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import {
  FUNCTION_ACCESS_KEY,
  FUNCTION_SECRET_KEY,
  FUNCTION_TOKEN,
} from "../../../tests/mocks/fixtures";
import { dialogTitled } from "../../../tests/setup/dialogs";
import { mockApi } from "../../../tests/setup/msw";
import { renderWithProviders } from "../../../tests/setup/render";
import CreateFunction from "./CreateFunction";

const render = () =>
  renderWithProviders(<CreateFunction />, { url: "/functions/new" });

const zip = () =>
  new File([new Uint8Array([0x50, 0x4b, 0x03, 0x04])], "code.zip", {
    type: "application/zip",
  });

describe("CreateFunction", () => {
  it("creates a function, sets its secrets and code, and shows the credentials once", async () => {
    render();
    expect(
      await screen.findByRole("heading", { name: "New function" }),
    ).toBeInTheDocument();
    // Defaults: the latest Node.js and its usual handler.
    const versions = screen.getByRole("radiogroup", { name: "Version" });
    expect(
      within(versions).getByRole("radio", { name: /Node\.js 26/ }),
    ).toHaveAttribute("aria-checked", "true");
    expect(screen.getByLabelText("Handler")).toHaveValue("index.handler");

    const input =
      document.querySelector<HTMLInputElement>('input[type="file"]');
    expect(input).not.toBeNull();
    await userEvent.upload(input!, zip());
    expect(await screen.findAllByText("code.zip")).not.toHaveLength(0);

    await userEvent.click(screen.getByRole("button", { name: /Add a secret/ }));
    await userEvent.type(
      screen.getByRole("textbox", { name: "Name of secret 1" }),
      "API_KEY",
    );
    await userEvent.type(
      screen.getByLabelText("Value of secret 1"),
      "s3cr3t-value",
    );
    await userEvent.type(
      screen.getByRole("textbox", { name: "Name" }),
      "resize",
    );
    await userEvent.click(
      screen.getAllByRole("button", { name: "Create the function" })[0],
    );

    const dialog = await dialogTitled("Invoke credentials");
    const [created] = mockApi().calls("POST", "/functions");
    expect(created.body).toEqual({
      name: "resize",
      runtime: "nodejs",
      runtimeVersion: "26",
      handler: "index.handler",
      memoryMB: 512,
      cpus: 1,
      timeoutSeconds: 30,
      tmpSizeMB: 512,
    });
    // The token is masked until revealed; no SigV4 pair by default.
    expect(within(dialog).queryByText(FUNCTION_TOKEN)).not.toBeInTheDocument();
    expect(
      within(dialog).queryByText(FUNCTION_ACCESS_KEY),
    ).not.toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: /Show/ }));
    expect(within(dialog).getByText(FUNCTION_TOKEN)).toBeInTheDocument();
    expect(
      within(dialog).getByText(
        "http://fn-resize:8080/2015-03-31/functions/resize/invocations",
      ),
    ).toBeInTheDocument();

    // Secret, then code, after the creation.
    await waitFor(() =>
      expect(mockApi().calls("POST", "/functions/resize/code")).toHaveLength(1),
    );
    expect(
      mockApi().calls("PUT", "/functions/resize/secrets/API_KEY")[0].body,
    ).toEqual({ value: "s3cr3t-value" });
    // The secret value left the page with the request.
    expect(screen.queryByDisplayValue("s3cr3t-value")).not.toBeInTheDocument();

    const close = within(dialog).getByRole("button", {
      name: "I have copied the credentials",
    });
    await waitFor(() => expect(close).toBeEnabled());
    await userEvent.click(close);
    expect(screen.getByTestId("location")).toHaveTextContent(
      "/functions/resize",
    );
    // The credentials left the page with the dialog.
    await waitFor(() =>
      expect(document.body.innerHTML).not.toContain(FUNCTION_TOKEN),
    );
  });

  it("asks for SigV4 and egress explicitly, with their warnings", async () => {
    render();
    await screen.findByRole("heading", { name: "New function" });
    await userEvent.click(screen.getByRole("radio", { name: /^Python/ }));
    expect(screen.getByLabelText("Handler")).toHaveValue(
      "lambda_function.lambda_handler",
    );
    await userEvent.click(
      screen.getByRole("switch", { name: "SigV4 callers (AWS SDK)" }),
    );
    expect(
      screen.getByText(/the function's code can read it/),
    ).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("switch", { name: "Internet access" }),
    );
    expect(
      screen.getByText(/send its data and secrets anywhere/),
    ).toBeInTheDocument();
    await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "etl");
    await userEvent.click(
      screen.getAllByRole("button", { name: "Create the function" })[0],
    );
    const dialog = await dialogTitled("Invoke credentials");
    expect(mockApi().calls("POST", "/functions")[0].body).toMatchObject({
      runtime: "python",
      runtimeVersion: "3.14",
      sigv4: true,
      egress: true,
    });
    expect(within(dialog).getByText(FUNCTION_ACCESS_KEY)).toBeInTheDocument();
    expect(
      within(dialog).queryByText(FUNCTION_SECRET_KEY),
    ).not.toBeInTheDocument();
  });

  it("refuses an invalid name and a reserved variable before sending", async () => {
    render();
    await screen.findByRole("heading", { name: "New function" });
    await userEvent.click(
      screen.getByRole("button", { name: /Add a variable/ }),
    );
    await userEvent.type(
      screen.getByRole("textbox", { name: "Name of variable 1" }),
      "AWS_REGION",
    );
    expect(screen.getByText(/reserved by the runtime/)).toBeInTheDocument();
    await userEvent.type(
      screen.getByRole("textbox", { name: "Name" }),
      "Bad_Name",
    );
    await userEvent.click(
      screen.getAllByRole("button", { name: "Create the function" })[0],
    );
    expect(
      await screen.findByText(/1 to 21 lowercase letters/, {
        selector: ".ant-form-item-explain-error",
      }),
    ).toBeInTheDocument();
    expect(mockApi().calls("POST", "/functions")).toHaveLength(0);
  });
});
