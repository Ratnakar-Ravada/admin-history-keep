import { test, expect } from "@playwright/test";

test("advanced analytics, cursor pages, filters and session drilldown", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/?view=analytics");
  await expect(
    page.getByRole("region", { name: "Application usage analytics" }),
  ).toBeVisible();
  await expect(page.locator(".metric-card")).toHaveCount(6);
  await expect(page.locator(".analytics-table-wrap tbody tr")).toHaveCount(3);
  await page.getByRole("tab", { name: "API requests", exact: true }).click();
  const rows = page.locator(".analytics-table-wrap tbody tr");
  await expect(rows).toHaveCount(20);
  await expect(
    page.getByRole("button", { name: "Previous 20" }),
  ).toBeDisabled();
  const first = await rows.first().innerText();
  await page.getByRole("button", { name: "Next 20" }).click();
  await expect(rows.first()).not.toHaveText(first);
  await expect(rows).toHaveCount(20);
  await page.getByLabel("Analytics period").selectOption("30");
  await expect(
    page.getByRole("button", { name: "Previous 20" }),
  ).toBeDisabled();
  await expect(rows).toHaveCount(20);
  await page.locator(".top-users button").first().click();
  await expect(page.locator(".analytics-filter-label")).toContainText("User:");
  await expect(rows).toHaveCount(20);
  await page.getByRole("tab", { name: "Sessions", exact: true }).click();
  await expect(rows).toHaveCount(1);
  await rows.getByRole("button", { name: "Requests", exact: true }).click();
  await expect(page.locator(".analytics-filter-label")).toContainText(
    "One session",
  );
  await expect(rows).toHaveCount(20);
  await page.getByRole("button", { name: "Clear analytics filters" }).click();
  await expect(page.locator(".analytics-filter-label")).toHaveCount(0);
  expect(errors).toEqual([]);
});

test("analytics remains readable on desktop, tablet and mobile", async ({
  page,
}) => {
  await page.goto("/?view=analytics");
  await expect(page.locator(".metric-card")).toHaveCount(6);
  for (const width of [1440, 834, 390, 320]) {
    await page.setViewportSize({ width, height: width === 1440 ? 1100 : 844 });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBeTruthy();
    await expect(page.getByLabel("Analytics period")).toBeVisible();
    await page.screenshot({
      path: `../docs/analytics-${width}.png`,
      fullPage: width === 1440,
    });
    await page
      .getByRole("tab", { name: "App users", exact: true })
      .scrollIntoViewIfNeeded();
    await expect(page.locator(".analytics-table-wrap tbody tr")).toHaveCount(3);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBeTruthy();
    if (width === 390)
      await page.screenshot({ path: "../docs/analytics-records-390.png" });
    await page.getByLabel("Analytics period").scrollIntoViewIfNeeded();
  }
});

test("ordinary viewers do not see analytics navigation", async ({ page }) => {
  const session = await (await page.request.get("/api/session")).json();
  await page.route("**/api/session", (route) =>
    route.fulfill({
      json: { ...session, user: { ...session.user, is_advanced: false } },
    }),
  );
  await page.goto("/?view=analytics");
  await expect(page.locator(".device-row")).toHaveCount(3);
  await expect(
    page.getByRole("button", { name: "Analytics", exact: true }),
  ).toHaveCount(0);
  await expect(page.locator(".analytics-page")).toHaveCount(0);
});

test("view telemetry is deduplicated and heartbeat distinguishes foreground activity", async ({
  page,
}) => {
  await page.clock.install();
  let opens = 0;
  const activity: boolean[] = [];
  await page.route("**/api/usage/view", (route) => {
    opens++;
    expect(route.request().postDataJSON()).toEqual({ view: "analytics" });
    expect(route.request().headers()["x-app-source"]).toBe("background");
    return route.fulfill({ json: { ok: true } });
  });
  await page.route("**/api/usage/heartbeat", (route) => {
    activity.push(route.request().postDataJSON().active);
    return route.fulfill({ json: { ok: true } });
  });
  await page.goto("/?view=analytics");
  await expect(page.locator(".metric-card")).toHaveCount(6);
  await expect.poll(() => opens).toBe(1);
  await page.clock.runFor(31000);
  await expect.poll(() => activity).toEqual([true]);
  await page.clock.runFor(30000);
  await expect.poll(() => activity).toEqual([true, false]);
  await page.locator(".analytics-explainer").click();
  await page.clock.runFor(30000);
  await expect.poll(() => activity).toEqual([true, false, true]);
});
