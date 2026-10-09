import { test, expect } from "@playwright/test";

test("authenticated live inventory, search, details, history and settings", async ({
  page,
  request,
}) => {
  expect((await request.get("/api/v1/devices")).status()).toBe(401);
  await page.goto("/");
  await page.getByLabel("アクセストークン").fill("wrong");
  await page.getByRole("button", { name: "ダッシュボードを開く" }).click();
  await expect(page.getByRole("alert")).toBeVisible();
  await page
    .getByLabel("アクセストークン")
    .fill("tunwatch-e2e-test-token-not-a-real-secret");
  await page.getByRole("button", { name: "ダッシュボードを開く" }).click();
  await expect(
    page.getByRole("heading", { name: "ネットワークの現在地" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "atlas-workstation", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".live-pill")).toHaveText("LIVE");
  expect(await page.evaluate(() => Object.keys(localStorage).length)).toBe(0);
  await page.getByLabel("クライアントを検索").fill("home-server");
  await expect(page.locator("tbody tr")).toHaveCount(1);
  await page.getByRole("button", { name: "home-server", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText("192.0.2.12");
  await page.getByRole("button", { name: "閉じる", exact: true }).click();
  await page.getByLabel("クライアントを検索").fill("");
  await page.setViewportSize({ width: 1440, height: 960 });
  await page.screenshot({
    path: "test-results/dashboard-desktop.png",
    fullPage: true,
  });
  await page.getByRole("button", { name: "接続履歴", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "接続イベント" }),
  ).toBeVisible();
  await expect(page.locator(".event").first()).toBeVisible();
  await page.getByRole("button", { name: "監視設定", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "状態判定と保存" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "ログアウト", exact: true }).click();
  await expect(page.getByLabel("アクセストークン")).toBeVisible();
});

test("mobile inventory stays within the viewport", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await page
    .getByLabel("アクセストークン")
    .fill("tunwatch-e2e-test-token-not-a-real-secret");
  await page.getByRole("button", { name: "ダッシュボードを開く" }).click();
  await expect(
    page.getByRole("button", { name: "atlas-workstation", exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: "test-results/dashboard-mobile.png",
    fullPage: true,
  });
});
