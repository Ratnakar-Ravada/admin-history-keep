import { test, expect, type Page } from "@playwright/test";
import { calendarShift, dateInput } from "../src/api";
async function open(page: Page, view = "logs", days = 7) {
  const s = await (await page.request.get("/api/session")).json();
  const today = dateInput(s.now, "Asia/Kolkata");
  await page.goto(
    "/?" +
      new URLSearchParams({
        view,
        from: calendarShift(today, 1 - days),
        to: today,
        tz: "Asia/Kolkata",
      }),
  );
  if (view === "alerts")
    await expect(page.locator(".alerts-view")).toBeVisible();
  else await expect(page.locator(".device-row")).toHaveCount(3);
  if (view === "logs")
    await expect(page.locator(".feed-heading h2")).toContainText("20 loaded");
  return today;
}
async function more(page: Page, count = 40) {
  await page
    .locator(".feed-scroll")
    .evaluate((el) => (el.scrollTop = el.scrollHeight));
  await expect(page.locator(".feed-heading h2")).toContainText(
    count + " loaded",
  );
}
test("20 records, infinite scroll, filter reset and cached-filter return", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await open(page);
  await more(page);
  await page
    .getByLabel("Event reason", { exact: true })
    .selectOption("internet_off");
  await expect(page.locator(".feed-heading h2")).toContainText("20 loaded");
  await expect
    .poll(() => page.locator(".feed-scroll").evaluate((e) => e.scrollTop))
    .toBe(0);
  await expect(page.locator(".event-topline b").first()).toHaveText(
    "Internet became unavailable",
  );
  await page.getByLabel("Event reason", { exact: true }).selectOption("");
  await expect(page.locator(".feed-heading h2")).toContainText("20 loaded");
  await expect(page.locator(".event-row")).not.toHaveCount(20);
  expect(errors).toEqual([]);
});
test("expanded real event details and semantic indicators", async ({
  page,
}) => {
  await open(page);
  await page
    .getByLabel("Event reason", { exact: true })
    .selectOption("location_off");
  await expect(page.locator(".feed-heading h2")).toContainText("20 loaded");
  await page.locator(".event-main").first().click();
  await expect(page.locator(".event-detail").first()).toContainText(
    "Job executed",
  );
  await expect(page.locator(".event-detail").first()).toContainText(
    "Stored in database",
  );
  await expect(page.locator(".event-detail").first()).toContainText(
    "Location was disabled",
  );
  await expect(page.locator(".status-icon.attention").first()).toBeVisible();
  await expect(page.locator(".event-identifiers button")).toHaveCount(0);
  await page
    .getByLabel("Event reason", { exact: true })
    .selectOption("airplane_mode_on");
  await page.locator(".event-main").first().click();
  await expect(page.locator(".event-detail").first()).toContainText("Enabled");
});
test("invalid dates, zero selection and add-existing-device flow", async ({
  page,
}) => {
  const today = await open(page);
  await page.getByLabel("Date period").selectOption("custom");
  await page.getByLabel("From date").fill(today);
  await page.getByLabel("To date").fill(calendarShift(today, -1));
  await page.getByRole("button", { name: "Apply dates" }).click();
  await expect(page.getByRole("alert")).toContainText("valid date range");
  await page.getByRole("button", { name: "Clear", exact: true }).click();
  await expect(page.locator(".empty-state")).toContainText(
    "No matching events",
  );
  await page.getByRole("button", { name: "Add existing device" }).click();
  await page.getByRole("searchbox").last().fill("Travel");
  await page.getByRole("button", { name: "Add to view" }).click();
  await page.getByRole("button", { name: "Done", exact: true }).click();
  await expect(page.locator(".people-button")).toContainText("1 device");
  await expect(page.locator(".feed-heading h2")).toContainText("20 loaded");
});
test("map data is independent of event pages, broad history is explicit, alerts stay future", async ({
  page,
}) => {
  await open(page);
  await page
    .getByRole("button", { name: "Journeys", exact: true })
    .last()
    .click();
  await expect(page.locator(".playback-slider>div").first()).toContainText(
    "points",
  );
  await expect
    .poll(async () =>
      parseInt(
        await page.locator(".playback-slider>div>span").first().innerText(),
      ),
    )
    .toBeGreaterThan(20);
  await page.getByLabel("Date period").selectOption("90");
  await expect(page.locator(".resolution-note")).toContainText(
    "daily representative",
  );
  await page
    .getByRole("button", { name: "Alerts", exact: true })
    .first()
    .click();
  await expect(page.locator(".coming-pill")).toHaveCount(2);
  await expect(page.locator(".alerts-view")).toContainText(
    "monitoring is not active",
  );
});
test("mobile and tablet logs, expanded rows and device sheet fit", async ({
  page,
}) => {
  await open(page);
  for (const width of [834, 390, 320]) {
    await page.setViewportSize({ width, height: 844 });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBeTruthy();
    expect(
      (await page.getByLabel("Date period").boundingBox())!.width,
    ).toBeGreaterThan(80);
    await page.locator(".event-main").first().click();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBeTruthy();
    await page.locator(".event-main").first().click();
    await page.screenshot({
      path: `../docs/logs-${width}.png`,
      fullPage: true,
    });
  }
  await page.locator(".people-button").click();
  await expect(page.locator(".people-panel")).toBeVisible();
  await page.getByRole("checkbox", { name: "Show Sam’s phone" }).uncheck();
  await expect(page.locator(".people-button")).toContainText("1 device");
  await expect(page.locator(".feed-heading h2")).toContainText("20 loaded");
});
test("rapid filter changes cannot append an older response", async ({
  page,
}) => {
  await open(page);
  await page.route("**/api/events?**", async (route) => {
    const u = new URL(route.request().url());
    if (u.searchParams.get("reasons") === "location_off")
      await new Promise((r) => setTimeout(r, 600));
    try {
      await route.continue();
    } catch {}
  });
  await page
    .getByLabel("Event reason", { exact: true })
    .selectOption("location_off");
  await page
    .getByLabel("Event reason", { exact: true })
    .selectOption("internet_on");
  await expect(page.locator(".event-topline b").first()).toHaveText(
    "Internet became available",
  );
  await page.waitForTimeout(900);
  for (const title of await page.locator(".event-topline b").allTextContents())
    expect(title).toBe("Internet became available");
  await expect(page.locator(".feed-heading h2")).toContainText("20 loaded");
});
test("cursor expiry preserves loaded rows and offers a clean restart", async ({
  page,
}) => {
  await open(page);
  let blocked = false;
  await page.route("**/api/events?**", async (route) => {
    if (new URL(route.request().url()).searchParams.has("cursor")) {
      blocked = true;
      await route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          code: "cursor_expired",
          error: "This browsing session expired.",
        }),
      });
    } else await route.continue();
  });
  await page
    .locator(".feed-scroll")
    .evaluate((el) => (el.scrollTop = el.scrollHeight));
  await expect(page.getByRole("alert")).toContainText("expired");
  await expect(page.locator(".feed-heading h2")).toContainText("20 loaded");
  expect(blocked).toBeTruthy();
  await page.getByRole("button", { name: "Restart first 20" }).click();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(page.locator(".feed-heading h2")).toContainText("20 loaded");
});
test("touch selection and accessible list remain usable", async ({
  browser,
}) => {
  const context = await browser.newContext({
    viewport: { width: 390, height: 844 },
    isMobile: true,
    hasTouch: true,
  });
  const page = await context.newPage();
  await open(page);
  await page.locator(".people-button").click();
  const size = await page.locator(".device-check").first().boundingBox();
  expect(size!.width).toBeGreaterThanOrEqual(44);
  expect(size!.height).toBeGreaterThanOrEqual(44);
  await page.locator(".people-button").click();
  await page.getByLabel("Simple list").check();
  await expect(page.locator(".event-row")).toHaveCount(20);
  await more(page);
  await expect(page.locator(".event-row")).toHaveCount(40);
  await page.screenshot({
    path: "../docs/mobile-activity.png",
    fullPage: true,
  });
  await context.close();
});
test("bounded list memory can recover earlier pages at the same snapshot", async ({
  page,
}) => {
  await open(page);
  for (let count = 40; count <= 200; count += 20) await more(page, count);
  await page
    .locator(".feed-scroll")
    .evaluate((el) => (el.scrollTop = el.scrollHeight));
  await expect(page.locator(".feed-footer")).toContainText("Records 21–220");
  await expect(page.locator(".feed-heading h2")).toContainText("200 loaded");
  await page.locator(".feed-scroll").evaluate((el) => (el.scrollTop = 0));
  await expect(page.locator(".feed-footer")).toContainText("Records 1–200");
  await expect(page.locator(".feed-heading h2")).toContainText("200 loaded");
});

test("map workers and basemap load in the production build", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  await open(page, "journeys");
  await expect(
    page.getByRole("region", { name: "Location map" }),
  ).toHaveAttribute("aria-busy", "false", { timeout: 30000 });
  await expect(page.locator(".map-provider-note")).toHaveCount(0);
  expect(errors).toEqual([]);
  await page.screenshot({
    path: "../docs/desktop-journeys.png",
    fullPage: true,
  });
});

test("Google sign-in screen uses the official button without requesting mail access", async ({
  page,
}) => {
  const session = await (await page.request.get("/api/session")).json();
  await page.route("**/api/session", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        ...session,
        authenticated: false,
        demo: false,
        user: undefined,
      }),
    }),
  );
  await page.goto("/");
  const link = page.getByRole("link", { name: "Sign in with Google" });
  await expect(link).toHaveAttribute("href", "/auth/login");
  const image = page.getByRole("img", { name: "Sign in with Google" });
  await expect(image).toBeVisible();
  await expect
    .poll(() => image.evaluate((el: HTMLImageElement) => el.naturalWidth))
    .toBeGreaterThan(0);
  await expect(page.locator(".signin-permissions")).toContainText(
    "Email alerts are coming soon",
  );
  await page.screenshot({ path: "../docs/google-signin.png", fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(link).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
});

test("Gmail sender connection is explicit and disconnect sends no message", async ({
  page,
}) => {
  let connected = true,
    connects = 0,
    disconnects = 0;
  await page.route("**/api/email/status", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        enabled: true,
        connected,
        can_connect: true,
        sender_email: "synthetic-sender@gmail.com",
      }),
    }),
  );
  await page.route("**/api/email/disconnect", (route) => {
    disconnects++;
    connected = false;
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: '{"disconnected":true}',
    });
  });
  await page.route("**/api/email/connect", (route) => {
    connects++;
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: '{"authorization_url":"/mock-google-consent"}',
    });
  });
  await page.route("**/mock-google-consent", (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/html",
      body: "<p>Synthetic Google consent screen</p>",
    }),
  );
  await open(page, "alerts");
  await expect(
    page.getByRole("region", { name: "Email sender" }),
  ).toContainText("Sender connected");
  await expect(
    page.getByRole("region", { name: "Email sender" }),
  ).toContainText("Connecting Gmail does not send a message");
  await page.getByRole("button", { name: "Disconnect sender" }).click();
  await expect(
    page.getByRole("region", { name: "Email sender" }),
  ).toContainText("Ready to connect");
  expect(disconnects).toBe(1);
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
  await page.screenshot({ path: "../docs/gmail-sender.png", fullPage: true });
  await page
    .getByRole("button", { name: "Connect Gmail", exact: true })
    .click();
  await expect(page).toHaveURL(/mock-google-consent$/);
  expect(connects).toBe(1);
});
