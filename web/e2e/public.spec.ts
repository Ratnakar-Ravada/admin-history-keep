import { expect, test } from "@playwright/test";

test("public overview explains the app and permissions on desktop and mobile", async ({
  page,
}, testInfo) => {
  await page.route("**/api/session", (route) =>
    route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ authenticated: false, login_url: "/auth/login" }),
    }),
  );
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    "Your devices.Their journeys.One clear view.",
  );
  await expect(
    page.getByRole("link", { name: "Sign in with Google", exact: true }),
  ).toHaveAttribute("href", "/auth/login");
  await expect(
    page.getByRole("heading", { name: "Journeys over time" }),
  ).toBeVisible();
  await expect(page.locator("#google-access")).toContainText(
    "recipient email addresses you configure in Alerts",
  );
  await expect(page.locator("#google-access")).toContainText("not active yet");
  await page.setViewportSize({
    width: 1440,
    height: await page.evaluate(() => document.documentElement.scrollHeight),
  });
  await page.screenshot({
    path: testInfo.outputPath("landing-desktop.png"),
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByRole("link", { name: "Sign in with Google", exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
  await page.screenshot({
    path: testInfo.outputPath("landing-mobile.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 320, height: 700 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
});

test("an API outage leaves the public overview and privacy link accessible", async ({
  page,
}) => {
  await page.route("**/api/session", (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/json",
      body: '{"error":"Temporarily unavailable"}',
    }),
  );
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "Journeys over time" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Retry connection" }),
  ).toBeVisible();
  await page
    .getByRole("navigation", { name: "About Location Keeper" })
    .getByRole("link", { name: "Privacy policy" })
    .click();
  await expect(page).toHaveTitle("Privacy Policy | Location Keeper");
});

test("policy, PDF and homepage disclosure work without JavaScript or sign-in", async ({
  browser,
  baseURL,
}, testInfo) => {
  const context = await browser.newContext({
    javaScriptEnabled: false,
    baseURL,
  });
  try {
    const page = await context.newPage();
    const apiRequests: string[] = [];
    page.on("request", (request) => {
      if (new URL(request.url()).pathname.startsWith("/api/"))
        apiRequests.push(request.url());
    });
    const response = await page.goto("/privacy.html");
    expect(response?.status()).toBe(200);
    await expect(
      page.getByRole("heading", { name: "Privacy Policy", exact: true }),
    ).toBeVisible();
    await expect(page.locator("#gmail").locator("..")).toContainText(
      "recipient email addresses you configure in the Alerts section",
    );
    await expect(
      page.getByRole("link", {
        name: "Google Account connections",
        exact: true,
      }),
    ).toHaveAttribute("href", "https://myaccount.google.com/connections");
    const pdf = await page.request.get("/privacy-policy.pdf");
    expect(pdf.status()).toBe(200);
    expect(pdf.headers()["content-type"]).toContain("application/pdf");
    expect((await pdf.body()).subarray(0, 5).toString()).toBe("%PDF-");
    await page.screenshot({
      path: testInfo.outputPath("privacy-desktop.png"),
      fullPage: true,
    });
    await page.setViewportSize({ width: 390, height: 844 });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBeTruthy();
    await page.screenshot({
      path: testInfo.outputPath("privacy-mobile.png"),
      fullPage: true,
    });
    await page.goto("/");
    await expect(
      page.getByRole("heading", { name: "Location Keeper", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: "Read the privacy policy" }),
    ).toHaveAttribute("href", "/privacy.html");
    await page
      .getByRole("link", { name: "Terms of service", exact: true })
      .click();
    await expect(page).toHaveTitle("Terms of Service | Location Keeper");
    await expect(
      page.getByRole("heading", { name: "Terms of Service", exact: true }),
    ).toBeVisible();
    await expect(page.locator("#google-email").locator("..")).toContainText(
      "recipient email addresses you configure in Alerts",
    );
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBeTruthy();
    await page.screenshot({ path: testInfo.outputPath("terms-mobile.png") });
    expect(apiRequests).toEqual([]);
  } finally {
    await context.close();
  }
});
