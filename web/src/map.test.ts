import { describe, it, expect } from "vitest";
import { mapFeatures } from "./mapData";
import type { Device, Event } from "./types";
const device = {
  device_id: "one",
  device_name: "Fictional",
  detected_device_name: "Demo",
  label: "",
  revoked: false,
} as Device;
const event = (at: string, extra: Partial<Event> = {}) =>
  ({
    event_id: at,
    device_id: "one",
    captured_at: at,
    recorded_at: at,
    latitude: 51.5,
    longitude: -0.1,
    accuracy_m: 10,
    location_status: "fresh",
    is_mock: false,
    ...extra,
  }) as Event;
const links = (
  events: Event[],
  breaks: { device_id: string; recorded_at: string }[] = [],
) =>
  mapFeatures(events, [device], true, false, breaks).features.filter(
    (f) => f.geometry.type === "LineString",
  );
describe("honest map links", () => {
  it("connects consecutive high-quality observations", () => {
    expect(
      links([
        event("2026-11-18T10:00:00.000Z"),
        event("2026-11-18T10:30:00.000Z", { longitude: -0.11 }),
      ]),
    ).toHaveLength(1);
  });
  it("breaks paths across disabled tracking intervals", () => {
    expect(
      links(
        [event("2026-11-18T10:00:00.000Z"), event("2026-11-18T10:30:00.000Z")],
        [{ device_id: "one", recorded_at: "2026-11-18T10:15:00.000Z" }],
      ),
    ).toHaveLength(0);
  });
  it("does not connect last-known or coarse fixes", () => {
    expect(
      links([
        event("2026-11-18T10:00:00.000Z"),
        event("2026-11-18T10:30:00.000Z", { accuracy_m: 2000 }),
      ]),
    ).toHaveLength(0);
  });
  it("breaks gaps longer than 45 minutes", () => {
    expect(
      links([
        event("2026-11-18T10:00:00.000Z"),
        event("2026-11-18T11:30:00.000Z"),
      ]),
    ).toHaveLength(0);
  });
  it("rejects implausible jumps without removing observations", () => {
    const data = mapFeatures(
      [
        event("2026-11-18T10:00:00.000Z"),
        event("2026-11-18T10:30:00.000Z", { latitude: 12, longitude: 77 }),
      ],
      [device],
      true,
      false,
    );
    expect(
      data.features.filter((f) => f.geometry.type === "Point"),
    ).toHaveLength(2);
    expect(
      data.features.filter((f) => f.geometry.type === "LineString"),
    ).toHaveLength(0);
  });
});
