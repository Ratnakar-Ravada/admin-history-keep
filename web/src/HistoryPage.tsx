import { useId, useRef, useState, type FormEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Bell,
  BellOff,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Clock,
  FileText,
  History,
  Info,
  Layers,
  Phone,
  PhoneIncoming,
  PhoneMissed,
  PhoneOutgoing,
  RefreshCw,
  Search,
  Smartphone,
} from "lucide-react";
import { api, APIError, parameters, timeLabel } from "./api";
import type { Device, Filters } from "./types";
import {
  callNumber,
  callTypes,
  duration,
  notificationMessages,
  type HistoryKind,
  type HistoryPageData,
  type HistoryRecord,
} from "./history";
import "./history.css";

type Props = {
  kind: HistoryKind;
  filters: Filters;
  devices: Device[];
  generation: number;
};
const config = {
  calls: {
    heading: "Call records",
    icon: Phone,
    search: "Search name or number",
    note: "Each call shows its latest saved revision. Expand a record for the available call details.",
    empty: "No calls in this selection",
  },
  notifications: {
    heading: "Notification records",
    icon: Bell,
    search: "Search app, title or text",
    note: "Posted, active when collected, and removed are separate observations. Android may hide or omit notification content.",
    empty: "No notifications in this selection",
  },
  "app-usage": {
    heading: "App usage records",
    icon: Smartphone,
    search: "Search app or package",
    note: "Daily totals use the latest saved snapshot per Android interval. Sessions are estimated. Foreground, visible and service times overlap and should not be added together.",
    empty: "No app usage in this selection",
  },
};
const label = (key: string) =>
  key
    .replace(/^_/, "")
    .replaceAll("_", " ")
    .replace(/^./, (s) => s.toUpperCase());
function stamp(
  value: string | null | undefined,
  timezone: string,
  full = true,
) {
  return value && !Number.isNaN(Date.parse(value))
    ? timeLabel(value, timezone, full)
    : "Not recorded";
}
function fieldValue(key: string, value: string | null, timezone: string) {
  if (value == null || value === "") return "Not recorded";
  if (key.endsWith("_at") || key === "interval_start")
    return stamp(value, timezone);
  if (
    [
      "duration_ms",
      "foreground_ms",
      "visible_ms",
      "foreground_service_ms",
    ].includes(key)
  )
    return duration(value);
  if (key === "duration") return duration(value, false);
  if (key === "type") return callTypes[value] ?? `Type ${value}`;
  if (value === "true") return "Yes";
  if (value === "false") return "No";
  return value;
}
const fieldLabels: Record<string, string> = {
  app_label: "App name",
  duration: "Call duration",
  type: "Call type",
  number: "Phone number",
  presentation: "Number presentation code",
  name: "Saved caller name",
  geocoded_location: "Number location hint",
  countryiso: "Country code",
  numbertype: "Number type code",
  subscription_id: "Phone account / SIM ID",
  subscription_component_name: "Phone account component",
  features: "Call feature flags",
  is_read: "Read flag",
  new: "New flag",
  foreground_ms: "Foreground time",
  visible_ms: "Visible time",
  foreground_service_ms: "Foreground service time",
  duration_ms: "Session duration",
  start_at: "Session started",
  end_at: "Session ended",
  interval_start: "Android interval started",
  posted_at: "Notification posted",
  observed_at: "Observed on phone",
  removal_reason: "Android removal reason code",
  _truncated_fields: "Shortened fields",
  _backup_truncated_fields: "Shortened fields",
};

function RecordDetails({
  record: row,
  kind,
  timezone,
}: {
  record: HistoryRecord;
  kind: HistoryKind;
  timezone: string;
}) {
  const messages = notificationMessages(row.fields.messages);
  const content =
    kind === "notifications"
      ? [
          ...new Set(
            [
              row.fields.text,
              row.fields.big_text,
              row.fields.text_lines,
              row.fields.sub_text,
            ].filter((v): v is string => !!v),
          ),
        ]
      : [];
  return (
    <div className="history-details">
      {(row.fields._truncated_fields ||
        row.fields._backup_truncated_fields) && (
        <p className="history-clock-note">
          Some fields were shortened by the phone before upload. The saved
          metadata identifies them.
        </p>
      )}
      {kind === "notifications" &&
        (content.length > 0 || messages.length > 0) && (
          <div className="history-content">
            <h4>Available notification content</h4>
            {content.map((text, index) => (
              <p key={index}>{text}</p>
            ))}
            {messages.length > 0 && (
              <ol className="history-messages">
                {messages.map((message, index) => (
                  <li key={index}>
                    <div>
                      <strong>{message.sender || "Sender not provided"}</strong>
                      {message.time != null && (
                        <time dateTime={new Date(message.time).toISOString()}>
                          {stamp(
                            new Date(message.time).toISOString(),
                            timezone,
                          )}
                        </time>
                      )}
                    </div>
                    <p>{message.text || "Message text not available"}</p>
                  </li>
                ))}
              </ol>
            )}
          </div>
        )}
      <dl className="history-detail-grid">
        <div>
          <dt>
            {kind === "calls"
              ? "Call started"
              : kind === "app-usage" && row.event_type === "daily"
                ? "Android interval started"
                : "Event recorded"}
          </dt>
          <dd>{stamp(row.recorded_at, timezone)}</dd>
        </div>
        <div>
          <dt>Observed on phone</dt>
          <dd>{stamp(row.observed_at, timezone)}</dd>
        </div>
        <div>
          <dt>Stored in database</dt>
          <dd>{stamp(row.received_at, timezone)}</dd>
        </div>
        {row.source_package && (
          <div>
            <dt>App package</dt>
            <dd>{row.source_package}</dd>
          </div>
        )}
      </dl>
      <details className="history-metadata">
        <summary>
          <FileText size={14} /> All saved metadata <ChevronDown size={14} />
        </summary>
        <dl className="history-detail-grid">
          {Object.entries(row.fields)
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([key, value]) => (
              <div key={key}>
                <dt>{fieldLabels[key] || label(key)}</dt>
                <dd>{fieldValue(key, value, timezone)}</dd>
              </div>
            ))}
        </dl>
        <dl className="history-identifiers">
          {Object.entries({
            "Event ID": row.event_id,
            "Device ID": row.device_id,
            "Call ID": row.call_id,
            "Source ID": row.source_id,
            "Source key": row.source_key,
          })
            .filter(([, value]) => value)
            .map(([key, value]) => (
              <div key={key}>
                <dt>{key}</dt>
                <dd>
                  <code>{value}</code>
                </dd>
              </div>
            ))}
        </dl>
      </details>
      <p className="history-clock-note">
        Times shown in {timezone}. Phone and server clocks can differ.
      </p>
    </div>
  );
}

function RecordCard({
  record: row,
  kind,
  timezone,
  devices,
}: {
  record: HistoryRecord;
  kind: HistoryKind;
  timezone: string;
  devices: Device[];
}) {
  const f = row.fields;
  const device = devices.find((d) => d.device_id === row.device_id);
  const app = f.app_label || row.source_package || "App name not recorded";
  const callType = f.type || row.event_type;
  const Icon =
    kind === "calls"
      ? callType === "1"
        ? PhoneIncoming
        : callType === "2"
          ? PhoneOutgoing
          : ["3", "5", "6"].includes(callType)
            ? PhoneMissed
            : Phone
      : kind === "notifications"
        ? row.event_type === "removed"
          ? BellOff
          : Bell
        : Smartphone;
  const title =
    kind === "calls"
      ? f.name || f.asserted_display_name || callNumber(f)
      : kind === "notifications"
        ? f.title || app
        : app;
  const subtitle =
    kind === "calls"
      ? f.name || f.asserted_display_name
        ? callNumber(f)
        : f.geocoded_location || "Call history"
      : kind === "notifications"
        ? app
        : row.source_package || "Package not recorded";
  const badge =
    kind === "calls"
      ? callTypes[callType] || `Type ${callType}`
      : kind === "notifications"
        ? row.event_type === "active"
          ? "Active when collected"
          : label(row.event_type)
        : row.event_type === "daily"
          ? "Daily total"
          : `Estimated ${f.metric || "usage"}`;
  const attention = kind === "calls" && ["3", "5", "6"].includes(callType);
  return (
    <article
      className={
        "history-record history-row " + (attention ? "history-attention" : "")
      }
    >
      <details>
        <summary
          className="history-record-summary"
          aria-label={`Details for ${title}`}
        >
          <span className="history-record-icon">
            <Icon size={19} />
          </span>
          <span className="history-record-main">
            <strong>{title}</strong>
            <span className="history-record-subtitle">{subtitle}</span>
            {kind === "notifications" && (
              <span
                className={
                  "history-notification-preview " +
                  (!f.text && !f.big_text ? "history-no-content" : "")
                }
              >
                {f.text || f.big_text || "No notification text available"}
              </span>
            )}
            <span className="history-record-context">
              <span>
                {device?.device_name || device?.label || row.device_id}
              </span>
              <span>·</span>
              <time dateTime={row.recorded_at}>
                {stamp(row.recorded_at, timezone)}
              </time>
            </span>
          </span>
          <span className="history-record-values">
            <span className="history-type-badge">{badge}</span>
            {kind === "calls" && (
              <span className="history-duration">
                <Clock size={12} />
                {duration(f.duration, false)}
              </span>
            )}
            {kind === "app-usage" &&
              (row.event_type === "daily" ? (
                <span className="history-usage-metrics">
                  <span>
                    <b>{duration(f.foreground_ms)}</b>
                    <small>foreground</small>
                  </span>
                  <span>
                    <b>{duration(f.visible_ms)}</b>
                    <small>visible</small>
                  </span>
                  <span>
                    <b>{duration(f.foreground_service_ms)}</b>
                    <small>service</small>
                  </span>
                </span>
              ) : (
                <span className="history-duration">
                  <Clock size={12} />
                  {duration(f.duration_ms)}
                </span>
              ))}
          </span>
          <ChevronDown className="history-record-chevron" size={17} />
        </summary>
        <RecordDetails record={row} kind={kind} timezone={timezone} />
      </details>
    </article>
  );
}

export function HistoryPage(props: Props) {
  const scope = parameters(props.filters, false).toString();
  return <HistoryBrowser key={props.kind} {...props} scope={scope} />;
}
function HistoryBrowser({
  kind,
  filters,
  devices,
  generation,
  scope,
}: Props & { scope: string }) {
  const info = config[kind];
  const [type, setType] = useState(kind === "app-usage" ? "daily" : "");
  const [draft, setDraft] = useState("");
  const [search, setSearch] = useState("");
  const pageScope = `${scope}:${generation}`;
  const [pagination, setPagination] = useState<{
    scope: string;
    cursor: string | null;
    previous: (string | null)[];
  }>({ scope: pageScope, cursor: null, previous: [] });
  const cursor = pagination.scope === pageScope ? pagination.cursor : null;
  const previous = pagination.scope === pageScope ? pagination.previous : [];
  const heading = useRef<HTMLDivElement>(null);
  const [revision, setRevision] = useState(0);
  const headingID = useId();
  const params = new URLSearchParams(scope);
  if (type) params.set("event_type", type);
  if (search) params.set("q", search);
  if (cursor) params.set("cursor", cursor);
  const query = useQuery({
    queryKey: ["phone-history", kind, params.toString(), generation, revision],
    queryFn: ({ signal }) =>
      api<HistoryPageData>(`/api/history/${kind}?${params}`, { signal }),
    enabled: filters.devices.length > 0,
    refetchOnWindowFocus: false,
    staleTime: 30000,
    retry: false,
  });
  const reset = () =>
    setPagination({ scope: pageScope, cursor: null, previous: [] });
  const scrollToHeading = () =>
    heading.current?.scrollIntoView({ block: "start" });
  const refresh = () => {
    reset();
    setRevision((v) => v + 1);
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    reset();
    setSearch(draft.trim());
  };
  const rows = query.data?.items ?? [];
  const Icon = info.icon;
  return (
    <section className="phone-history history-page" aria-labelledby={headingID}>
      <div className="history-heading" ref={heading}>
        <div>
          <span className="eyebrow">ON THE DEVICE</span>
          <h2 id={headingID}>
            <Icon size={20} />
            {info.heading}
          </h2>
        </div>
        <button
          className="history-refresh"
          onClick={refresh}
          disabled={query.isFetching || !filters.devices.length}
        >
          <RefreshCw size={14} />
          Refresh
        </button>
      </div>
      <div className="history-explainer">
        <Info size={15} />
        <p>{info.note}</p>
      </div>
      <div className="history-toolbar">
        {kind === "app-usage" ? (
          <div
            className="history-segments"
            role="group"
            aria-label="Usage record type"
          >
            {[
              { id: "daily", name: "Daily totals", icon: Layers },
              { id: "session", name: "Estimated sessions", icon: Clock },
            ].map((tab) => (
              <button
                key={tab.id}
                aria-pressed={type === tab.id}
                onClick={() => {
                  setType(tab.id);
                  reset();
                }}
              >
                <tab.icon size={14} />
                {tab.name}
              </button>
            ))}
          </div>
        ) : (
          <label className="history-type-control">
            {kind === "calls" ? "Call type" : "Event type"}
            <select
              aria-label={
                kind === "calls" ? "Call type" : "Notification event type"
              }
              value={type}
              onChange={(event) => {
                setType(event.target.value);
                reset();
              }}
            >
              <option value="">
                {kind === "calls" ? "All calls" : "All events"}
              </option>
              {Object.entries(
                kind === "calls"
                  ? callTypes
                  : {
                      posted: "Posted",
                      active: "Active when collected",
                      removed: "Removed",
                    },
              ).map(([key, value]) => (
                <option value={key} key={key}>
                  {value}
                </option>
              ))}
            </select>
          </label>
        )}
        <form className="history-search" role="search" onSubmit={submit}>
          <Search size={15} />
          <input
            type="search"
            aria-label={info.search}
            placeholder={info.search}
            value={draft}
            maxLength={200}
            onChange={(event) => setDraft(event.target.value)}
          />
          <button type="submit">Search</button>
        </form>
      </div>
      <div className="history-results" aria-live="polite">
        <span>
          {filters.devices.length && !query.isPending && !query.error
            ? `${rows.length} record${rows.length === 1 ? "" : "s"} on this page`
            : "Saved device history"}
          {search && <> · Matching “{search}”</>}
        </span>
        <span>{filters.timezone}</span>
      </div>
      {query.error ? (
        <div className="history-state history-error" role="alert">
          <Info size={26} />
          <h3>History could not be loaded</h3>
          <p>
            {query.error instanceof Error
              ? query.error.message
              : "Please try again."}
          </p>
          <button
            onClick={
              query.error instanceof APIError &&
              query.error.code.startsWith("cursor_")
                ? refresh
                : () => void query.refetch()
            }
          >
            {query.error instanceof APIError &&
            query.error.code.startsWith("cursor_")
              ? "Restart list"
              : "Retry"}
          </button>
        </div>
      ) : !filters.devices.length ? (
        <div className="history-state">
          <Smartphone size={30} />
          <h3>Choose a device to see its history</h3>
          <p>Use the device selector above.</p>
        </div>
      ) : query.isPending ? (
        <div className="history-state" role="status">
          <RefreshCw size={25} className="history-loading-icon" />
          <h3>Loading saved history…</h3>
        </div>
      ) : !rows.length ? (
        <div className="history-state">
          <Icon size={30} />
          <h3>{info.empty}</h3>
          <p>
            Try another date range, device or search. Records appear after the
            phone collects and uploads them.
          </p>
          {(search || (type && kind !== "app-usage")) && (
            <button
              onClick={() => {
                setSearch("");
                setDraft("");
                setType(kind === "app-usage" ? "daily" : "");
                reset();
              }}
            >
              Clear history filters
            </button>
          )}
        </div>
      ) : (
        <div className="history-record-list" aria-busy={query.isFetching}>
          {rows.map((record) => (
            <RecordCard
              key={record.event_id}
              record={record}
              kind={kind}
              timezone={filters.timezone}
              devices={devices}
            />
          ))}
        </div>
      )}
      {rows.length > 0 && !query.error && (
        <div className="history-pagination">
          <span>
            <History size={13} />
            Page {previous.length + 1}
          </span>
          <div>
            <button
              disabled={!previous.length || query.isFetching}
              onClick={() => {
                setPagination({
                  scope: pageScope,
                  cursor: previous.at(-1) ?? null,
                  previous: previous.slice(0, -1),
                });
                scrollToHeading();
              }}
            >
              <ChevronLeft size={15} />
              Previous
            </button>
            <button
              disabled={
                !query.data?.has_more ||
                !query.data.next_cursor ||
                query.isFetching
              }
              onClick={() => {
                setPagination({
                  scope: pageScope,
                  cursor: query.data!.next_cursor,
                  previous: [...previous, query.data?.page_cursor ?? cursor],
                });
                scrollToHeading();
              }}
            >
              Next
              <ChevronRight size={15} />
            </button>
          </div>
        </div>
      )}
      {kind === "app-usage" && (
        <p className="history-footnote">
          Android controls interval boundaries and available history. Daily
          totals can be partial and sessions from different apps can overlap.
        </p>
      )}
    </section>
  );
}
