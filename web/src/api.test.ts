import { describe, it, expect } from "vitest";
import { utcBoundary, calendarShift, parameters } from "./api";
describe("calendar filters", () => {
  it("uses IST midnight rather than the host timezone", () => {
    expect(utcBoundary("2026-11-18", "Asia/Kolkata")).toBe(
      "2026-11-17T18:30:00.000Z",
    );
    expect(utcBoundary("2026-11-18", "Asia/Kolkata", true)).toBe(
      "2026-11-18T18:30:00.000Z",
    );
  });
  it("handles the 25 hour New York DST transition", () => {
    const from = utcBoundary("2026-11-01", "America/New_York");
    const to = utcBoundary("2026-11-01", "America/New_York", true);
    expect(Date.parse(to) - Date.parse(from)).toBe(25 * 3600000);
  });
  it("handles quarter-hour timezone offsets", () => {
    expect(utcBoundary("2026-11-18", "Asia/Kathmandu")).toBe(
      "2026-11-17T18:15:00.000Z",
    );
  });
  it("keeps filter identity independent of device ordering", () => {
    const f = {
      devices: ["b", "a"],
      from: "2026-11-18",
      to: "2026-11-18",
      timezone: "UTC",
      reasons: "",
      severity: "",
      outcomes: "",
      statuses: "",
      transports: "",
    };
    expect(parameters(f).get("device_ids")).toBe("a,b");
    expect(calendarShift("2026-11-18", -89)).toBe("2026-08-21");
  });
});
