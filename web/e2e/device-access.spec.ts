import { expect, test, type Page } from "@playwright/test";
import type { Device, Session } from "../src/types";

const now = "2026-11-22T06:00:00.000Z";
const phone = (id: string, locked = true, setup = false): Device => ({
  device_id: id,
  device_name: `${id} phone`,
  detected_device_name: "Test Android",
  label: "",
  revoked: false,
  locked,
  pin_setup_required: setup,
});

async function mockAccess(page: Page, devices = [phone("one"), phone("two")]) {
  const state = {
    devices,
    session: {
      authenticated: true,
      user: {
        subject: "account-one",
        email: "one@example.test",
        name: "Test User",
        admin: false,
        is_advanced: false,
      },
      analytics_enabled: false,
      demo: false,
      now,
      map_style: "https://example.test/style.json",
      geocoding_enabled: false,
      max_devices: 20,
      login_url: "/auth/login",
      reasons: [],
      outcomes: [],
      statuses: [],
      transports: [],
    } as Session,
    attempts: [] as { device_id: string; pin: string }[],
    dataRequests: [] as string[],
    denyHistory: false,
    sessionExpired: false,
    unlockFailure: null as null | { status: number; code: string },
  };
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const send = (body: unknown, status = 200) =>
      route.fulfill({ status, json: body });
    if (url.pathname === "/api/session") return send(state.session);
    if (url.pathname === "/api/devices") return send({ items: state.devices });
    if (url.pathname === "/api/devices/unlock") {
      const body = request.postDataJSON();
      state.attempts.push(body);
      if (state.unlockFailure)
        return send(
          { code: state.unlockFailure.code, error: "Unavailable" },
          state.unlockFailure.status,
        );
      if (body.pin !== "0007")
        return send({ code: "incorrect_pin", error: "Incorrect PIN" }, 403);
      state.devices = state.devices.map((device) =>
        device.device_id === body.device_id
          ? { ...device, locked: false }
          : device,
      );
      return send({ device_id: body.device_id, unlocked: true });
    }
    if (url.pathname.startsWith("/api/history/")) {
      state.dataRequests.push(request.url());
      if (state.sessionExpired)
        return send({ code: "unauthenticated", error: "Sign in again" }, 401);
      if (state.denyHistory)
        return send(
          { code: "device_locked", error: "Unlock this device" },
          403,
        );
      return send({
        items: (url.searchParams.get("device_ids") || "")
          .split(",")
          .filter(Boolean)
          .map((id) => ({
            event_id: id + "-call",
            device_id: id,
            event_type: "1",
            recorded_at: now,
            received_at: now,
            observed_at: now,
            fields: {
              name: id + " private caller",
              number: "+15550100",
              duration: "10",
            },
          })),
        next_cursor: null,
        has_more: false,
        as_of: now,
      });
    }
    state.dataRequests.push(request.url());
    return send({
      items: [],
      days: [],
      total_observations: 0,
      returned_observations: 0,
      simplified: false,
      limit: 5000,
      as_of: now,
    });
  });
  return state;
}

test("locked URL and stored selections never request data, and zero-unlocked cards stay visible on mobile", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const state = await mockAccess(page, [
    phone("one"),
    phone("setup", true, true),
  ]);
  await page.addInitScript(() =>
    localStorage.setItem("selectedDevices", '["one","unrelated"]'),
  );
  await page.goto("/?view=calls&devices=one,setup,unrelated");
  await expect(
    page.getByRole("form", { name: "Unlock one phone" }),
  ).toBeVisible();
  await expect(page.getByText("PIN setup needed")).toBeVisible();
  await expect(
    page
      .getByText("Email recovery is not available yet", { exact: false })
      .first(),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Unlock a device to view its history" }),
  ).toBeVisible();
  expect(state.dataRequests).toEqual([]);
  expect(
    await page
      .locator("body")
      .evaluate(() => document.documentElement.scrollWidth <= innerWidth),
  ).toBe(true);
  await page.screenshot({
    path: test.info().outputPath("locked-device-mobile.png"),
    fullPage: true,
  });
});

test("leading-zero PIN unlocks only its device and selection persists through a refresh", async ({
  page,
}) => {
  const state = await mockAccess(page);
  await page.goto("/?view=calls&devices=one,two");
  const first = page.getByRole("form", { name: "Unlock one phone" });
  await first.getByLabel("Device PIN").fill("1111");
  await first.getByRole("button", { name: "Unlock", exact: true }).click();
  await expect(first.getByRole("alert")).toContainText("Incorrect PIN");
  expect(state.dataRequests).toEqual([]);
  await first.getByLabel("Device PIN").fill("0007");
  await first.getByRole("button", { name: "Unlock", exact: true }).click();
  await expect(
    page
      .locator(".history-record-main > strong")
      .filter({ hasText: "one private caller" }),
  ).toBeVisible();
  await expect(
    page.getByRole("form", { name: "Unlock two phone" }),
  ).toBeVisible();
  expect(state.attempts).toEqual([
    { device_id: "one", pin: "1111" },
    { device_id: "one", pin: "0007" },
  ]);
  expect(
    state.dataRequests.every(
      (url) => new URL(url).searchParams.get("device_ids") === "one",
    ),
  ).toBe(true);
  await page.reload();
  await expect(
    page
      .locator(".history-record-main > strong")
      .filter({ hasText: "one private caller" }),
  ).toBeVisible();
  await expect(
    page.getByRole("form", { name: "Unlock two phone" }),
  ).toBeVisible();
  const second = page.getByRole("form", { name: "Unlock two phone" });
  await second.getByLabel("Device PIN").fill("0007");
  await second.getByRole("button", { name: "Unlock", exact: true }).click();
  await expect(
    page
      .locator(".history-record-main > strong")
      .filter({ hasText: "two private caller" }),
  ).toBeVisible();
  expect(state.attempts.at(-1)).toEqual({ device_id: "two", pin: "0007" });
});

test("rate-limit and verification failures leave devices locked", async ({
  page,
}) => {
  const state = await mockAccess(page);
  await page.goto("/?view=calls");
  const form = page.getByRole("form", { name: "Unlock one phone" });
  for (const failure of [
    { status: 429, code: "pin_rate_limited", message: "Too many attempts" },
    {
      status: 503,
      code: "pin_verification_unavailable",
      message: "PIN verification is unavailable",
    },
  ]) {
    state.unlockFailure = failure;
    await form.getByLabel("Device PIN").fill("0007");
    await form.getByRole("button", { name: "Unlock", exact: true }).click();
    await expect(form.getByRole("alert")).toContainText(failure.message);
    await expect(form.getByLabel("Device PIN")).toHaveValue("");
  }
  expect(state.dataRequests).toEqual([]);
});

test("access denial clears cached history and refreshes locked devices without a request loop", async ({
  page,
}) => {
  await page.clock.install();
  const state = await mockAccess(page, [phone("one", false)]);
  await page.goto("/?view=calls");
  await expect(
    page
      .locator(".history-record-main > strong")
      .filter({ hasText: "one private caller" }),
  ).toBeVisible();
  const initialRequests = state.dataRequests.length;
  state.devices = [phone("one", true)];
  state.denyHistory = true;
  await page.locator(".history-refresh").click();
  await expect(
    page.getByRole("form", { name: "Unlock one phone" }),
  ).toBeVisible();
  await expect(
    page
      .locator(".history-record-main > strong")
      .filter({ hasText: "one private caller" }),
  ).toHaveCount(0);
  await page.clock.fastForward(5000);
  expect(state.dataRequests).toHaveLength(initialRequests + 1);
});

test("account change removes the previous account’s cached history", async ({
  page,
}) => {
  await page.clock.install();
  const state = await mockAccess(page, [phone("one", false)]);
  await page.goto("/?view=calls");
  await expect(
    page
      .locator(".history-record-main > strong")
      .filter({ hasText: "one private caller" }),
  ).toBeVisible();
  state.session = {
    ...state.session,
    user: {
      subject: "account-two",
      email: "two@example.test",
      name: "Second user",
      admin: false,
      is_advanced: false,
    },
  };
  state.devices = [phone("two")];
  await page.clock.fastForward(61000);
  await expect(
    page.getByRole("form", { name: "Unlock two phone" }),
  ).toBeVisible();
  await expect(
    page
      .locator(".history-record-main > strong")
      .filter({ hasText: "one private caller" }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("heading", { name: "one phone", exact: true }),
  ).toHaveCount(0);
});

test("advanced accounts retain analytics navigation and unlocked history", async ({
  page,
}) => {
  const state = await mockAccess(page, [phone("one", false)]);
  state.session.user!.is_advanced = true;
  await page.goto("/?view=calls");
  await expect(
    page
      .locator(".history-record-main > strong")
      .filter({ hasText: "one private caller" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Analytics", exact: true }).last(),
  ).toBeVisible();
  await expect(page.locator(".device-unlock")).toHaveCount(0);
});

test("registry polling removes history immediately when a device becomes locked", async ({
  page,
}) => {
  await page.clock.install();
  const state = await mockAccess(page, [phone("one", false)]);
  await page.goto("/?view=calls");
  await expect(page.locator(".history-record-main > strong")).toContainText(
    "one private caller",
  );
  const requestsBeforeLock = state.dataRequests.length;
  state.devices = [phone("one")];
  await page.clock.fastForward(31000);
  await expect(
    page.getByRole("form", { name: "Unlock one phone" }),
  ).toBeVisible();
  await expect(page.locator(".history-record")).toHaveCount(0);
  expect(state.dataRequests).toHaveLength(requestsBeforeLock);
});

test("an expired session hides device history and returns to sign-in", async ({
  page,
}) => {
  const state = await mockAccess(page, [phone("one", false)]);
  await page.goto("/?view=calls");
  await expect(page.locator(".history-record-main > strong")).toContainText(
    "one private caller",
  );
  state.sessionExpired = true;
  await page.locator(".history-refresh").click();
  await expect(page.locator(".workspace")).toHaveCount(0);
  await expect(page.locator(".public-site")).toBeVisible();
  await expect(page.locator(".history-record")).toHaveCount(0);
});
