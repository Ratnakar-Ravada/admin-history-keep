import { describe, expect, it } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import {
  clearPrivateQueries,
  unlockedSelection,
  validDevicePIN,
} from "./deviceAccess";
import type { Device } from "./types";

const device = (device_id: string, locked: boolean, setup = false): Device => ({
  device_id,
  device_name: device_id,
  detected_device_name: "Phone",
  label: "",
  revoked: false,
  locked,
  pin_setup_required: setup,
});

describe("device PIN access", () => {
  it("accepts exactly four ASCII digits and preserves leading zeros", () => {
    expect(validDevicePIN("0007")).toBe(true);
    for (const pin of ["123", "12345", "12a3", "１２３４", " 1234", "1234\n"])
      expect(validDevicePIN(pin)).toBe(false);
  });

  it("filters untrusted selections independently for each unlocked device", () => {
    const registry = [
      device("one", false),
      device("two", true),
      device("setup", true, true),
    ];
    expect(
      unlockedSelection(["two", "unknown", "one", "one", "setup"], registry),
    ).toEqual(["one"]);
    expect(unlockedSelection(["two"], registry)).toEqual([]);
    expect(unlockedSelection(["one"], [device("one", true)])).toEqual([]);
    expect(
      unlockedSelection(
        ["one"],
        [{ ...device("one", false), locked: undefined } as unknown as Device],
      ),
    ).toEqual([]);
  });

  it("clears every private cache on logout or account change", () => {
    const client = new QueryClient();
    for (const key of [
      "session",
      "devices",
      "history",
      "phone-history",
      "places",
      "analytics-overview",
    ])
      client.setQueryData([key], { secret: key });
    clearPrivateQueries(client);
    expect(
      client
        .getQueryCache()
        .getAll()
        .map((query) => query.queryKey),
    ).toEqual([["session"]]);
  });

  it("retains only session and registry during access refresh", () => {
    const client = new QueryClient();
    for (const key of ["session", "devices", "events", "latest", "places"])
      client.setQueryData([key], { secret: key });
    clearPrivateQueries(client, true);
    expect(
      client
        .getQueryCache()
        .getAll()
        .map((query) => query.queryKey),
    ).toEqual([["session"], ["devices"]]);
  });
});
