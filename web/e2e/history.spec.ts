import {
  expect,
  test,
  type APIRequestContext,
  type Page,
} from "@playwright/test";
import { calendarShift, dateInput, utcBoundary } from "../src/api";

type Kind = "calls" | "notifications" | "app-usage";
type HistoryRecord = {
  event_id: string;
  device_id: string;
  event_type: string;
  source_package?: string;
  fields: Record<string, string | null>;
};
type HistoryPage = {
  items: HistoryRecord[];
  next_cursor: string | null;
  page_cursor: string | null;
  has_more: boolean;
};

const sections = [
  { kind: "calls", nav: "Calls", title: "Calls history", type: "3" },
  {
    kind: "notifications",
    nav: "Notifications",
    title: "Notification history",
    type: "removed",
  },
  {
    kind: "app-usage",
    nav: "App usage",
    title: "App usage history",
    type: "session",
  },
] as const;

async function selectedPeriod(request: APIRequestContext) {
  const session = await (await request.get("/api/session")).json();
  const registry = await (await request.get("/api/devices")).json();
  const today = dateInput(session.now, "Asia/Kolkata");
  const from = calendarShift(today, -6);
  const devices = registry.items
    .slice(0, 2)
    .map((d: { device_id: string }) => d.device_id);
  return {
    from,
    today,
    params: new URLSearchParams({
      device_ids: devices.join(","),
      from: utcBoundary(from, "Asia/Kolkata"),
      to: utcBoundary(today, "Asia/Kolkata", true),
      timezone: "Asia/Kolkata",
    }),
  };
}

async function history(
  request: APIRequestContext,
  kind: Kind,
  params: URLSearchParams,
) {
  const response = await request.get(`/api/history/${kind}?${params}`);
  expect(response.status()).toBe(200);
  return response.json() as Promise<HistoryPage>;
}

async function open(page: Page, kind: Kind = "calls") {
  const period = await selectedPeriod(page.request);
  await page.goto(
    "/?" +
      new URLSearchParams({
        view: kind,
        from: period.from,
        to: period.today,
        tz: "Asia/Kolkata",
      }),
  );
  await expect(page.locator(".history-record")).toHaveCount(20);
}

test("device history APIs paginate actual demo records and apply type/search filters", async ({
  request,
}) => {
  const { params } = await selectedPeriod(request);
  const deviceIDs = params.get("device_ids")!.split(",");
  for (const section of sections) {
    const first = await history(request, section.kind, params);
    expect(first.items).toHaveLength(20);
    expect(first.has_more).toBe(true);
    expect(first.next_cursor).toBeTruthy();
    expect(first.items.every((row) => deviceIDs.includes(row.device_id))).toBe(
      true,
    );
    const nextParams = new URLSearchParams(params);
    nextParams.set("cursor", first.next_cursor!);
    const next = await history(request, section.kind, nextParams);
    const firstIDs = new Set(first.items.map((row) => row.event_id));
    expect(next.items.length).toBeGreaterThan(0);
    expect(next.items.every((row) => !firstIDs.has(row.event_id))).toBe(true);
    if (first.page_cursor) {
      nextParams.set("cursor", first.page_cursor);
      const replay = await history(request, section.kind, nextParams);
      expect(replay.items.map((row) => row.event_id)).toEqual(
        first.items.map((row) => row.event_id),
      );
    }
    const filteredParams = new URLSearchParams(params);
    filteredParams.set("event_type", section.type);
    const filtered = await history(request, section.kind, filteredParams);
    expect(filtered.items.length).toBeGreaterThan(0);
    expect(filtered.items.every((row) => row.event_type === section.type)).toBe(
      true,
    );
    filteredParams.set("q", "no-history-match-7dc388b3");
    expect(
      (await history(request, section.kind, filteredParams)).items,
    ).toEqual([]);
    const emptyDevices = new URLSearchParams(params);
    emptyDevices.set("device_ids", "");
    expect((await history(request, section.kind, emptyDevices)).items).toEqual(
      [],
    );
  }
});

test("signed-in navigation opens all phone histories with saved record details", async ({
  page,
}, testInfo) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await open(page);
  const nav = page.getByRole("navigation", { name: "Main views" });
  for (const section of sections) {
    await nav.getByRole("button", { name: section.nav, exact: true }).click();
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(
      section.title,
    );
    const panel = page.locator(".phone-history");
    await expect(panel.locator(".history-record")).toHaveCount(20);
    const row = panel.locator(".history-record").first();
    await row.locator(".history-record-summary").click();
    await expect(row.locator(".history-details")).toBeVisible();
    await expect(row.locator(".history-details")).toContainText(
      "Observed on phone",
    );
    await expect(row.locator(".history-details")).toContainText(
      "Stored in database",
    );
    await row.locator(".history-metadata > summary").click();
    await expect(row.locator(".history-identifiers")).toContainText("Event ID");
    await expect(row.locator(".history-identifiers")).toContainText(
      "Device ID",
    );
    expect(await panel.innerText()).not.toContain("undefined");
    await page.screenshot({
      path: testInfo.outputPath(`${section.kind}-desktop.png`),
    });
  }
  expect(errors).toEqual([]);
});

test("call pagination returns to the same page and filters restart the list", async ({
  page,
}) => {
  await open(page);
  const panel = page.locator(".phone-history");
  const records = panel.locator(".history-record");
  const firstSummary = records.first().locator(".history-record-summary");
  const first = (await firstSummary.textContent())!;
  const previous = panel.getByRole("button", { name: "Previous", exact: true });
  const next = panel.getByRole("button", { name: "Next", exact: true });
  await expect(previous).toBeDisabled();
  await next.click();
  await expect(panel.locator(".history-pagination")).toContainText("Page 2");
  await expect(firstSummary).not.toHaveText(first);
  await previous.click();
  await expect(firstSummary).toHaveText(first);
  await next.click();
  await panel.getByLabel("Call type", { exact: true }).selectOption("3");
  await expect(previous).toBeDisabled();
  await expect(panel.locator(".history-pagination")).toContainText("Page 1");
  await expect(panel.locator(".history-type-badge").first()).toHaveText(
    "Missed",
  );
  for (const badge of await panel
    .locator(".history-type-badge")
    .allTextContents()) {
    expect(badge).toBe("Missed");
  }
  await panel
    .getByLabel("Search name or number")
    .fill("no-history-match-7dc388b3");
  await panel.getByRole("button", { name: "Search", exact: true }).click();
  await expect(panel.locator(".history-state")).toContainText(
    "No calls in this selection",
  );
  await expect(records).toHaveCount(0);
  await panel.getByRole("button", { name: "Clear history filters" }).click();
  await expect(records).toHaveCount(20);
  await expect(previous).toBeDisabled();
  await expect(panel.getByLabel("Call type", { exact: true })).toHaveValue("");

  const { params } = await selectedPeriod(page.request);
  const data = await history(page.request, "calls", params);
  const name = data.items.find((row) => row.fields.name)?.fields.name;
  expect(name).toBeTruthy();
  await panel.getByLabel("Search name or number").fill(name!);
  await panel.getByRole("button", { name: "Search", exact: true }).click();
  await expect(panel.locator(".history-results")).toContainText(
    `Matching “${name}”`,
  );
  await expect(
    records.first().locator(".history-record-main > strong"),
  ).toHaveText(name!);
});

test("notification redactions and usage record modes retain their meaning", async ({
  page,
}) => {
  await open(page, "notifications");
  const panel = page.locator(".phone-history");
  const redacted = panel
    .locator(".history-record")
    .filter({
      has: page.locator(".history-no-content"),
    })
    .first();
  await expect(redacted).toBeVisible();
  await expect(redacted).toContainText("No notification text available");
  await redacted.locator(".history-record-summary").click();
  await redacted.locator(".history-metadata > summary").click();
  await expect(redacted.locator(".history-metadata")).toContainText(
    "Not recorded",
  );
  await panel.getByLabel("Notification event type").selectOption("active");
  await expect(panel.locator(".history-type-badge").first()).toHaveText(
    "Active when collected",
  );
  for (const badge of await panel
    .locator(".history-type-badge")
    .allTextContents()) {
    expect(badge).toBe("Active when collected");
  }

  await page
    .getByRole("navigation", { name: "Main views" })
    .getByRole("button", { name: "App usage", exact: true })
    .click();
  const daily = panel.getByRole("button", {
    name: "Daily totals",
    exact: true,
  });
  const sessions = panel.getByRole("button", {
    name: "Estimated sessions",
    exact: true,
  });
  await expect(daily).toHaveAttribute("aria-pressed", "true");
  await expect(panel.locator(".history-record")).toHaveCount(20);
  await expect(panel.locator(".history-usage-metrics").first()).toContainText(
    "foreground",
  );
  await expect(panel.locator(".history-explainer")).toContainText(
    "should not be added together",
  );
  await panel.getByRole("button", { name: "Next", exact: true }).click();
  await sessions.click();
  await expect(sessions).toHaveAttribute("aria-pressed", "true");
  await expect(
    panel.getByRole("button", { name: "Previous", exact: true }),
  ).toBeDisabled();
  await expect(panel.locator(".history-type-badge").first()).toContainText(
    "Estimated",
  );
  await expect(panel.locator(".history-usage-metrics")).toHaveCount(0);
  const search = panel.getByLabel("Search app or package");
  await search.fill("com.example.music");
  await panel.getByRole("button", { name: "Search", exact: true }).click();
  await expect(panel.locator(".history-record")).toHaveCount(20);
  await panel.getByRole("button", { name: "Next", exact: true }).click();
  await expect(panel.locator(".history-pagination")).toContainText("Page 2");
  await page.getByLabel("Date period").selectOption("30");
  await expect(sessions).toHaveAttribute("aria-pressed", "true");
  await expect(search).toHaveValue("com.example.music");
  await expect(panel.locator(".history-results")).toContainText(
    "Matching “com.example.music”",
  );
  await expect(
    panel.getByRole("button", { name: "Previous", exact: true }),
  ).toBeDisabled();
  await expect(panel.locator(".history-pagination")).toContainText("Page 1");
  await expect(panel.locator(".history-type-badge").first()).toContainText(
    "Estimated",
  );
});

test("phone histories and expanded metadata fit tablet and small mobile screens", async ({
  page,
}, testInfo) => {
  await open(page);
  const nav = page.getByRole("navigation", { name: "Main views" });
  for (const width of [834, 390, 320]) {
    await page.setViewportSize({ width, height: 844 });
    for (const section of sections) {
      await nav.getByRole("button", { name: section.nav, exact: true }).click();
      await expect(page.getByRole("heading", { level: 1 })).toHaveText(
        section.title,
      );
      const panel = page.locator(".phone-history");
      await expect(panel.locator(".history-record")).toHaveCount(20);
      await expect(
        panel.getByRole("button", { name: "Search", exact: true }),
      ).toBeVisible();
      if (width === 390) {
        await page.evaluate(() => window.scrollTo(0, 0));
        await page.screenshot({
          path: testInfo.outputPath(`${section.kind}-390.png`),
        });
      }
      const row = panel.locator(".history-record").first();
      await row.locator(".history-record-summary").click();
      await row.locator(".history-metadata > summary").click();
      await expect(row.locator(".history-identifiers")).toBeVisible();
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
      if (width === 320) {
        await page.screenshot({
          path: testInfo.outputPath(`${section.kind}-320-expanded.png`),
        });
      }
      await row.locator(".history-record-summary").click();
    }
  }
});
