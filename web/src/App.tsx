import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  BarChart3,
  Bell,
  CalendarDays,
  Check,
  ChevronLeft,
  ChevronRight,
  Clock,
  Download,
  Globe,
  Grid2X2,
  Info,
  ListFilter,
  LogOut,
  Map,
  MapPin,
  Navigation,
  Phone,
  Pause,
  Play,
  Plus,
  RefreshCw,
  Route,
  Search,
  Settings2,
  ShieldCheck,
  Users,
  X,
} from "lucide-react";
import { api, calendarShift, dateInput, parameters, timeLabel } from "./api";
import { colour } from "./mapData";
const MapView = lazy(() =>
  import("./MapView").then((m) => ({ default: m.MapView })),
);
import { EventDetails, EventFeed, StatusIcon } from "./EventFeed";
import { usePlaces } from "./usePlaces";
import { GmailPanel } from "./GmailPanel";
import { AnalyticsPage } from "./AnalyticsPage";
import { useUsage } from "./useUsage";
import { PublicLanding } from "./PublicLanding";
import { HistoryPage } from "./HistoryPage";
import type {
  Device,
  Event,
  Filters,
  History,
  Latest,
  Session,
  Summary,
} from "./types";
type View =
  | "live"
  | "journeys"
  | "logs"
  | "calls"
  | "notifications"
  | "app-usage"
  | "alerts"
  | "analytics";
const navigation: { id: View; label: string; icon: typeof Map }[] = [
  { id: "live", label: "Live", icon: Map },
  { id: "journeys", label: "Journeys", icon: Route },
  { id: "logs", label: "Activity logs", icon: Activity },
  { id: "calls", label: "Calls", icon: Phone },
  { id: "notifications", label: "Notifications", icon: Bell },
  { id: "app-usage", label: "App usage", icon: Grid2X2 },
  { id: "alerts", label: "Alerts", icon: Bell },
  { id: "analytics", label: "Analytics", icon: BarChart3 },
];
function initialise(session: Session): Filters {
  const q = new URLSearchParams(location.search);
  const zone = q.get("tz") || "Asia/Kolkata";
  let timezone = zone;
  try {
    new Intl.DateTimeFormat("en", { timeZone: zone });
  } catch {
    timezone = "Asia/Kolkata";
  }
  const today = dateInput(session.now, timezone);
  const validDay = (v: string | null) => {
    if (!v || !/^\d{4}-\d{2}-\d{2}$/.test(v)) return false;
    const stamp = new Date(v + "T00:00:00Z");
    return (
      !Number.isNaN(stamp.valueOf()) && stamp.toISOString().slice(0, 10) === v
    );
  };
  return {
    devices: q.get("devices")?.split(",").filter(Boolean) ?? [],
    from: validDay(q.get("from")) ? q.get("from")! : today,
    to: validDay(q.get("to")) ? q.get("to")! : today,
    timezone,
    reasons: q.get("reason") || "",
    severity: q.get("severity") || "",
    outcomes: q.get("outcome") || "",
    statuses: q.get("status") || "",
    transports: q.get("network") || "",
  };
}
const initials = (name: string) =>
  name
    .split(/\s+/)
    .map((n) => n[0])
    .slice(0, 2)
    .join("")
    .toUpperCase();
function ErrorBox({ error }: { error: unknown }) {
  return error ? (
    <div className="inline-error" role="alert">
      <Info size={17} />
      {error instanceof Error ? error.message : "Unable to load data."}
    </div>
  ) : null;
}
function Age({ stamp, now }: { stamp: string; now: string }) {
  const minutes = Math.max(
    0,
    Math.floor((Date.parse(now) - Date.parse(stamp)) / 60000),
  );
  return (
    <>
      {minutes < 60
        ? `${minutes}m`
        : minutes < 1440
          ? `${Math.floor(minutes / 60)}h ${minutes % 60}m`
          : `${Math.floor(minutes / 1440)}d`}{" "}
      ago
    </>
  );
}

export default function App() {
  const session = useQuery({
    queryKey: ["session"],
    queryFn: ({ signal }) => api<Session>("/api/session", { signal }),
    retry: 1,
    refetchInterval: 60000,
  });
  if (!session.data?.authenticated)
    return (
      <PublicLanding
        loginUrl={session.data?.login_url}
        checking={session.isPending}
        error={!!session.error}
        onRetry={() => session.refetch()}
      />
    );
  return <Workspace session={session.data!} />;
}
function Workspace({ session }: { session: Session }) {
  const [view, setView] = useState<View>(() => {
      const v = new URLSearchParams(location.search).get("view") as View;
      return navigation.some((n) => n.id === v) &&
        (v !== "analytics" || session.user?.is_advanced)
        ? v
        : "journeys";
    }),
    [filters, setFilters] = useState(() => initialise(session)),
    [generation, setGeneration] = useState(0),
    [search, setSearch] = useState(""),
    [peopleOpen, setPeopleOpen] = useState(false),
    [addOpen, setAddOpen] = useState(false),
    [focused, setFocused] = useState<Event | null>(null),
    [activeDevice, setActiveDevice] = useState(""),
    [period, setPeriod] = useState(() => {
      const today = dateInput(session.now, filters.timezone);
      const days =
        (Date.parse(filters.to) - Date.parse(filters.from)) / 86400000 + 1;
      return filters.to === today && [1, 7, 30, 90].includes(days)
        ? String(days)
        : "custom";
    }),
    [dateError, setDateError] = useState(""),
    [dateDraft, setDateDraft] = useState({
      from: filters.from,
      to: filters.to,
    }),
    [playIndex, setPlayIndex] = useState<number | null>(null),
    [playing, setPlaying] = useState(false);
  const historyView =
    view === "calls" || view === "notifications" || view === "app-usage";
  const activeNav = useRef<HTMLButtonElement | null>(null);
  useEffect(() => {
    activeNav.current?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }, [view]);
  useUsage(!!session.analytics_enabled, view);
  const availableNavigation = navigation.filter(
    (n) => n.id !== "analytics" || session.user?.is_advanced,
  );
  const registry = useQuery({
    queryKey: ["devices"],
    queryFn: ({ signal }) =>
      api<{ items: Device[] }>("/api/devices", { signal }),
    staleTime: 30000,
    refetchOnWindowFocus: false,
  });
  const devices = registry.data?.items ?? [];
  const initialised = useRef(false);
  useEffect(() => {
    if (!registry.data || initialised.current) return;
    initialised.current = true;
    const allowed = new Set(devices.map((d) => d.device_id));
    setFilters((f) => ({
      ...f,
      devices: f.devices.length
        ? f.devices.filter((id) => allowed.has(id))
        : devices.slice(0, 2).map((d) => d.device_id),
    }));
    if (devices[0]) setActiveDevice(devices[0].device_id);
  }, [registry.data]);
  const selected = useMemo(
    () => devices.filter((d) => filters.devices.includes(d.device_id)),
    [devices, filters.devices],
  );
  const apply = (patch: Partial<Filters>) => {
    setFilters((f) => ({ ...f, ...patch }));
    setGeneration((g) => g + 1);
    setFocused(null);
    setPlayIndex(null);
    setPlaying(false);
  };
  const go = (v: View) => {
    setView(v);
    setGeneration((g) => g + 1);
    setFocused(null);
    setPlayIndex(null);
    setPlaying(false);
  };
  useEffect(() => {
    if (!initialised.current) return;
    const q = new URLSearchParams({
      view,
      devices: [...filters.devices].sort().join(","),
      from: filters.from,
      to: filters.to,
      tz: filters.timezone,
    });
    for (const [key, value] of Object.entries({
      reason: filters.reasons,
      severity: filters.severity,
      outcome: filters.outcomes,
      status: filters.statuses,
      network: filters.transports,
    }))
      if (value) q.set(key, value);
    history.replaceState(null, "", "?" + q);
  }, [view, filters]);
  const today = dateInput(session.now, filters.timezone),
    oldest = calendarShift(today, -89);
  const mapParams = parameters(filters, false).toString();
  const latest = useQuery({
    queryKey: ["latest", filters.devices],
    queryFn: ({ signal }) =>
      api<{ items: Latest[]; as_of: string }>(
        "/api/locations/latest?" +
          new URLSearchParams({ device_ids: filters.devices.join(",") }),
        { signal },
      ),
    enabled: initialised.current && !historyView,
    refetchInterval: view === "live" ? 30000 : false,
    refetchOnWindowFocus: false,
  });
  const historyQuery = useQuery({
    queryKey: ["history", mapParams],
    queryFn: ({ signal }) =>
      api<
        History & {
          interruptions?: { device_id: string; recorded_at: string }[];
        }
      >("/api/locations/history?" + mapParams, { signal }),
    enabled: initialised.current && view === "journeys",
    refetchOnWindowFocus: false,
  });
  const summary = useQuery({
    queryKey: ["summary", mapParams],
    queryFn: ({ signal }) =>
      api<Summary>("/api/activity/summary?" + mapParams, { signal }),
    enabled: initialised.current && view === "journeys",
    refetchOnWindowFocus: false,
  });
  const historyEvents = historyQuery.data?.items ?? [];
  const liveEvents = (latest.data?.items ?? []).flatMap((x) =>
    x.latest_fix ? [x.latest_fix] : [],
  );
  const mapEvents = view === "live" ? liveEvents : historyEvents;
  const focusedDevice =
    activeDevice && filters.devices.includes(activeDevice)
      ? activeDevice
      : selected[0]?.device_id;
  const dayEvents = useMemo(
    () =>
      historyEvents
        .filter((e) => e.device_id === focusedDevice)
        .sort((a, b) => a.captured_at!.localeCompare(b.captured_at!)),
    [historyEvents, focusedDevice],
  );
  const placeQuery = usePlaces(
    view === "live"
      ? liveEvents
      : view === "journeys"
        ? [...dayEvents.slice(-10), ...(focused ? [focused] : [])]
        : [],
  );
  const places = placeQuery.data?.items;
  const times = useMemo(
    () =>
      [
        ...new Set(
          historyEvents.flatMap((e) => (e.captured_at ? [e.captured_at] : [])),
        ),
      ].sort(),
    [historyEvents],
  );
  const playbackEvents =
    playIndex === null
      ? mapEvents
      : mapEvents.filter((e) => e.captured_at! <= times[playIndex]);
  useEffect(() => {
    if (!playing) return;
    const id = setInterval(
      () =>
        setPlayIndex((i) => {
          const next = (i ?? -1) + 1;
          if (next >= times.length - 1) {
            setPlaying(false);
            return Math.max(0, times.length - 1);
          }
          return next;
        }),
      700,
    );
    return () => clearInterval(id);
  }, [playing, times.length]);
  const toggleDevice = (id: string) => {
    const next = filters.devices.includes(id)
      ? filters.devices.filter((x) => x !== id)
      : [...filters.devices, id];
    if (next.length <= session.max_devices) apply({ devices: next });
  };
  const preset = (value: string) => {
    setPeriod(value);
    if (value === "custom") return;
    const from = calendarShift(today, 1 - Number(value));
    setDateDraft({ from, to: today });
    setDateError("");
    apply({ from, to: today });
  };
  const applyDates = () => {
    if (
      !dateDraft.from ||
      !dateDraft.to ||
      dateDraft.from > dateDraft.to ||
      dateDraft.from < oldest ||
      dateDraft.to > today
    ) {
      setDateError("Choose a valid date range within the last 90 days.");
      return;
    }
    setDateError("");
    apply({ from: dateDraft.from, to: dateDraft.to });
  };
  const reset = () => {
    setPeriod("1");
    setDateDraft({ from: today, to: today });
    setDateError("");
    setSearch("");
    apply({
      devices: devices.slice(0, 2).map((d) => d.device_id),
      from: today,
      to: today,
      reasons: "",
      severity: "",
      outcomes: "",
      statuses: "",
      transports: "",
    });
  };
  const onMap = (e: Event) => {
    setView("journeys");
    setGeneration((g) => g + 1);
    setFocused(e);
    setActiveDevice(e.device_id);
  };
  const titles: Record<View, [string, string]> = {
    live: [
      "A place for everyone.",
      "The latest location each device has reported.",
    ],
    journeys: [
      "Every journey, in view.",
      "Revisit the places and moments between A and B.",
    ],
    logs: [
      "Every event, in context.",
      "The actual reports, interruptions and recoveries along the way.",
    ],
    analytics: [
      "Understand the moments behind the map.",
      "App users, sessions and the services they use.",
    ],
    calls: [
      "Calls history",
      "Calls recorded on your devices, with their duration and details.",
    ],
    notifications: [
      "Notification history",
      "Revisit the notification text and events your devices captured.",
    ],
    "app-usage": [
      "App usage history",
      "Explore daily app usage and estimated sessions on your devices.",
    ],
    alerts: [
      "Stay a little closer.",
      "Thoughtful signals. Fewer things to keep in mind.",
    ],
  };
  const deviceList = (
    <>
      <div className="panel-heading">
        <div>
          <h2>
            Your devices <span>{devices.length}</span>
          </h2>
          <p>Choose who appears in this view</p>
        </div>
        <button
          className="icon-button"
          aria-label="Add existing device"
          onClick={() => setAddOpen(true)}
        >
          <Plus size={17} />
        </button>
      </div>
      <label className="search-input">
        <Search size={15} />
        <input
          type="search"
          placeholder="Search device name"
          aria-label="Search device name"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </label>
      <div className="selection-actions">
        <span>{filters.devices.length} selected</span>
        <button
          onClick={() =>
            apply({
              devices: devices
                .slice(0, session.max_devices)
                .map((d) => d.device_id),
            })
          }
        >
          Select all
        </button>
        <button onClick={() => apply({ devices: [] })}>Clear</button>
      </div>
      <div className="device-list">
        {devices
          .filter((d) =>
            (d.device_name + " " + d.detected_device_name)
              .toLowerCase()
              .includes(search.toLowerCase()),
          )
          .map((d) => {
            const state = latest.data?.items.find(
                (x) => x.device_id === d.device_id,
              ),
              color = colour(d.device_id, devices);
            return (
              <div
                className={
                  "device-row " +
                  (focusedDevice === d.device_id ? "focused" : "")
                }
                key={d.device_id}
                style={{ "--device-color": color } as React.CSSProperties}
              >
                <label className="device-check">
                  <input
                    type="checkbox"
                    checked={filters.devices.includes(d.device_id)}
                    aria-label={"Show " + d.device_name}
                    onChange={() => toggleDevice(d.device_id)}
                  />
                </label>
                <button
                  className="device-focus"
                  onClick={() => {
                    setActiveDevice(d.device_id);
                    if (!filters.devices.includes(d.device_id))
                      apply({
                        devices: [...filters.devices, d.device_id].slice(
                          -session.max_devices,
                        ),
                      });
                  }}
                >
                  <span className="avatar">{initials(d.device_name)}</span>
                  <span>
                    <b>{d.device_name}</b>
                    <small>
                      {historyView
                        ? d.detected_device_name
                        : (state?.latest_fix?.place?.label ??
                          places?.[state?.latest_fix?.event_id ?? ""]?.place
                            ?.label ??
                          d.detected_device_name)}
                    </small>
                    <span className="device-report">
                      {historyView ? (
                        filters.devices.includes(d.device_id) ? (
                          "Included in this history"
                        ) : (
                          "Select to include history"
                        )
                      ) : state?.latest_fix?.captured_at ? (
                        <>
                          <i />
                          Measured{" "}
                          <Age
                            stamp={state.latest_fix.captured_at}
                            now={latest.data?.as_of ?? session.now}
                          />
                        </>
                      ) : (
                        "No position yet"
                      )}
                    </span>
                    <span className="device-id">
                      {d.device_id.slice(-8)}
                      {d.revoked ? " · Upload access revoked" : ""}
                    </span>
                  </span>
                </button>
              </div>
            );
          })}
        {registry.isPending && <p className="muted-note">Loading devices…</p>}
        {!devices.length && !registry.isPending && (
          <p className="muted-note">No devices are assigned to this account.</p>
        )}
      </div>
      <ErrorBox error={registry.error} />
    </>
  );
  return (
    <>
      <aside className="rail">
        <a className="brand-symbol" href="/" aria-label="Location Keeper">
          <MapPin size={25} />
        </a>
        <div className="rail-divider" />
        {availableNavigation.map((n) => (
          <button
            key={n.id}
            className={view === n.id ? "active" : ""}
            aria-label={n.label}
            onClick={() => go(n.id)}
          >
            <n.icon size={21} />
          </button>
        ))}
        <span className="rail-spacer" />
        <span className="user-avatar" title={session.user?.name}>
          {session.demo ? "DE" : initials(session.user?.name ?? "Viewer")}
        </span>
      </aside>
      <div className="workspace">
        <header className="topbar">
          <a className="wordmark" href="/">
            <MapPin className="mobile-symbol" />
            location<span>keeper</span>
            <small>ADMIN</small>
          </a>
          <nav aria-label="Main views">
            {availableNavigation.map((n) => (
              <button
                key={n.id}
                ref={view === n.id ? activeNav : undefined}
                aria-current={view === n.id ? "page" : undefined}
                className={view === n.id ? "active" : ""}
                onClick={() => go(n.id)}
              >
                <n.icon size={16} />
                {n.label}
                {n.id === "alerts" && <small>SOON</small>}
              </button>
            ))}
          </nav>
          {!session.demo && (
            <button
              className="signout"
              aria-label="Sign out"
              onClick={async () => {
                await api("/auth/logout", { method: "POST" });
                location.assign("/");
              }}
            >
              <LogOut size={17} />
            </button>
          )}
        </header>
        <main>
          <div className="page-heading">
            <div>
              <span className="eyebrow">
                A LITTLE CONTEXT. A CLEARER PICTURE.
              </span>
              <h1>{titles[view][0]}</h1>
              <p>{titles[view][1]}</p>
            </div>
            <span className={"workspace-mode " + (session.demo ? "demo" : "")}>
              <ShieldCheck size={14} />
              {session.demo ? "DEMO WORKSPACE" : "PRIVATE WORKSPACE"}
            </span>
          </div>
          {session.demo ? (
            <p className="demo-notice">
              <span />
              Synthetic demo · Fictional people and coordinates · No private
              database connected
            </p>
          ) : (
            <p className="demo-notice">
              <ShieldCheck size={12} />
              Only devices authorised for your signed-in account are available.
            </p>
          )}
          {view === "analytics" ? (
            session.user?.is_advanced ? (
              <AnalyticsPage timezone={filters.timezone} />
            ) : (
              <p>Advanced access is required.</p>
            )
          ) : view === "alerts" ? (
            <Alerts session={session} />
          ) : (
            <>
              <section
                className="filterbar"
                aria-label="Device and date filters"
              >
                {view !== "live" && (
                  <div className="date-control">
                    <CalendarDays size={15} />
                    <select
                      aria-label="Date period"
                      value={period}
                      onChange={(e) => preset(e.target.value)}
                    >
                      <option value="1">Today</option>
                      <option value="7">Last 7 days</option>
                      <option value="30">Last 30 days</option>
                      <option value="90">Last 90 days</option>
                      <option value="custom">Custom dates</option>
                    </select>
                    <span>
                      {filters.from === filters.to
                        ? filters.to
                        : `${filters.from} – ${filters.to}`}
                    </span>
                  </div>
                )}
                <button
                  className={"people-button " + (peopleOpen ? "active" : "")}
                  aria-expanded={peopleOpen}
                  onClick={() => setPeopleOpen(!peopleOpen)}
                >
                  <Users size={15} />
                  {selected.length}{" "}
                  {selected.length === 1 ? "device" : "devices"}
                </button>
                <label className="timezone-control">
                  <Globe size={13} />
                  <select
                    aria-label="Timezone"
                    value={filters.timezone}
                    onChange={(e) => {
                      const zone = e.target.value;
                      const newToday = dateInput(session.now, zone);
                      apply({
                        timezone: zone,
                        from: period === "1" ? newToday : filters.from,
                        to: period === "1" ? newToday : filters.to,
                      });
                      if (period === "1")
                        setDateDraft({ from: newToday, to: newToday });
                    }}
                  >
                    {[
                      "Asia/Kolkata",
                      "UTC",
                      "Europe/London",
                      "America/New_York",
                      "America/Los_Angeles",
                      "Australia/Sydney",
                    ].map((z) => (
                      <option key={z} value={z}>
                        {z === "Asia/Kolkata" ? "IST · UTC+5:30" : z}
                      </option>
                    ))}
                  </select>
                </label>
                <button className="text-button reset-button" onClick={reset}>
                  Reset filters
                </button>
                {period === "custom" && view !== "live" && (
                  <div className="custom-dates">
                    <label>
                      From
                      <input
                        type="date"
                        aria-label="From date"
                        min={oldest}
                        max={today}
                        value={dateDraft.from}
                        onChange={(e) =>
                          setDateDraft((d) => ({ ...d, from: e.target.value }))
                        }
                      />
                    </label>
                    <label>
                      To
                      <input
                        type="date"
                        aria-label="To date"
                        min={oldest}
                        max={today}
                        value={dateDraft.to}
                        onChange={(e) =>
                          setDateDraft((d) => ({ ...d, to: e.target.value }))
                        }
                      />
                    </label>
                    <button className="small-primary" onClick={applyDates}>
                      Apply dates
                    </button>
                    {dateError && (
                      <span role="alert" className="date-error">
                        {dateError}
                      </span>
                    )}
                  </div>
                )}
              </section>
              <div
                className={
                  "explorer " +
                  (view === "logs" || historyView ? "logs-layout" : "")
                }
              >
                <aside
                  className={
                    "people-panel " + (peopleOpen ? "mobile-open" : "")
                  }
                >
                  {deviceList}
                  {view === "journeys" && (
                    <div className="sidebar-activity">
                      <div className="activity-title">
                        <h2>Observed places</h2>
                        <span>
                          {filters.from === filters.to
                            ? "SELECTED DAY"
                            : "SELECTED PERIOD"}
                        </span>
                      </div>
                      <p>
                        {devices.find((d) => d.device_id === focusedDevice)
                          ?.device_name ?? "Choose a device"}
                      </p>
                      <div className="mini-timeline">
                        {dayEvents.slice(-10).map((e) => (
                          <button
                            key={e.event_id}
                            onClick={() => setFocused(e)}
                          >
                            <i />
                            <span>
                              <b>
                                {e.place?.label ??
                                  places?.[e.event_id]?.place?.label ??
                                  "Recorded position"}
                              </b>
                              <small>
                                ±{e.accuracy_m} m ·{" "}
                                {e.location_status.replace("_", " ")}
                              </small>
                            </span>
                            <time>
                              {timeLabel(e.captured_at, filters.timezone)}
                            </time>
                          </button>
                        ))}
                        {!dayEvents.length && (
                          <span className="muted-note">
                            No recorded positions in this period.
                          </span>
                        )}
                      </div>
                      <p className="sidebar-note">
                        <Info size={13} />
                        Links are estimates between recorded points.
                      </p>
                    </div>
                  )}
                </aside>
                <div className="content-column">
                  {historyView ? (
                    <HistoryPage
                      key={view}
                      kind={view as "calls" | "notifications" | "app-usage"}
                      filters={filters}
                      devices={devices}
                      generation={generation}
                    />
                  ) : view === "logs" ? (
                    <>
                      <div className="log-filters" aria-label="Event filters">
                        <ListFilter size={16} />
                        {(
                          [
                            {
                              key: "reasons",
                              label: "Event reason",
                              values: session.reasons,
                            },
                            {
                              key: "severity",
                              label: "Severity",
                              values: [
                                "attention",
                                "positive",
                                "limited",
                                "neutral",
                              ],
                            },
                            {
                              key: "outcomes",
                              label: "Capture outcome",
                              values: session.outcomes,
                            },
                            {
                              key: "statuses",
                              label: "Fix status",
                              values: session.statuses,
                            },
                            {
                              key: "transports",
                              label: "Network",
                              values: session.transports,
                            },
                          ] as const
                        ).map((f) => (
                          <select
                            key={f.key}
                            aria-label={f.label}
                            value={filters[f.key]}
                            onChange={(e) => apply({ [f.key]: e.target.value })}
                          >
                            <option value="">
                              {
                                {
                                  reasons: "All event reasons",
                                  severity: "All severities",
                                  outcomes: "All capture outcomes",
                                  statuses: "All fix statuses",
                                  transports: "All networks",
                                }[f.key]
                              }
                            </option>
                            {f.values.map((v) => (
                              <option key={v} value={v}>
                                {v.replaceAll("_", " ")}
                              </option>
                            ))}
                          </select>
                        ))}
                      </div>
                      <EventFeed
                        key={generation}
                        filters={filters}
                        devices={devices}
                        generation={generation}
                        onMap={onMap}
                        onRestart={() => setGeneration((g) => g + 1)}
                      />
                    </>
                  ) : (
                    <>
                      <Suspense
                        fallback={
                          <div className="map-shell map-loading">
                            Loading the map…
                          </div>
                        }
                      >
                        <MapView
                          live={view === "live"}
                          events={playbackEvents}
                          devices={selected}
                          registry={devices}
                          style={session.map_style}
                          onSelect={setFocused}
                          lines={
                            view === "journeys" &&
                            filters.from === filters.to &&
                            !historyQuery.data?.simplified
                          }
                          focused={focused}
                          breaks={historyQuery.data?.interruptions}
                        />
                      </Suspense>
                      <ErrorBox
                        error={
                          view === "live" ? latest.error : historyQuery.error
                        }
                      />
                      {view === "journeys" ? (
                        <>
                          <section className="playback-panel">
                            <div className="playback-title">
                              <div>
                                <span className="eyebrow">
                                  THE PERIOD AT A GLANCE
                                </span>
                                <h2>
                                  {filters.from === filters.to
                                    ? new Intl.DateTimeFormat("en-GB", {
                                        weekday: "long",
                                        day: "numeric",
                                        month: "long",
                                      }).format(
                                        new Date(filters.to + "T12:00:00Z"),
                                      )
                                    : `${filters.from} – ${filters.to}`}
                                </h2>
                              </div>
                              <span className="estimated-label">
                                ┄ Observed links · estimated
                              </span>
                            </div>
                            {historyQuery.data?.simplified && (
                              <p className="resolution-note">
                                Showing daily representative observations (
                                {historyQuery.data.returned_observations} of{" "}
                                {historyQuery.data.total_observations}). Choose
                                a day for detail.
                              </p>
                            )}
                            <div className="playback-controls">
                              <button
                                className="play-button"
                                aria-label={
                                  playing
                                    ? "Pause playback"
                                    : "Play recorded points"
                                }
                                disabled={times.length < 2}
                                onClick={() => {
                                  if (
                                    !playing &&
                                    (playIndex === null ||
                                      playIndex >= times.length - 1)
                                  )
                                    setPlayIndex(0);
                                  setPlaying(!playing);
                                }}
                              >
                                {playing ? (
                                  <Pause size={15} />
                                ) : (
                                  <Play size={15} />
                                )}
                              </button>
                              <div className="playback-slider">
                                <div>
                                  <b>
                                    {playIndex === null
                                      ? "All recorded points"
                                      : timeLabel(
                                          times[playIndex],
                                          filters.timezone,
                                          true,
                                        )}
                                  </b>
                                  <span>
                                    {historyEvents.length.toLocaleString()}{" "}
                                    points
                                  </span>
                                </div>
                                <input
                                  type="range"
                                  min="0"
                                  max={Math.max(0, times.length - 1)}
                                  value={
                                    playIndex ?? Math.max(0, times.length - 1)
                                  }
                                  disabled={times.length < 2}
                                  aria-label="Recorded point playback"
                                  onChange={(e) => {
                                    setPlaying(false);
                                    setPlayIndex(+e.target.value);
                                  }}
                                />
                                <div className="slider-ticks">
                                  <span>
                                    {times[0]
                                      ? timeLabel(times[0], filters.timezone)
                                      : "—"}
                                  </span>
                                  <span>Recorded observations only</span>
                                  <span>
                                    {times.at(-1)
                                      ? timeLabel(
                                          times.at(-1)!,
                                          filters.timezone,
                                        )
                                      : "—"}
                                  </span>
                                </div>
                              </div>
                              <button
                                className="text-button"
                                onClick={() => {
                                  setPlaying(false);
                                  setPlayIndex(null);
                                }}
                              >
                                Show all
                              </button>
                            </div>
                            <div className="playback-foot">
                              <span>
                                {selected.length} devices ·{" "}
                                {historyQuery.data?.total_observations ?? 0}{" "}
                                observations in period
                              </span>
                              <span>
                                <Info size={11} />
                                The route between reports is unknown.
                              </span>
                            </div>
                          </section>
                          <section className="history-panel">
                            <div>
                              <h2>Daily activity</h2>
                              <span>Select a day to explore</span>
                            </div>
                            <div className="day-buttons">
                              {(summary.data?.days ?? []).map((d) => (
                                <button
                                  title={`${d.date}: ${d.events} events, ${d.fixes} positions`}
                                  aria-label={"Explore " + d.date}
                                  key={d.date}
                                  onClick={() => {
                                    setPeriod("custom");
                                    setDateDraft({ from: d.date, to: d.date });
                                    apply({ from: d.date, to: d.date });
                                  }}
                                >
                                  <i
                                    style={{
                                      height: Math.max(
                                        6,
                                        Math.min(
                                          32,
                                          d.events / selected.length,
                                        ),
                                      ),
                                    }}
                                  />
                                  <span>{d.date.slice(5)}</span>
                                </button>
                              ))}
                            </div>
                            <ErrorBox error={summary.error} />
                          </section>
                        </>
                      ) : (
                        <div className="live-cards">
                          {(latest.data?.items ?? []).map((x) => (
                            <article key={x.device_id}>
                              <div>
                                <i
                                  style={{
                                    background: colour(x.device_id, devices),
                                  }}
                                />
                                <h2>
                                  {
                                    devices.find(
                                      (d) => d.device_id === x.device_id,
                                    )?.device_name
                                  }
                                </h2>
                              </div>
                              <b>
                                {x.latest_fix?.place?.label ??
                                  places?.[x.latest_fix?.event_id ?? ""]?.place
                                    ?.label ??
                                  (session.geocoding_enabled
                                    ? "Area lookup pending"
                                    : "Place lookup not configured")}
                              </b>
                              <p>
                                {x.latest_fix?.captured_at ? (
                                  <>
                                    Position measured{" "}
                                    <Age
                                      stamp={x.latest_fix.captured_at}
                                      now={latest.data!.as_of}
                                    />{" "}
                                    · ±{x.latest_fix.accuracy_m} m
                                  </>
                                ) : (
                                  "No position reported"
                                )}
                              </p>
                              <small>
                                Latest observed event:{" "}
                                {x.latest_observed_event?.title ?? "None"}
                                {x.latest_observed_event &&
                                  ` · ${timeLabel(x.latest_observed_event.recorded_at, filters.timezone)}`}
                              </small>
                              <small>
                                At the latest observed event:{" "}
                                {x.latest_observed_event?.network_transport ===
                                "none"
                                  ? "Offline"
                                  : (x.latest_observed_event
                                      ?.network_transport ?? "Unknown")}{" "}
                                · Battery{" "}
                                {x.latest_observed_event?.battery_percent ===
                                  null ||
                                x.latest_observed_event?.battery_percent ===
                                  undefined
                                  ? "unknown"
                                  : x.latest_observed_event.battery_percent +
                                    "%"}
                              </small>
                              <small>
                                Latest upload:{" "}
                                {x.latest_event
                                  ? timeLabel(
                                      x.latest_event.received_at,
                                      filters.timezone,
                                      true,
                                    )
                                  : "Not recorded"}
                              </small>
                              <small>
                                Airplane mode:{" "}
                                {x.airplane_mode_enabled === null
                                  ? "Unknown"
                                  : x.airplane_mode_enabled
                                    ? "Enabled"
                                    : "Disabled"}
                                {x.airplane_observed_at &&
                                  ` · observed ${timeLabel(x.airplane_observed_at, filters.timezone)}`}
                              </small>
                            </article>
                          ))}
                        </div>
                      )}
                      {focused && (
                        <section className="selected-detail">
                          <header>
                            <div>
                              <span className="eyebrow">
                                SELECTED OBSERVATION
                              </span>
                              <h2>{focused.title}</h2>
                            </div>
                            <button
                              className="icon-button"
                              aria-label="Close observation details"
                              onClick={() => setFocused(null)}
                            >
                              <X size={16} />
                            </button>
                          </header>
                          <EventDetails
                            event={focused}
                            timezone={filters.timezone}
                            onMap={(e) => setFocused(e)}
                          />
                        </section>
                      )}
                    </>
                  )}
                </div>
              </div>
            </>
          )}
          <footer className="page-footer">
            <span>
              LOCATION KEEPER <i>·</i> A clearer sense of where.
              {" · "}
              <a href="/privacy.html">Privacy policy</a>
            </span>
            <span>
              {session.demo ? (
                "Synthetic data · MapLibre map"
              ) : session.geocoding_enabled ? (
                <a
                  href="https://www.geoapify.com/"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  Powered by Geoapify
                </a>
              ) : (
                "Private location workspace"
              )}
              <span>All times in {filters.timezone}</span>
            </span>
          </footer>
        </main>
      </div>
      {addOpen && (
        <div
          className="modal-backdrop"
          role="presentation"
          onClick={(e) => {
            if (e.target === e.currentTarget) setAddOpen(false);
          }}
        >
          <DeviceDialog
            devices={devices}
            selected={filters.devices}
            onClose={() => setAddOpen(false)}
            onAdd={(id) => {
              if (
                filters.devices.length < session.max_devices &&
                !filters.devices.includes(id)
              )
                apply({ devices: [...filters.devices, id] });
            }}
          />
        </div>
      )}
    </>
  );
}
function DeviceDialog({
  devices,
  selected,
  onAdd,
  onClose,
}: {
  devices: Device[];
  selected: string[];
  onAdd: (id: string) => void;
  onClose: () => void;
}) {
  const [search, setSearch] = useState("");
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    ref.current?.showModal();
    return () => ref.current?.close();
  }, []);
  return (
    <dialog
      ref={ref}
      onCancel={onClose}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      className="device-dialog"
    >
      <header>
        <div>
          <span className="eyebrow">YOUR MAP, YOUR DEVICES</span>
          <h2>Add an existing device</h2>
        </div>
        <button
          className="icon-button"
          aria-label="Close add device"
          onClick={onClose}
        >
          <X size={17} />
        </button>
      </header>
      <p>
        Find devices in your authorised registry. Adding a device here changes
        your selection.
      </p>
      <label className="dialog-search">
        Device name
        <input
          autoFocus
          type="search"
          placeholder="e.g. Travel phone"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </label>
      <div className="dialog-results">
        {devices
          .filter((d) =>
            d.device_name.toLowerCase().includes(search.toLowerCase()),
          )
          .map((d) => (
            <div key={d.device_id}>
              <span>
                <b>{d.device_name}</b>
                <small>
                  {d.detected_device_name} · {d.device_id.slice(-8)}
                </small>
              </span>
              <button
                disabled={selected.includes(d.device_id)}
                onClick={() => onAdd(d.device_id)}
              >
                {selected.includes(d.device_id) ? "Added" : "Add to view"}
              </button>
            </div>
          ))}
      </div>
      <p className="muted-note">
        New phone registration uses the separate phone setup flow.
      </p>
      <button className="primary" onClick={onClose}>
        Done <Check size={16} />
      </button>
    </dialog>
  );
}
function Alerts({ session }: { session: Session }) {
  return (
    <section className="alerts-view">
      <span className="roadmap-label">ON THE ROADMAP</span>
      <h2>A heads-up, when it matters.</h2>
      <p>
        Rules that turn location events into useful signals.
        <br />
        Coming soon. Automated alert monitoring is not active.
      </p>
      {session.user?.admin && <GmailPanel />}
      <div className="alert-cards">
        <article>
          <span className="alert-art">
            <Clock />
          </span>
          <span className="eyebrow">PLANNED RULE 01</span>
          <h3>Missed check-ins</h3>
          <p>
            Flag a device after <b>5 consecutive expected uploads</b> are
            missing, with a grace period for late delivery.
          </p>
          <div>
            <span>Expected cadence</span>
            <b>Per-device schedule</b>
          </div>
          <span className="coming-pill">Coming soon</span>
        </article>
        <article>
          <span className="alert-art">
            <Route />
          </span>
          <span className="eyebrow">PLANNED RULE 02</span>
          <h3>A longer journey</h3>
          <p>
            Flag estimated travel of{" "}
            <b>over 100 km within a rolling 3-hour window.</b> Distance and time
            will be configurable.
          </p>
          <div>
            <span>Distance confidence</span>
            <b>Based on available fixes</b>
          </div>
          <span className="coming-pill">Coming soon</span>
        </article>
      </div>
      <p className="alert-note">
        Recorded attention events are visible in Activity logs. Missing uploads
        and no GPS fix remain separate conditions.
      </p>
    </section>
  );
}
