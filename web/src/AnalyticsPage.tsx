import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  BarChart3,
  Clock,
  RefreshCw,
  Users,
  ShieldCheck,
} from "lucide-react";
import { api } from "./api";
type Row = Record<string, any>;
type Overview = {
  totals: Row;
  accounts: Row;
  sessions: Row;
  daily: Row[];
  top_users: Row[];
  routes: Row[];
  services: Row[];
  views: Row[];
  pending_records: number;
  dropped_records: number;
  write_failures: number;
};
const number = (v: unknown) => Number(v ?? 0);
const count = (v: unknown) => number(v).toLocaleString();
const advanced = (v: unknown) => v === true || Number(v) === 1;
const serviceName = (v: string) =>
  ({
    activity_logs: "Activity logs",
    calls: "Calls history",
    notifications: "Notification history",
    app_usage: "Device app usage",
    live_locations: "Live locations",
    place_names: "Place names",
    authentication: "Sign-in & sessions",
    journeys: "Journeys",
    devices: "Device registry",
    email: "Email",
    analytics: "Analytics",
    usage: "Usage heartbeat",
  })[v] ?? v;
export function AnalyticsPage({ timezone }: { timezone: string }) {
  const [days, setDays] = useState("7"),
    [tab, setTab] = useState<"users" | "sessions" | "requests">("users"),
    [user, setUser] = useState(""),
    [label, setLabel] = useState(""),
    [session, setSession] = useState(""),
    [service, setService] = useState(""),
    [cursor, setCursor] = useState<string | null>(null),
    [previous, setPrevious] = useState<(string | null)[]>([]);
  const client = useQueryClient();
  const reset = () => {
    setCursor(null);
    setPrevious([]);
  };
  const overview = useQuery({
    queryKey: ["analytics-overview", days],
    queryFn: ({ signal }) =>
      api<Overview>("/api/analytics/overview?days=" + days, { signal }),
    refetchOnWindowFocus: false,
  });
  const params = new URLSearchParams({ days });
  if (user) params.set("user_id", user);
  if (session) params.set("session_id", session);
  if (service) params.set("service", service);
  if (cursor) params.set("cursor", cursor);
  const list = useQuery({
    queryKey: ["analytics-list", tab, params.toString()],
    queryFn: ({ signal }) =>
      api<{ items: Row[]; has_more: boolean; next_cursor: string | null }>(
        `/api/analytics/${tab}?${params}`,
        { signal },
      ),
    refetchOnWindowFocus: false,
    retry: 0,
  });
  const data = overview.data;
  const date = (value: unknown) => {
    if (!value) return "—";
    const d = new Date(String(value));
    return Number.isNaN(d.valueOf())
      ? "—"
      : new Intl.DateTimeFormat("en-GB", {
          timeZone: timezone,
          day: "numeric",
          month: "short",
          hour: "2-digit",
          minute: "2-digit",
        }).format(d);
  };
  const chooseUser = (row: Row, target: "sessions" | "requests") => {
    setUser(String(row.id));
    setLabel(String(row.email));
    setSession("");
    setService("");
    setTab(target);
    reset();
  };
  const rows = list.data?.items ?? [];
  const error = overview.error ?? list.error;
  return (
    <section
      className="analytics-page"
      aria-label="Application usage analytics"
    >
      <div className="analytics-toolbar">
        <span className="advanced-pill">
          <ShieldCheck size={14} /> Advanced access
        </span>
        <label>
          Period{" "}
          <select
            aria-label="Analytics period"
            value={days}
            onChange={(e) => {
              setDays(e.target.value);
              reset();
            }}
          >
            <option value="7">Last 7 days</option>
            <option value="30">Last 30 days</option>
            <option value="90">Last 90 days</option>
          </select>
        </label>
        <button
          className="text-button"
          onClick={() => {
            reset();
            void client.invalidateQueries({ queryKey: ["analytics-overview"] });
            void client.invalidateQueries({ queryKey: ["analytics-list"] });
          }}
        >
          <RefreshCw size={14} /> Refresh analytics
        </button>
      </div>
      <p className="analytics-explainer">
        Usage by app viewers, separate from the people whose phones report
        locations. Automatic polling is counted separately from user-driven
        requests.
      </p>
      {error && (
        <div className="inline-error" role="alert">
          {error instanceof Error ? error.message : "Unable to load analytics."}
          <button
            onClick={() => {
              void overview.refetch();
              void list.refetch();
            }}
          >
            Retry
          </button>
        </div>
      )}
      {overview.isPending ? (
        <div className="analytics-loading" role="status">
          Loading app usage…
        </div>
      ) : (
        data && (
          <>
            <div className="metric-grid">
              {[
                {
                  label: "Registered app users",
                  value: count(data.accounts.registered_users),
                  note: `${count(data.accounts.new_users)} new in this period`,
                  Icon: Users,
                },
                {
                  label: "Active users",
                  value: count(data.totals.active_users),
                  note: "Accounts making API requests",
                  Icon: Activity,
                },
                {
                  label: "Sessions",
                  value: count(data.sessions.sessions),
                  note: `${count(data.sessions.active_sessions)} active in the last 5 minutes`,
                  Icon: Users,
                },
                {
                  label: "API requests",
                  value: count(data.totals.requests),
                  note: `${count(data.totals.background_requests)} automatic / background`,
                  Icon: BarChart3,
                },
                {
                  label: "User-driven requests",
                  value: count(data.totals.interaction_requests),
                  note: "Automatic polling excluded",
                  Icon: Activity,
                },
                {
                  label: "Average API response",
                  value: `${Math.round(number(data.totals.average_ms))} ms`,
                  note: `${count(data.totals.errors)} responses with errors`,
                  Icon: Clock,
                },
              ].map((m) => (
                <article className="metric-card" key={m.label}>
                  <m.Icon size={18} />
                  <span>{m.label}</span>
                  <strong>{m.value}</strong>
                  <small>{m.note}</small>
                </article>
              ))}
            </div>
            <div className="analytics-panels">
              <article className="analytics-card">
                <h2>
                  Daily API traffic <small>UTC</small>
                </h2>
                <div
                  className="analytics-chart"
                  aria-label="Daily API request counts"
                >
                  {data.daily.length ? (
                    data.daily.map((r) => (
                      <div
                        className="analytics-day"
                        key={String(r.day)}
                        title={`${r.day}: ${count(r.requests)} requests`}
                      >
                        <b>{count(r.requests)}</b>
                        <i
                          style={{
                            height:
                              Math.max(
                                4,
                                (number(r.requests) /
                                  Math.max(
                                    1,
                                    ...data.daily.map((x) =>
                                      number(x.requests),
                                    ),
                                  )) *
                                  100,
                              ) + "%",
                          }}
                        />
                        <span>{String(r.day).slice(5, 10)}</span>
                      </div>
                    ))
                  ) : (
                    <p>No requests recorded in this period yet.</p>
                  )}
                </div>
              </article>
              <article className="analytics-card">
                <h2>Services used</h2>
                <div className="service-bars">
                  {data.services.map((r) => (
                    <button
                      key={String(r.service)}
                      onClick={() => {
                        setService(String(r.service));
                        setTab("requests");
                        reset();
                      }}
                    >
                      <span>
                        {serviceName(String(r.service))}
                        <b>{count(r.requests)}</b>
                      </span>
                      <i
                        style={{
                          width:
                            Math.max(
                              2,
                              (number(r.requests) /
                                Math.max(
                                  1,
                                  ...data.services.map((x) =>
                                    number(x.requests),
                                  ),
                                )) *
                                100,
                            ) + "%",
                        }}
                      />
                    </button>
                  ))}
                  {!data.services.length && (
                    <p>No service requests recorded.</p>
                  )}
                </div>
              </article>
            </div>
            <div className="analytics-panels">
              <article className="analytics-card">
                <h2>Most active users</h2>
                <p>
                  Ranked by user-driven API calls. Background polling does not
                  raise the primary rank.
                </p>
                <div className="top-users">
                  {data.top_users.map((r, i) => (
                    <button
                      key={String(r.id)}
                      onClick={() => chooseUser(r, "requests")}
                    >
                      <span className="rank">{i + 1}</span>
                      <span>
                        <b>{r.display_name}</b>
                        <small>{r.email}</small>
                      </span>
                      <strong>
                        {count(r.interaction_requests)}
                        <small>{count(r.requests)} total</small>
                      </strong>
                    </button>
                  ))}
                  {!data.top_users.length && (
                    <p>Users will appear after signing in and using the app.</p>
                  )}
                </div>
              </article>
              <article className="analytics-card">
                <h2>Most requested APIs</h2>
                <div className="route-ranking">
                  {data.routes.slice(0, 8).map((r) => (
                    <div key={String(r.method) + r.route}>
                      <code>{r.route}</code>
                      <span>
                        {count(r.requests)} calls ·{" "}
                        {Math.round(number(r.average_ms))} ms ·{" "}
                        {count(r.errors)} errors
                      </span>
                    </div>
                  ))}
                </div>
              </article>
            </div>
            <div className="analytics-card view-summary">
              <h2>Views opened</h2>
              {data.views.length ? (
                data.views.map((r) => (
                  <span key={String(r.app_view)}>
                    {r.app_view}
                    <b>{count(r.opens)}</b>
                  </span>
                ))
              ) : (
                <p>View opens are recorded as people navigate the app.</p>
              )}
            </div>
            {(data.dropped_records > 0 || data.write_failures > 0) && (
              <p className="inline-warning" role="status">
                Some usage records could not be saved:{" "}
                {count(data.dropped_records)} dropped records,{" "}
                {count(data.write_failures)} write failures since startup.
              </p>
            )}
          </>
        )
      )}
      <section
        className="analytics-card analytics-detail"
        aria-label="Analytics records"
      >
        <div className="analytics-list-toolbar">
          <div role="tablist" aria-label="Analytics record type">
            {(["users", "sessions", "requests"] as const).map((value) => (
              <button
                role="tab"
                aria-selected={tab === value}
                key={value}
                onClick={() => {
                  setTab(value);
                  if (value !== "requests") {
                    setService("");
                    setSession("");
                  }
                  if (value === "users") {
                    setUser("");
                    setLabel("");
                  }
                  reset();
                }}
              >
                {value === "users"
                  ? "App users"
                  : value === "sessions"
                    ? "Sessions"
                    : "API requests"}
              </button>
            ))}
          </div>
          {(user || session || service) && (
            <button
              className="text-button"
              onClick={() => {
                setUser("");
                setSession("");
                setService("");
                setLabel("");
                reset();
              }}
            >
              Clear analytics filters
            </button>
          )}
        </div>
        {(user || service) && (
          <p className="analytics-filter-label">
            {label && `User: ${label}`}
            {service && ` · Service: ${serviceName(service)}`}
            {session && " · One session"}
          </p>
        )}
        {list.isPending ? (
          <p role="status">Loading first 20 records…</p>
        ) : (
          <div className="analytics-table-wrap">
            <table>
              <thead>
                <tr>
                  {(tab === "users"
                    ? [
                        "User",
                        "Advanced",
                        "Created",
                        "Last sign-in",
                        "Last seen",
                        "Requests",
                        "Explore",
                      ]
                    : tab === "sessions"
                      ? [
                          "User",
                          "Client",
                          "Started",
                          "Last active",
                          "Status",
                          "Requests",
                          "Explore",
                        ]
                      : [
                          "Time",
                          "User",
                          "API",
                          "Service / view",
                          "Source",
                          "Status",
                          "Duration",
                        ]
                  ).map((h) => (
                    <th key={h}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => (
                  <tr key={String(r.id)}>
                    {tab === "users" ? (
                      <>
                        <td>
                          <b>{r.display_name}</b>
                          <small>{r.email}</small>
                        </td>
                        <td>
                          <span
                            className={
                              advanced(r.is_advanced)
                                ? "table-pill advanced"
                                : "table-pill"
                            }
                          >
                            {advanced(r.is_advanced) ? "Yes" : "No"}
                          </span>
                        </td>
                        <td>{date(r.created_at)}</td>
                        <td>{date(r.last_login_at)}</td>
                        <td>{date(r.last_seen_at)}</td>
                        <td>{count(r.requests)}</td>
                        <td>
                          <button
                            className="text-button"
                            onClick={() => chooseUser(r, "sessions")}
                          >
                            Sessions
                          </button>
                          <button
                            className="text-button"
                            onClick={() => chooseUser(r, "requests")}
                          >
                            Requests
                          </button>
                        </td>
                      </>
                    ) : tab === "sessions" ? (
                      <>
                        <td>
                          <b>{r.display_name}</b>
                          <small>{r.email}</small>
                        </td>
                        <td>
                          {r.browser} · {r.operating_system}
                          <small>{r.device_type}</small>
                        </td>
                        <td>{date(r.started_at)}</td>
                        <td>{date(r.last_active_at)}</td>
                        <td>
                          <span className="table-pill">
                            {r.ended_at
                              ? "Logged out"
                              : Date.parse(String(r.expires_at)) <= Date.now()
                                ? "Expired"
                                : Date.parse(String(r.last_active_at)) <
                                    Date.now() - 300000
                                  ? "Idle"
                                  : "Active"}
                          </span>
                        </td>
                        <td>{count(r.requests)}</td>
                        <td>
                          <button
                            className="text-button"
                            onClick={() => {
                              setUser(String(r.user_id));
                              setSession(String(r.id));
                              setLabel(String(r.email));
                              setTab("requests");
                              reset();
                            }}
                          >
                            Requests
                          </button>
                        </td>
                      </>
                    ) : (
                      <>
                        <td>{date(r.occurred_at)}</td>
                        <td>{r.email}</td>
                        <td>
                          <code>{r.route}</code>
                        </td>
                        <td>
                          {serviceName(String(r.service))}
                          <small>{r.app_view}</small>
                        </td>
                        <td>
                          {advanced(r.is_background)
                            ? "Background"
                            : "User-driven"}
                        </td>
                        <td>
                          <span
                            className={
                              number(r.status_code) >= 400
                                ? "table-pill failed"
                                : "table-pill"
                            }
                          >
                            {r.status_code}
                          </span>
                        </td>
                        <td>{count(r.duration_ms)} ms</td>
                      </>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
            {!rows.length && (
              <p className="analytics-empty">
                No records yet. Tracking begins with sign-in after this update.
              </p>
            )}
          </div>
        )}
        <div className="analytics-pagination">
          <span>{rows.length} records · 20 per page</span>
          <button
            disabled={!previous.length || list.isFetching}
            onClick={() => {
              setCursor(previous.at(-1) ?? null);
              setPrevious((p) => p.slice(0, -1));
            }}
          >
            Previous 20
          </button>
          <button
            disabled={!list.data?.has_more || list.isFetching}
            onClick={() => {
              setPrevious((p) => [...p, cursor]);
              setCursor(list.data!.next_cursor);
            }}
          >
            Next 20
          </button>
        </div>
      </section>
      <p className="analytics-note">
        API activity is buffered for up to a few seconds. Daily charts use UTC;
        session times use {timezone}. This measures app API usage and
        navigation, not every click or direct browser map-tile request.
      </p>
    </section>
  );
}
