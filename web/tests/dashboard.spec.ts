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
  expect(
    await page.evaluate(() => localStorage.getItem("tunwatch.api_token")),
  ).toBe("tunwatch-e2e-test-token-not-a-real-secret");
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
  await expect(page.getByText("demo-sensor", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "ログアウト", exact: true }).click();
  await expect(page.getByLabel("アクセストークン")).toBeVisible();
  expect(
    await page.evaluate(() => localStorage.getItem("tunwatch.api_token")),
  ).toBeNull();
  await page.reload();
  await expect(page.getByLabel("アクセストークン")).toBeVisible();
});

test("saved token restores an expired session and live stream", async ({
  page,
  context,
}) => {
  await page.goto("/");
  await page
    .getByLabel("アクセストークン")
    .fill("tunwatch-e2e-test-token-not-a-real-secret");
  await page.getByRole("button", { name: "ダッシュボードを開く" }).click();
  await expect(page.locator(".live-pill")).toHaveText("LIVE");
  await context.clearCookies();
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "ネットワークの現在地" }),
  ).toBeVisible();
  await expect(page.locator(".live-pill")).toHaveText("LIVE");
  expect(
    (await context.cookies()).some((c) => c.name === "tunwatch_session"),
  ).toBe(true);
  // Expiry while the page stays open must also renew the cookie.
  await context.clearCookies();
  await expect
    .poll(
      async () =>
        (await context.cookies()).some((c) => c.name === "tunwatch_session"),
      { timeout: 15000 },
    )
    .toBe(true);
  await expect(page.locator(".live-pill")).toHaveText("LIVE");
});

test("rejected saved credentials are removed", async ({ page }) => {
  await page.addInitScript(() =>
    localStorage.setItem("tunwatch.api_token", "invalid-saved-token"),
  );
  await page.goto("/");
  await expect(page.getByLabel("アクセストークン")).toBeVisible();
  expect(
    await page.evaluate(() => localStorage.getItem("tunwatch.api_token")),
  ).toBeNull();
});

test("transient renewal failures preserve saved credentials", async ({
  page,
}) => {
  await page.addInitScript(() =>
    localStorage.setItem(
      "tunwatch.api_token",
      "tunwatch-e2e-test-token-not-a-real-secret",
    ),
  );
  await page.route("**/api/v1/session", (route) =>
    route.fulfill({ status: 503, body: "temporarily unavailable" }),
  );
  await page.goto("/");
  await expect(page.getByRole("alert")).toContainText("503");
  expect(
    await page.evaluate(() => localStorage.getItem("tunwatch.api_token")),
  ).toBe("tunwatch-e2e-test-token-not-a-real-secret");
  await page.unroute("**/api/v1/session");
  await page.reload();
  await expect(page.locator(".live-pill")).toHaveText("LIVE");
});

test("login remains usable when browser storage is disabled", async ({
  page,
}) => {
  await page.addInitScript(() => {
    Storage.prototype.getItem = () => {
      throw new DOMException("disabled", "SecurityError");
    };
    Storage.prototype.setItem = () => {
      throw new DOMException("disabled", "SecurityError");
    };
    Storage.prototype.removeItem = () => {
      throw new DOMException("disabled", "SecurityError");
    };
  });
  await page.goto("/");
  await page
    .getByLabel("アクセストークン")
    .fill("tunwatch-e2e-test-token-not-a-real-secret");
  await page.getByRole("button", { name: "ダッシュボードを開く" }).click();
  await expect(page.locator(".live-pill")).toHaveText("LIVE");
  await expect(page.getByRole("alert")).toContainText("ブラウザへの保存に失敗");
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

test("unnamed clients filter combines with status and search", async ({
  page,
}) => {
  await page.goto("/");
  await page
    .getByLabel("アクセストークン")
    .fill("tunwatch-e2e-test-token-not-a-real-secret");
  await page.getByRole("button", { name: "ダッシュボードを開く" }).click();
  const unnamed = page.getByRole("button", { name: "名称不明のみ 1" });
  await expect(unnamed).toBeVisible();
  await unnamed.click();
  await expect(unnamed).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("tbody tr")).toHaveCount(1);
  await expect(page.locator("tbody tr")).toContainText("名前未取得の端末");
  await page.locator(".filters").getByRole("button", { name: "観測中" }).click();
  await expect(page.locator("tbody tr")).toHaveCount(1);
  await page.getByLabel("クライアントを検索").fill("atlas");
  await expect(page.locator("tbody tr")).toHaveCount(0);
  await expect(page.getByText("条件に一致する端末がありません")).toBeVisible();
  await page.getByLabel("クライアントを検索").fill("");
  await unnamed.click();
  await expect(unnamed).toHaveAttribute("aria-pressed", "false");
  await expect(page.locator("tbody tr")).toHaveCount(7);
});

test("VIP identities are hidden by default and can be inspected", async ({
  page,
  request,
}) => {
  expect((await request.get("/api/v1/vips")).status()).toBe(401);
  await page.goto("/");
  await page
    .getByLabel("アクセストークン")
    .fill("tunwatch-e2e-test-token-not-a-real-secret");
  await page.getByRole("button", { name: "ダッシュボードを開く" }).click();
  const showVIPs = page.getByRole("button", { name: "VIP も表示 1" });
  await expect(showVIPs).toBeVisible();
  await expect(page.locator("tbody tr")).toHaveCount(7);
  const evidence = await page.evaluate(async () =>
    (await fetch("/api/v1/vips")).json(),
  );
  expect(evidence.vips).toContainEqual({
    interface: "demo0",
    ip: "192.0.2.1",
    source: "vrrp_mac",
    vrid: 42,
  });
  expect(
    await page.evaluate(async () =>
      (await fetch("/api/v1/vips?ip=192.0.2.1")).json(),
    ),
  ).toMatchObject({ vips: evidence.vips });
  expect(
    await page.evaluate(async () =>
      (await fetch("/api/v1/vips?ip=192.0.2.99")).json(),
    ),
  ).toMatchObject({ vips: [] });
  expect(
    await page.evaluate(async () =>
      (await fetch("/api/v1/vips?ip=invalid")).status,
    ),
  ).toBe(400);
  await showVIPs.click();
  await expect(showVIPs).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("tbody tr")).toHaveCount(8);
  await page.getByLabel("クライアントを検索").fill("00:00:5e:00:01:2a");
  await expect(page.locator("tbody tr")).toHaveCount(1);
  await page.locator("tbody tr").click();
  await expect(page.getByRole("dialog")).toContainText("VRRP VIP");
  await expect(page.getByRole("dialog")).toContainText("VRID 42");
  await page.getByRole("button", { name: "閉じる", exact: true }).click();
  await showVIPs.click();
  await expect(page.locator("tbody tr")).toHaveCount(0);
});

test("hosts display name keeps file provenance and observed hostname", async ({
  page,
}) => {
  await page.goto("/");
  await page
    .getByLabel("アクセストークン")
    .fill("tunwatch-e2e-test-token-not-a-real-secret");
  await page.getByRole("button", { name: "ダッシュボードを開く" }).click();
  await page.getByLabel("クライアントを検索").fill("access-point");
  await expect(page.locator("tbody tr")).toHaveCount(1);
  await expect(page.locator("tbody tr")).toContainText("hosts ファイル");
  await page
    .getByRole("button", { name: "configured-ap.example", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toContainText(
    "test.hosts · 192.0.2.15",
  );
  await expect(page.getByRole("dialog")).toContainText("access-point");
});

test("GitHub hosts sync status shows source and last success", async ({
  page,
}) => {
  await page.route("**/api/v1/status", async (route) => {
    const response = await route.fetch();
    if (!response.ok()) {
      await route.fulfill({ response });
      return;
    }
    const data = await response.json();
    data.capture.hosts_sync = {
      url: "https://github.com/y2e-org/playbooks/blob/master/svc/coredns/config/tun.y2e.org.hosts",
      entries: 42,
      last_checked: "2026-10-10T00:00:00Z",
      last_success: "2026-10-10T00:00:00Z",
    };
    await route.fulfill({ response, json: data });
  });
  await page.goto("/");
  await page
    .getByLabel("アクセストークン")
    .fill("tunwatch-e2e-test-token-not-a-real-secret");
  await page.getByRole("button", { name: "ダッシュボードを開く" }).click();
  await page.getByRole("button", { name: "監視設定", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "hosts 自動同期" }),
  ).toBeVisible();
  await expect(
    page.getByText(
      "https://github.com/y2e-org/playbooks/blob/master/svc/coredns/config/tun.y2e.org.hosts",
    ),
  ).toBeVisible();
  await expect(page.getByText("42", { exact: true })).toBeVisible();
});
