import { restrictedUser } from "../mocks/fixtures";
import { expect, test } from "./fixtures";

test("an admin sees the administration and service menus", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByText("Ada Admin")).toBeVisible();
  const menu = page.getByRole("menu").first();
  for (const group of [
    "Databases",
    "Cache",
    "Queues",
    "Storage",
    "Administration",
  ]) {
    await expect(menu.getByText(group, { exact: true })).toBeVisible();
  }
  // Engine names carry their trademark symbol.
  await menu.getByText("Queues", { exact: true }).click();
  await expect(menu.getByText("Eclipse Mosquitto™")).toBeVisible();
  await expect(menu.getByText("ElasticMQ", { exact: true })).toBeVisible();
});

test("a visitor without a session is sent to the identity provider", async ({
  page,
  api,
}) => {
  api.state.me = null;
  await page.goto("/");
  await expect(page).toHaveURL("http://api.test/openid-connect/login");
  await expect(page.getByText("Mock identity provider")).toBeVisible();
});

test("a restricted user sees neither the administration nor the services", async ({
  page,
  api,
}) => {
  api.state.me = restrictedUser;
  await page.goto("/");
  await expect(page.getByText("Rémi Restricted")).toBeVisible();
  const menu = page.getByRole("menu").first();
  // "Compute" holds only Containers now and is flattened into a plain item.
  await expect(menu.getByText("Containers", { exact: true })).toBeVisible();
  await expect(menu.getByText("Serverless", { exact: true })).toBeVisible();
  for (const group of ["Databases", "Storage", "Administration"]) {
    await expect(menu.getByText(group, { exact: true })).toHaveCount(0);
  }
  // Admin-only data is never requested for a restricted user (once every
  // request of the page has settled).
  await page.waitForLoadState("networkidle");
  expect(api.calls("GET", "/services/config")).toHaveLength(0);
  expect(api.calls("GET", "/services/instances")).toHaveLength(0);
});
