import {
  apiKey,
  container,
  domainEntry,
  hostingStack,
  restrictedUser,
  sftpUser,
} from "../mocks/fixtures";
import { expect, test } from "./fixtures";

test("the resource overview counts an admin's resources and links to them", async ({
  page,
  api,
}) => {
  api.state.containers = [
    container("shop-web-1", "shop"),
    container("shop-db-1", "shop"),
    container("blog-web-1", "blog"),
  ];
  api.state.stacks = [hostingStack("alpha")];
  api.state.sftpConfig = { enabled: true, initDirs: [] };
  api.state.sftpUsers = [sftpUser("ann", "alpha")];
  api.state.domains = [domainEntry("shop-web-1", ["shop.example.com"])];
  api.state.apiKeys = [apiKey("one"), apiKey("two")];

  await page.goto("/");
  const card = page.locator(".ant-card", { hasText: "Resource overview" });
  await expect(card.getByRole("link", { name: "Containers: 3" })).toBeVisible();
  await expect(card.getByRole("link", { name: "Stacks: 2" })).toBeVisible();
  await expect(
    card.getByRole("link", { name: "Hosted sites: 1" }),
  ).toBeVisible();
  await expect(card.getByRole("link", { name: "Databases: 1" })).toBeVisible();
  await expect(card.getByRole("link", { name: "SFTP users: 1" })).toBeVisible();
  await expect(card.getByRole("link", { name: "API keys: 2" })).toBeVisible();

  if (process.env.HOME_SCREENSHOT) {
    await page.waitForLoadState("networkidle");
    await page.screenshot({
      path: process.env.HOME_SCREENSHOT,
      fullPage: true,
    });
  }

  await card.getByRole("link", { name: "Databases: 1" }).click();
  await expect(page).toHaveURL(/\/services\/postgres$/);
});

test("a restricted user's overview has only their resources", async ({
  page,
  api,
}) => {
  api.state.me = restrictedUser;
  await page.goto("/");
  const card = page.locator(".ant-card", { hasText: "Resource overview" });
  const names = [
    "Containers: 0",
    "Stacks: 0",
    "Scheduled tasks: 0",
    "API keys: 0",
  ];
  for (const name of names) {
    await expect(card.getByRole("link", { name })).toBeVisible();
  }
  await expect(card.getByRole("link")).toHaveCount(names.length);
  // No admin-only endpoint is requested (it would answer 403 and write an
  // "access denied" entry to the audit trail on every page load).
  await page.waitForLoadState("networkidle");
  const adminOnly = [
    /^\/sftp\//,
    /^\/domains$/,
    /^\/networks/,
    /^\/hosting\//,
    /^\/services\//,
    /^\/functions/,
    /^\/users$/,
    /^\/audit/,
  ];
  const denied = api.requests
    .map((r) => r.path)
    .filter((path) => adminOnly.some((re) => re.test(path)));
  expect(denied).toEqual([]);
});
