import { FUNCTION_TOKEN } from "../mocks/fixtures";
import { expect, test } from "./fixtures";

test.describe("functions", () => {
  test("creates a function, keeps its credentials once, deploys and tests it", async ({
    page,
    api,
    context,
  }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    api.withFunctionNetworks();

    // The menu entry, in the Serverless group.
    await page.goto("/");
    await page.getByRole("menuitem", { name: "Serverless" }).click();
    await page.getByRole("menuitem", { name: "Functions" }).click();
    await expect(page).toHaveURL("/functions");
    await expect(
      page.getByRole("link", { name: "thumbs", exact: true }),
    ).toBeVisible();
    await expect(
      page
        .getByRole("row")
        .filter({ hasText: "legacy" })
        .getByText("Purge only"),
    ).toBeVisible();

    await page.getByRole("button", { name: "New function" }).click();
    await expect(
      page.getByRole("heading", { name: "New function" }),
    ).toBeVisible();
    await page.getByRole("textbox", { name: "Name" }).fill("resize");
    await page.getByRole("button", { name: "Create the function" }).click();

    const dialog = page
      .getByRole("dialog")
      .filter({ hasText: "Invoke credentials" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText(FUNCTION_TOKEN)).toHaveCount(0);
    // Copy works on the masked token.
    await dialog
      .getByRole("row", { name: /Bearer token/ })
      .getByRole("button", { name: /copy/i })
      .click();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
      FUNCTION_TOKEN,
    );
    expect(api.calls("POST", "/functions")[0].body).toMatchObject({
      name: "resize",
      runtime: "nodejs",
    });
    await dialog
      .getByRole("button", { name: "I have copied the credentials" })
      .click();

    await expect(page).toHaveURL("/functions/resize");
    await expect(page.getByRole("heading", { name: "resize" })).toBeVisible();
    await expect(
      page.getByText(
        "http://fn-resize:8080/2015-03-31/functions/resize/invocations",
      ),
    ).toBeVisible();
    await expect(page.getByText(FUNCTION_TOKEN)).toHaveCount(0);

    // Deploy code: the function starts.
    await page.getByRole("tab", { name: "Code" }).click();
    await page.locator('input[type="file"]').setInputFiles({
      name: "code.zip",
      mimeType: "application/zip",
      buffer: Buffer.from([0x50, 0x4b, 0x05, 0x06]),
    });
    await expect(
      page
        .getByRole("row")
        .filter({ hasText: "20261106T090000Z" })
        .getByText("Active", { exact: true }),
    ).toBeVisible();
    expect(api.calls("POST", "/functions/resize/code")).toHaveLength(1);

    // Invoke: the response is shown as text.
    api.state.invoke = () => ({
      statusCode: 200,
      durationMs: 5,
      payload: "<b>hello</b>",
      logTail: "END",
    });
    await page.getByRole("tab", { name: "Test" }).click();
    await page.getByLabel("Event (JSON)").fill('{"width": 200}');
    await page.getByRole("button", { name: "Invoke" }).click();
    await expect(page.getByLabel("Response")).toHaveText("<b>hello</b>");
    await expect(page.locator("pre b")).toHaveCount(0);
    expect(api.calls("POST", "/functions/resize/invoke")[0].body).toEqual({
      width: 200,
    });
  });

  test("creates a Go function on the custom runtime and shows its deploy checks", async ({
    page,
    api,
  }) => {
    await page.goto("/functions/new");
    await page
      .getByRole("radio", { name: /Go, Rust… \(custom runtime\)/ })
      .click();
    await expect(
      page.getByRole("radio", { name: /provided\.al2023/ }),
    ).toBeChecked();
    const handler = page.getByLabel("Handler (optional)");
    await expect(handler).toHaveValue("");
    await expect(handler).toHaveAttribute("placeholder", "bootstrap");
    await expect(page.getByTestId("fn-build-command")).toContainText(
      "GOARCH=arm64 go build -tags lambda.norpc -o bootstrap",
    );
    await page.getByRole("textbox", { name: "Name" }).fill("gofn");
    await page.getByRole("button", { name: "Create the function" }).click();
    const dialog = page
      .getByRole("dialog")
      .filter({ hasText: "Invoke credentials" });
    await expect(dialog).toBeVisible();
    const body = api.calls("POST", "/functions")[0].body;
    expect(body).toMatchObject({
      runtime: "provided",
      runtimeVersion: "al2023",
    });
    expect(body).not.toHaveProperty("handler");
    await dialog
      .getByRole("button", { name: "I have copied the credentials" })
      .click();
    await expect(page).toHaveURL("/functions/gofn");
    await expect(
      page
        .getByRole("tabpanel")
        .getByText("Go, Rust… (custom runtime, provided.al2023)"),
    ).toBeVisible();

    // A binary of the other architecture: the translated error.
    await page.getByRole("tab", { name: "Code" }).click();
    await expect(
      page.getByText(/bootstrap executable at its root/),
    ).toBeVisible();
    api.on("POST", "/functions/gofn/code", {
      status: 400,
      body: { code: "FUNCTION_BOOTSTRAP_ARCH", error: "x" },
    });
    const upload = () =>
      page.locator('input[type="file"]').setInputFiles({
        name: "function.zip",
        mimeType: "application/zip",
        buffer: Buffer.from([0x50, 0x4b, 0x05, 0x06]),
      });
    await upload();
    await expect(
      page.getByText(/another processor architecture than the function's/),
    ).toBeVisible();

    // A dynamic binary: deployed, with a warning.
    const dynamic = {
      id: "20261106T090000Z-00000001",
      treeHash: "e5".repeat(32),
      zipSha256: "f6".repeat(32),
      zipSize: 2048,
      files: 1,
      bytes: 6144,
      active: true,
      warnings: ["FUNCTION_BOOTSTRAP_DYNAMIC"],
    };
    api.on("POST", "/functions/gofn/code", () => {
      const fn = api.state.functions.find((f) => f.name === "gofn")!;
      fn.deployments = [dynamic];
      fn.deployment = dynamic.id;
      fn.state = "running";
      return { status: 201, body: dynamic };
    });
    await upload();
    await expect(
      page
        .getByRole("alert")
        .filter({ hasText: /dynamically linked against glibc/ }),
    ).toBeVisible();
    await expect(
      page
        .getByRole("row")
        .filter({ hasText: "20261106T090000Z-00000001" })
        .getByText("Warning", { exact: true }),
    ).toBeVisible();

    // The handler stays optional in the configuration.
    await page.getByRole("tab", { name: "Configuration" }).click();
    await expect(page.getByLabel("Handler (optional)")).toHaveValue(
      "bootstrap",
    );
  });
});
