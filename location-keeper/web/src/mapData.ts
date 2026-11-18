import type { Device, Event } from "./types";
import type { Feature, FeatureCollection, Geometry } from "geojson";
function metres(a: number, b: number, c: number, d: number) {
  const r = Math.PI / 180;
  const v =
    Math.sin(((c - a) * r) / 2) ** 2 +
    Math.cos(a * r) * Math.cos(c * r) * Math.sin(((d - b) * r) / 2) ** 2;
  return 6371000 * 2 * Math.asin(Math.sqrt(Math.min(1, v)));
}
export const palette = [
  "#397254",
  "#bd8e53",
  "#6887b1",
  "#a17196",
  "#648d88",
  "#92844c",
];
export const colour = (id: string, devices: Device[]) =>
  palette[
    Math.max(
      0,
      devices.findIndex((d) => d.device_id === id),
    ) % palette.length
  ];
export function mapFeatures(
  events: Event[],
  devices: Device[],
  lines: boolean,
  accuracy: boolean,
  breaks: { device_id: string; recorded_at: string }[] = [],
): FeatureCollection {
  const features: Feature<Geometry>[] = [];
  for (const device of devices) {
    const points = events
      .filter(
        (e) =>
          e.device_id === device.device_id &&
          e.latitude !== null &&
          e.longitude !== null &&
          e.captured_at,
      )
      .sort(
        (a, b) =>
          a.captured_at!.localeCompare(b.captured_at!) ||
          a.event_id.localeCompare(b.event_id),
      );
    let previous: Event | undefined;
    const seen = new Set<string>();
    for (const e of points) {
      const key = e.latitude + "," + e.longitude + "," + e.captured_at;
      if (seen.has(key)) continue;
      seen.add(key);
      const color = colour(device.device_id, devices);
      features.push({
        type: "Feature",
        geometry: { type: "Point", coordinates: [e.longitude!, e.latitude!] },
        properties: {
          event_id: e.event_id,
          color,
          name: device.device_name,
          latest: e === points.at(-1),
        },
      });
      if (accuracy && e.accuracy_m !== null) {
        const coordinates: number[][] = [];
        for (let i = 0; i <= 48; i++) {
          const theta = (i / 48) * Math.PI * 2;
          const lat = e.latitude! + (e.accuracy_m / 111320) * Math.sin(theta);
          const lon =
            e.longitude! +
            (e.accuracy_m /
              (111320 *
                Math.max(0.01, Math.cos((e.latitude! * Math.PI) / 180)))) *
              Math.cos(theta);
          coordinates.push([lon, lat]);
        }
        features.push({
          type: "Feature",
          geometry: { type: "Polygon", coordinates: [coordinates] },
          properties: { color },
        });
      }
      if (
        lines &&
        previous &&
        e.location_status === "fresh" &&
        previous.location_status === "fresh" &&
        !e.is_mock &&
        !previous.is_mock &&
        (e.accuracy_m ?? Infinity) <= 200 &&
        (previous.accuracy_m ?? Infinity) <= 200
      ) {
        const diff =
          Date.parse(e.captured_at!) - Date.parse(previous.captured_at!);
        const interrupted = breaks.some(
          (b) =>
            b.device_id === e.device_id &&
            b.recorded_at > previous!.captured_at! &&
            b.recorded_at <= e.captured_at!,
        );
        const distance = metres(
          e.latitude!,
          e.longitude!,
          previous.latitude!,
          previous.longitude!,
        );
        if (
          diff > 0 &&
          diff <= 45 * 60000 &&
          !interrupted &&
          distance / (diff / 3600000) < 200000
        )
          features.push({
            type: "Feature",
            geometry: {
              type: "LineString",
              coordinates: [
                [previous.longitude!, previous.latitude!],
                [e.longitude!, e.latitude!],
              ],
            },
            properties: { color },
          });
      }
      previous = e;
    }
  }
  return { type: "FeatureCollection", features };
}
