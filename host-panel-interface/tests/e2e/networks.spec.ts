import { hostingStack } from "../mocks/fixtures";
import { expect, test } from "./fixtures";

test.describe("private networks", () => {
  test("creates a network, attaches a site and shows its address", async ({
    page,
    api,
  }) => {
    api.state.stacks = [hostingStack("blog")];

    await page.goto("/networks");
    await expect(
      page.getByRole("heading", { name: "Private networks" }),
    ).toBeVisible();
    await page.getByRole("button", { name: "New network" }).click();
    await expect(page).toHaveURL("/networks/new");

    await page.getByRole("textbox", { name: "Name" }).fill("front");
    await page.getByRole("radio", { name: /Manual subnet/ }).click();
    await page
      .getByRole("textbox", { name: "Subnet (CIDR)" })
      .fill("10.40.0.0/24");
    await expect(page.getByText("253 usable addresses")).toBeVisible();
    await page
      .getByRole("button", { name: "Create the network" })
      .first()
      .click();

    await expect(page).toHaveURL("/networks/front");
    await expect(page.getByText("Network front created.")).toBeVisible();
    await expect(
      page.getByText("loch-net-front", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("No member yet: attach a site or a service instance."),
    ).toBeVisible();

    await page.getByRole("button", { name: "Attach a resource" }).click();
    const dialog = page.locator(".ant-modal").filter({
      hasText: "Attach a resource to front",
    });
    await dialog.getByRole("combobox").click();
    await page
      .locator(".ant-select-item-option")
      .filter({ hasText: "blog" })
      .click();
    await expect(dialog.getByText(/The site blog is redeployed/)).toBeVisible();
    await dialog.getByRole("button", { name: "Attach" }).click();

    await expect(page.getByText(/blog is being redeployed/)).toBeVisible();
    await expect(
      page.getByText("blog was redeployed with its new networks."),
    ).toBeVisible();
    const row = page.getByRole("row").filter({ hasText: "blog" });
    await expect(row.getByText("10.40.0.2")).toBeVisible();
    await expect(row.getByText("hosted-blog-web-1")).toBeVisible();
    expect(api.calls("POST", "/networks")[0].body).toEqual({
      name: "front",
      subnet: "10.40.0.0/24",
    });
    expect(api.calls("POST", "/networks/front/members")[0].body).toEqual({
      kind: "site",
      name: "blog",
    });

    // Back to the list: the network is there with its member.
    await page.getByRole("button", { name: "Back to the networks" }).click();
    const listRow = page.getByRole("row").filter({ hasText: "front" });
    await expect(listRow.getByText("10.40.0.0/24")).toBeVisible();
    await expect(
      listRow.getByRole("cell", { name: "1", exact: true }),
    ).toBeVisible();
  });

  test("connects and disconnects a network from the site page", async ({
    page,
    api,
  }) => {
    api.state.stacks = [hostingStack("shop")];

    await page.goto("/hosting/shop");
    await page.getByRole("tab", { name: "Networks" }).click();
    const backend = page.getByRole("row").filter({ hasText: "backend" });
    await expect(backend.getByText("Declared but not connected")).toBeVisible();
    // The other Docker networks of the container, read-only.
    await expect(
      page
        .getByRole("row")
        .filter({ hasText: "traefik" })
        .getByText("172.18.0.5"),
    ).toBeVisible();

    await page.getByRole("button", { name: "Connect a network" }).click();
    const dialog = page.locator(".ant-modal").filter({
      hasText: "Connect shop to a network",
    });
    await dialog.getByRole("combobox").click();
    await page
      .locator(".ant-select-item-option")
      .filter({ hasText: "services" })
      .click();
    await expect(dialog.getByText(/The site shop is redeployed/)).toBeVisible();
    await dialog.getByRole("button", { name: "Connect" }).click();

    await expect(page.getByText(/shop is being redeployed/)).toBeVisible();
    const services = page.getByRole("row").filter({ hasText: "services" });
    await expect(services.getByText("Connected")).toBeVisible();
    expect(api.calls("POST", "/networks/services/members")[0].body).toEqual({
      kind: "site",
      name: "shop",
    });

    await backend.getByRole("button", { name: "Disconnect" }).click();
    await page
      .locator(".ant-modal-confirm")
      .getByRole("button", { name: "Disconnect" })
      .click();
    await expect(backend).toHaveCount(0);
    expect(
      api.calls("DELETE", "/networks/backend/members/site/shop"),
    ).toHaveLength(1);
  });

  test("creates a network with a site as member", async ({ page, api }) => {
    api.state.stacks = [hostingStack("blog")];

    await page.goto("/networks/new");
    await page.getByRole("textbox", { name: "Name" }).fill("front");
    await page
      .getByRole("combobox", { name: "Sites and service instances" })
      .click();
    await page
      .locator(".ant-select-item-option")
      .filter({ hasText: "blog" })
      .click();
    await expect(
      page.getByText(/Each selected member is recreated/),
    ).toBeVisible();
    await page
      .getByRole("button", { name: "Create the network" })
      .first()
      .click();

    await expect(page).toHaveURL("/networks/front");
    await expect(
      page.getByText("blog was redeployed with its new networks."),
    ).toBeVisible();
    await expect(
      page
        .getByRole("row")
        .filter({ hasText: "blog" })
        .getByText("hosted-blog-web-1"),
    ).toBeVisible();
  });
});
