import { hostingConfig } from "../mocks/fixtures";
import { expect, test } from "./fixtures";

const networksSelect = (page: import("@playwright/test").Page) =>
  page.getByRole("combobox", { name: "Networks" });
const selectedNetworks = (page: import("@playwright/test").Page) =>
  page.locator(".ant-select-selection-item");

test.describe("hosting creation", () => {
  test("preselects the shared network for a new site", async ({ page }) => {
    await page.goto("/hosting/new");
    await page.getByRole("button", { name: /Next/ }).click();
    await expect(networksSelect(page)).toBeVisible();
    await expect(selectedNetworks(page)).toHaveText(["services"]);
  });

  test("preselects no network when the services module is disabled", async ({
    page,
    api,
  }) => {
    api.state.hostingConfig = hostingConfig(false);
    await page.goto("/hosting/new");
    await page.getByRole("button", { name: /Next/ }).click();
    await expect(page.getByLabel("Name")).toBeVisible();
    await expect(networksSelect(page)).toBeVisible();
    await expect(selectedNetworks(page)).toHaveCount(0);
  });
});
