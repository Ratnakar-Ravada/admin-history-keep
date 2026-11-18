import type { Filters } from "./types";
export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}
export async function api<T>(
  path: string,
  init: RequestInit & { source?: "interaction" | "background" } = {},
): Promise<T> {
  const { source, ...request } = init;
  const currentView =
    typeof location !== "undefined"
      ? new URLSearchParams(location.search).get("view") || "journeys"
      : "unknown";
  const automatic =
    path.startsWith("/api/locations/latest") || path.startsWith("/api/places/");
  const background =
    source === "background" ||
    automatic ||
    (typeof document !== "undefined" && document.visibilityState === "hidden");
  const r = await fetch(path, {
    credentials: "same-origin",
    ...request,
    headers: {
      Accept: "application/json",
      "X-App-View": currentView,
      "X-App-Source": background ? "background" : "interaction",
      ...request.headers,
    },
  });
  if (!r.ok) {
    const e = await r.json().catch(() => ({}));
    throw new APIError(
      r.status,
      e.code || "request_failed",
      e.error || "Unable to load this data. Please try again.",
    );
  }
  return r.json();
}
export function dateInput(now: string, zone: string) {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: zone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(new Date(now));
}
// Convert local calendar midnight to UTC, including fractional offsets and DST.
export function utcBoundary(day: string, zone: string, end = false) {
  const [y, m, d] = day.split("-").map(Number);
  const target = Date.UTC(y, m - 1, d + (end ? 1 : 0));
  let guess = target;
  for (let i = 0; i < 4; i++) {
    const p = new Intl.DateTimeFormat("en-GB", {
      timeZone: zone,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hourCycle: "h23",
    }).formatToParts(new Date(guess));
    const v = Object.fromEntries(p.map((x) => [x.type, x.value]));
    const local = Date.UTC(
      +v.year,
      +v.month - 1,
      +v.day,
      +v.hour,
      +v.minute,
      +v.second,
    );
    const diff = target - local;
    guess += diff;
    if (!diff) break;
  }
  return new Date(guess).toISOString();
}
export function parameters(f: Filters, log = true) {
  const q = new URLSearchParams({
    device_ids: [...f.devices].sort().join(","),
    from: utcBoundary(f.from, f.timezone),
    to: utcBoundary(f.to, f.timezone, true),
    timezone: f.timezone,
  });
  if (log)
    for (const k of [
      "reasons",
      "severity",
      "outcomes",
      "statuses",
      "transports",
    ] as const)
      if (f[k]) q.set(k, f[k]);
  return q;
}
export const timeLabel = (s: string | null, zone: string, full = false) =>
  s
    ? new Intl.DateTimeFormat("en-GB", {
        timeZone: zone,
        ...(full ? { day: "numeric", month: "short", year: "numeric" } : {}),
        hour: "2-digit",
        minute: "2-digit",
        ...(full ? { second: "2-digit" } : {}),
      }).format(new Date(s))
    : "Not recorded";
export function calendarShift(day: string, offset: number) {
  const d = new Date(day + "T12:00:00Z");
  d.setUTCDate(d.getUTCDate() + offset);
  return d.toISOString().slice(0, 10);
}
