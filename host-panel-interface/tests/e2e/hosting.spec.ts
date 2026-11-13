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

test.describe("hosting creation review", () => {
  // The review used to crash on its first render (a selector watcher starts
  // with an empty object): every template, PHP or static, must reach it.
  for (const template of [
    "PHP + Nginx",
    "PHP + Apache",
    "Static (Nginx)",
    "Static (Apache)",
  ]) {
    test(`summarizes a ${template} site`, async ({ page }) => {
      const errors: string[] = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.goto("/hosting/new");
      await page.getByRole("radio", { name: template }).click();
      await page.getByRole("button", { name: /Next/ }).click();
      await page.getByLabel("Name").fill("mysite");

      const review = page.getByText(
        "Check the settings before creating the hosting",
      );
      const next = page.getByRole("button", { name: /Next/ });
      for (let step = 0; step < 10 && !(await review.isVisible()); step++) {
        await next.click();
      }

      await expect(review).toBeVisible();
      await expect(page.getByText("mysite", { exact: true })).toBeVisible();
      await expect(page.getByRole("button", { name: /Create/ })).toBeEnabled();
      expect(errors).toEqual([]);
    });
  }
});
