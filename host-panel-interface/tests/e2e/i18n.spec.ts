import { expect, test } from "./fixtures";

test("the language can be switched and is remembered", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByText("Ada Admin")).toBeVisible();
  await page.getByRole("combobox", { name: "Language" }).click();
  await page.getByTitle("Français").click();
  const menu = page.getByRole("menu").first();
  await expect(
    menu.getByText("Bases de données", { exact: true }),
  ).toBeVisible();
  await expect(menu.getByText("Stockage", { exact: true })).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem("lang"))).toBe("fr");

  await page.reload();
  await expect(
    page.getByRole("menu").first().getByText("Stockage", { exact: true }),
  ).toBeVisible();
});
