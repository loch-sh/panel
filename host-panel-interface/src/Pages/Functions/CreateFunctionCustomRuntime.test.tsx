import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { dialogTitled } from "../../../tests/setup/dialogs";
import { mockApi } from "../../../tests/setup/msw";
import { renderWithProviders } from "../../../tests/setup/render";
import {
  CUSTOM_HANDLER_PATTERN,
  goBuildCommand,
  handlerValid,
} from "../../Services/Functions/Models/functions.model";
import CreateFunction from "./CreateFunction";

// No ToastContainer in the tests: the toasts are checked on the calls.
const toast = vi.hoisted(() => ({
  success: vi.fn(),
  info: vi.fn(),
  warning: vi.fn(),
  error: vi.fn(),
}));
vi.mock("react-toastify", () => ({ toast }));

const zip = () =>
  new File([new Uint8Array([0x50, 0x4b, 0x03, 0x04])], "function.zip", {
    type: "application/zip",
  });

const render = () =>
  renderWithProviders(<CreateFunction />, { url: "/functions/new" });

const chooseCustomRuntime = async () => {
  render();
  const runtimes = await screen.findByRole("radiogroup", { name: "Runtime" });
  await userEvent.click(
    within(runtimes).getByRole("radio", {
      name: /Go, Rust… \(custom runtime\)/,
    }),
  );
};

describe("CreateFunction: custom runtime", () => {
  it("offers provided.al2023 with an optional handler and the build command", async () => {
    await chooseCustomRuntime();
    const versions = screen.getByRole("radiogroup", { name: "Version" });
    expect(
      within(versions).getByRole("radio", { name: /provided\.al2023/ }),
    ).toHaveAttribute("aria-checked", "true");
    const handler = screen.getByLabelText("Handler (optional)");
    expect(handler).toHaveValue("");
    expect(handler).toHaveAttribute("placeholder", "bootstrap");
    expect(screen.getByTestId("fn-build-command")).toHaveTextContent(
      goBuildCommand("arm64"),
    );
    expect(goBuildCommand("arm64")).toContain(
      "CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o bootstrap",
    );
    // An invalid handler is flagged with the custom runtime's rule.
    await userEvent.type(handler, "-bad");
    expect(
      await screen.findByText(/no part starting with \. or -/),
    ).toBeInTheDocument();
  });

  it("creates without a handler and reports the deployment warnings", async () => {
    mockApi().on("POST", "/functions/gofn/code", {
      status: 201,
      body: {
        id: "20261106T090000Z-00000001",
        treeHash: "e5".repeat(32),
        zipSha256: "f6".repeat(32),
        zipSize: 2048,
        files: 1,
        bytes: 6144,
        active: true,
        warnings: ["FUNCTION_BOOTSTRAP_DYNAMIC", "SOMETHING_UNKNOWN"],
      },
    });
    await chooseCustomRuntime();
    const input =
      document.querySelector<HTMLInputElement>('input[type="file"]');
    await userEvent.upload(input!, zip());
    await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "gofn");
    await userEvent.click(
      screen.getAllByRole("button", { name: "Create the function" })[0],
    );
    await dialogTitled("Invoke credentials");
    const [created] = mockApi().calls("POST", "/functions");
    expect(created.body).toMatchObject({
      name: "gofn",
      runtime: "provided",
      runtimeVersion: "al2023",
    });
    expect(created.body).not.toHaveProperty("handler");
    await waitFor(() =>
      expect(toast.warning).toHaveBeenCalledWith(
        expect.stringContaining("dynamically linked against glibc"),
      ),
    );
    expect(toast.warning).toHaveBeenCalledTimes(1);
  });

  it("shows the translated bootstrap errors", async () => {
    mockApi().on("POST", "/functions/gofn/code", {
      status: 400,
      body: { code: "FUNCTION_BOOTSTRAP_ARCH", error: "x" },
    });
    await chooseCustomRuntime();
    const input =
      document.querySelector<HTMLInputElement>('input[type="file"]');
    await userEvent.upload(input!, zip());
    await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "gofn");
    await userEvent.click(
      screen.getAllByRole("button", { name: "Create the function" })[0],
    );
    const dialog = await dialogTitled("Invoke credentials");
    expect(
      await within(dialog).findByText(/another processor architecture/),
    ).toBeInTheDocument();
  });
});

describe("custom runtime handler rule", () => {
  it("mirrors the API", () => {
    for (const ok of ["", "bootstrap", "my-func.v2", "pkg/handler"]) {
      expect(handlerValid("provided", ok)).toBe(true);
    }
    for (const bad of [
      "-x",
      ".hidden",
      "a/../b",
      "a//b",
      "/abs",
      "a b",
      "x$y",
    ]) {
      expect(CUSTOM_HANDLER_PATTERN.test(bad)).toBe(false);
      expect(handlerValid("provided", bad)).toBe(false);
    }
    expect(handlerValid("nodejs", "")).toBe(false);
    expect(handlerValid("provided", "a".repeat(129))).toBe(false);
  });
});
