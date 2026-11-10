import { S3_ACCESS_KEY, S3_SECRET } from "../mocks/fixtures";
import { expect, test } from "./fixtures";

test.describe("managed services", () => {
  test("creates a Loch S3 instance and shows its credentials once", async ({
    page,
    api,
    context,
  }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await page.goto("/services/s3/new");
    await expect(
      page.getByRole("heading", { name: "New Loch S3 instance" }),
    ).toBeVisible();

    await page
      .getByRole("radiogroup", { name: "Memory" })
      .getByRole("radio", { name: /1 GB/ })
      .click();
    await page.getByRole("textbox", { name: "Name" }).fill("media");
    await page.getByRole("button", { name: "Create" }).first().click();

    const dialog = page.getByRole("dialog", { name: "Connection details" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText(S3_ACCESS_KEY)).toBeVisible();
    await expect(dialog.getByText("http://svc-s3-media:8080")).toBeVisible();
    // The secret is masked until revealed.
    await expect(dialog.getByText(S3_SECRET)).toHaveCount(0);

    // The Laravel .env lines carry the bucket and the path-style flag.
    await dialog
      .getByText(/Laravel \.env/)
      .getByRole("button")
      .click();
    const env = await page.evaluate(() => navigator.clipboard.readText());
    expect(env).toContain(`AWS_SECRET_ACCESS_KEY=${S3_SECRET}`);
    expect(env).toContain("AWS_BUCKET=media");
    expect(env).toContain("AWS_USE_PATH_STYLE_ENDPOINT=true");

    expect(api.calls("POST", "/services/resources")).toHaveLength(1);
    const [created] = api.calls("POST", "/services/resources");
    expect(created.body).toEqual({
      engine: "s3",
      version: "1.4",
      name: "media",
      options: { memory: "1024", quota: "10240", encryption: "off" },
      // A dedicated instance joins the shared network by default.
      networks: ["services"],
    });

    await dialog
      .getByRole("button", { name: "I have copied the password" })
      .click();
    await expect(page).toHaveURL("/services/s3");
    await expect(
      page.getByRole("heading", { name: "Loch S3 instances" }),
    ).toBeVisible();
    await expect(
      page.getByRole("row").filter({ hasText: "svc-s3-media:8080" }),
    ).toHaveCount(1);
    expect(api.calls("POST", "/services/resources")).toHaveLength(1);
  });

  test("edits a Redis instance: only the changed mutable options are sent", async ({
    page,
    api,
  }) => {
    await page.goto("/services/redis");
    await page.getByRole("button", { name: "Edit" }).click();
    await expect(page).toHaveURL("/services/redis/cache/edit");
    await expect(
      page.getByRole("heading", { name: "Edit cache" }),
    ).toBeVisible();
    // Persistence cannot change after creation.
    await expect(page.getByText("Set at creation")).toBeVisible();

    await page
      .getByRole("radiogroup", { name: "Memory" })
      .getByRole("radio", { name: /512 MB/ })
      .click();
    await page.getByRole("button", { name: "Save" }).first().click();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Save" })
      .click();

    await expect(page.getByText("Instance updated.")).toBeVisible();
    expect(api.calls("PATCH", "/services/instances/redis-cache")).toHaveLength(
      1,
    );
    const [patch] = api.calls("PATCH", "/services/instances/redis-cache");
    expect(patch.body).toEqual({ options: { memory: "512" } });
    await expect(page).toHaveURL("/services/redis");
  });

  test("shows the translated API error when the name is taken", async ({
    page,
    api,
  }) => {
    api.on("POST", "/services/resources", {
      status: 409,
      body: { code: "RESOURCE_EXISTS", error: "Resource already exists" },
    });
    await page.goto("/services/s3/new");
    await page.getByRole("textbox", { name: "Name" }).fill("assets");
    await page.getByRole("button", { name: "Create" }).first().click();
    await expect(
      page.getByRole("alert").filter({ hasText: /already exists/i }),
    ).toBeVisible();
    await expect(
      page.getByRole("dialog", { name: "Connection details" }),
    ).toHaveCount(0);
  });

  test("deletes an instance edited by hand after typing its name", async ({
    page,
    api,
  }) => {
    const redis = api.state.instances.find((i) => i.key === "redis-cache")!;
    redis.modified = true;
    await page.goto("/services/redis");
    await expect(page.getByText("Edited by hand")).toBeVisible();
    // Edited by hand: no edit, rotation or usual deletion.
    await expect(page.getByRole("button", { name: "Edit" })).toHaveCount(0);
    await page.getByRole("button", { name: "Delete", exact: true }).click();

    const dialog = page.getByRole("dialog", {
      name: "Delete svc-redis-cache?",
    });
    const submit = dialog.getByRole("button", { name: "Delete permanently" });
    await expect(submit).toBeDisabled();
    await dialog.getByRole("textbox").fill("svc-redis");
    await expect(submit).toBeDisabled();
    await dialog.getByRole("textbox").fill("svc-redis-cache");
    await submit.click();

    await expect(page.getByText("Instance deleted.")).toBeVisible();
    const calls = api.calls("DELETE", "/services/instances/redis-cache");
    expect(calls).toHaveLength(1);
    expect(calls[0].query.get("purge")).toBe("true");
    await expect(page.getByText("Edited by hand")).toHaveCount(0);
  });

  test("creates a PostgreSQL database with the legacy authentication", async ({
    page,
    api,
  }) => {
    await page.goto("/services/postgres/new");
    await page.getByRole("textbox", { name: "Name" }).fill("oldcms");
    const legacy = page.getByRole("switch", { name: "Legacy authentication" });
    await expect(legacy).not.toBeChecked();
    await legacy.click();
    await expect(page.getByText(/Weaker mechanism/)).toBeVisible();
    await expect(page.getByText("md5 (legacy)")).toBeVisible();
    await page.getByRole("button", { name: "Create" }).first().click();
    await expect(
      page.getByRole("dialog", { name: "Connection details" }),
    ).toBeVisible();
    const calls = api.calls("POST", "/services/resources");
    expect(calls).toHaveLength(1);
    expect(calls[0].body).toMatchObject({
      engine: "postgres",
      name: "oldcms",
      legacyAuth: true,
    });
  });

  test("offers no legacy authentication on dedicated engines", async ({
    page,
  }) => {
    await page.goto("/services/redis/new");
    await expect(page.getByRole("textbox", { name: "Name" })).toBeVisible();
    await expect(
      page.getByRole("switch", { name: "Legacy authentication" }),
    ).toHaveCount(0);
  });

  test("shows a published port where TLS is only offered", async ({
    page,
    api,
  }) => {
    const pg = api.state.instances.find((i) => i.key === "postgres-17")!;
    pg.exposure = {
      address: "0.0.0.0",
      port: 5417,
      tls: true,
      tlsOptional: true,
    };
    await page.goto("/services/postgres");
    await expect(
      page.getByText(/0\.0\.0\.0:5417 · TLS optional/),
    ).toBeVisible();
  });

  test("creates an instance with a custom size in GB", async ({
    page,
    api,
  }) => {
    await page.goto("/services/s3/new");
    const quota = page.getByRole("radiogroup", { name: "Storage quota" });
    await quota.getByRole("radio", { name: /Custom/ }).click();
    const input = page.getByRole("spinbutton", { name: /Storage quota/ });
    await input.fill("25");
    await input.blur();
    await expect(page.getByText("25 GB").first()).toBeVisible();
    await page.getByRole("textbox", { name: "Name" }).fill("bigdata");
    await page.getByRole("button", { name: "Create" }).first().click();
    await expect(
      page.getByRole("dialog", { name: "Connection details" }),
    ).toBeVisible();
    const [created] = api.calls("POST", "/services/resources");
    expect(created.body).toMatchObject({
      options: { quota: "25600", memory: "256" },
    });
  });

  test("edits an instance that already has a custom size", async ({
    page,
    api,
  }) => {
    const redis = api.state.instances.find((i) => i.key === "redis-cache")!;
    redis.options = { ...redis.options, memory: "3072" };
    await page.goto("/services/redis/cache/edit");
    const input = page.getByRole("spinbutton", { name: /Memory/ });
    await expect(input).toHaveValue("3");
    await input.fill("4.5");
    await input.blur();
    await page.getByRole("button", { name: "Save" }).first().click();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Save" })
      .click();
    await expect(page.getByText("Instance updated.")).toBeVisible();
    const [patch] = api.calls("PATCH", "/services/instances/redis-cache");
    expect(patch.body).toEqual({ options: { memory: "4608" } });
  });

  test("keeps the custom input when the size typed is an offered one", async ({
    page,
  }) => {
    await page.goto("/services/s3/new");
    const memory = page.getByRole("radiogroup", { name: "Memory" });
    await memory.getByRole("radio", { name: /Custom/ }).click();
    const input = page.getByRole("spinbutton", { name: /Memory/ });
    await input.fill("1");
    await input.blur();
    // 1 GB is an offered value: "Custom" stays selected, the input stays.
    await expect(input).toBeVisible();
    await expect(memory.getByRole("radio", { name: /Custom/ })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    await expect(memory.getByRole("radio", { name: /^1 GB/ })).toHaveAttribute(
      "aria-checked",
      "false",
    );
    // Choosing an offered card leaves the custom mode.
    await memory.getByRole("radio", { name: /^512 MB/ }).click();
    await expect(input).toHaveCount(0);
  });

  test("creates a Loch S3 instance with server-side encryption", async ({
    page,
    api,
  }) => {
    await page.goto("/services/s3/new");
    const enc = page.getByRole("radiogroup", {
      name: "Server-side encryption",
    });
    await expect(enc.getByRole("radio", { name: /Disabled/ })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    await enc.getByRole("radio", { name: /Enabled/ }).click();
    await page.getByRole("textbox", { name: "Name" }).fill("secure");
    await page.getByRole("button", { name: "Create" }).first().click();
    await expect(
      page.getByRole("dialog", { name: "Connection details" }),
    ).toBeVisible();
    const [created] = api.calls("POST", "/services/resources");
    expect(created.body).toMatchObject({ options: { encryption: "on" } });
  });

  test("creates an ElasticMQ instance: warned, no password", async ({
    page,
    api,
  }) => {
    await page.goto("/services/elasticmq/new");
    await expect(
      page.getByText(/ElasticMQ has no authentication/).first(),
    ).toBeVisible();
    await page.getByRole("textbox", { name: "Name" }).fill("jobs");
    // No authentication: a private network is required.
    await page.getByRole("button", { name: "Create" }).first().click();
    await expect(
      page
        .locator(".ant-form-item-explain-error")
        .filter({ hasText: /cannot join the shared network/ }),
    ).toBeVisible();
    expect(api.calls("POST", "/services/resources")).toHaveLength(0);
    await page.getByRole("combobox", { name: "Networks" }).click();
    await page
      .locator(".ant-select-item-option")
      .filter({ hasText: "backend" })
      .click();
    await page.keyboard.press("Escape");
    await page.getByRole("button", { name: "Create" }).first().click();
    const dialog = page.getByRole("dialog", { name: "Connection details" });
    await expect(dialog.getByText(/No authentication/)).toBeVisible();
    await expect(
      dialog.getByText("http://svc-elasticmq-jobs:9324", { exact: true }),
    ).toBeVisible();
    await dialog
      .locator(".ant-modal-footer")
      .getByRole("button", { name: "Close" })
      .click();
    expect(api.calls("POST", "/services/resources")[0].body).toMatchObject({
      networks: ["backend"],
    });
    await expect(page).toHaveURL("/services/elasticmq");
    // No password to reset on an engine without authentication.
    await expect(page.getByRole("button", { name: "Edit" })).toBeVisible();
    await expect(
      page.getByRole("button", { name: "New password" }),
    ).toHaveCount(0);
  });

  test("presents an engine without instances and links to its creation", async ({
    page,
  }) => {
    await page.goto("/services/s3");
    await expect(
      page.getByText(/Loch S3 is object storage compatible/),
    ).toBeVisible();
    await page.getByRole("button", { name: "Create an instance" }).click();
    await expect(page).toHaveURL("/services/s3/new");
  });
});
