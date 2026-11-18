import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import {
  AlertCircle,
  Check,
  ChevronDown,
  ChevronUp,
  Clock,
  Info,
  LocateFixed,
  MapPin,
  Plane,
  Radio,
  RefreshCw,
  WifiOff,
} from "lucide-react";
import { api, APIError, parameters, timeLabel } from "./api";
import { usePlaces } from "./usePlaces";
import { colour } from "./mapData";
import type { Device, Event, Filters, Page, Place } from "./types";
const severityLabels = {
  attention: "Attention",
  positive: "Report / recovery",
  limited: "Limited",
  neutral: "Context",
};
export function StatusIcon({ event }: { event: Event }) {
  const Icon = event.reason.startsWith("airplane")
    ? Plane
    : event.reason === "internet_off"
      ? WifiOff
      : event.reason === "location_off"
        ? MapPin
        : event.severity === "attention"
          ? AlertCircle
          : event.severity === "positive"
            ? Check
            : event.severity === "limited"
              ? Clock
              : Radio;
  return (
    <span className={"status-icon " + event.severity}>
      <Icon size={16} />
    </span>
  );
}
export function EventDetails({
  event: e,
  timezone,
  onMap,
}: {
  event: Event;
  timezone: string;
  onMap: (e: Event) => void;
}) {
  const client = useQueryClient();
  const [refreshState, setRefreshState] = useState("");
  const [watchPlace, setWatchPlace] = useState(false);
  const watched = usePlaces(watchPlace ? [{ ...e, place: undefined }] : []);
  const place = watched.data?.items[e.event_id]?.place ?? e.place;
  const refresh = async () => {
    setRefreshState("Refreshing area…");
    try {
      const result = await api<{
        items: Record<string, { status: string; place?: Place }>;
      }>("/api/places/resolve", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ event_ids: [e.event_id], refresh: true }),
      });
      const row = result.items[e.event_id];
      setWatchPlace(
        row.status === "pending" ||
          row.status === "busy" ||
          row.status === "ready",
      );
      setRefreshState(
        row.status === "not_configured"
          ? "Place lookup is not configured."
          : row.status === "ready"
            ? "Area label refreshed."
            : "Area refresh queued.",
      );
      await client.invalidateQueries({ queryKey: ["places"] });
    } catch {
      setRefreshState("Area lookup could not be refreshed. Please retry.");
    }
  };
  const fields = [
    ["Event recorded", timeLabel(e.recorded_at, timezone, true)],
    [
      "Planned periodic slot",
      e.scheduled_for
        ? timeLabel(e.scheduled_for, timezone, true)
        : "Not a periodic slot",
    ],
    ["Job executed", timeLabel(e.executed_at, timezone, true)],
    [
      "Capture outcome",
      e.execution_status?.replaceAll("_", " ") ?? "Not recorded",
    ],
    ["Position measured", timeLabel(e.captured_at, timezone, true)],
    [
      "Position age",
      e.fix_age_ms === null
        ? "Not recorded"
        : `${e.fix_age_ms.toLocaleString()} ms`,
    ],
    [
      "Reported accuracy",
      e.accuracy_m === null
        ? "No position"
        : `±${e.accuracy_m.toLocaleString()} m`,
    ],
    [
      "Permission / requested mode",
      `${e.permission_accuracy} / ${e.accuracy_mode.replaceAll("_", " ")}`,
    ],
    [
      "Network at event",
      e.network_transport === "none" ? "Offline" : e.network_transport,
    ],
    [
      "Battery",
      e.battery_percent === null ? "Not recorded" : `${e.battery_percent}%`,
    ],
    [
      "Airplane mode observed",
      e.airplane_mode_enabled === null
        ? "Unknown"
        : e.airplane_mode_enabled
          ? "Enabled"
          : "Disabled",
    ],
    ["Stored in database", timeLabel(e.received_at, timezone, true)],
  ];
  return (
    <div className="event-detail">
      {place && (
        <p className="detail-area">
          {place.label}
          <small>
            {place.provider === "Geoapify" ? (
              <>
                <a
                  href="https://www.geoapify.com/"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  Geoapify
                </a>{" "}
                ·{" "}
                <a
                  href="https://www.openstreetmap.org/copyright"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  OpenStreetMap contributors
                </a>
              </>
            ) : (
              place.attribution
            )}{" "}
            · approximate {place.level} label
          </small>
        </p>
      )}
      <dl>
        {fields.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
      <div className="event-identifiers">
        <span>
          Event ID <code>{e.event_id}</code>
        </span>
        {e.latitude !== null && e.longitude !== null ? (
          <>
            <span>
              Coordinates{" "}
              <code>
                {e.latitude.toFixed(6)}, {e.longitude.toFixed(6)}
              </code>
            </span>
            <button onClick={() => onMap(e)}>
              <LocateFixed size={14} />
              View observation on map
            </button>
            <button onClick={refresh}>Refresh area label</button>
            {refreshState && <span role="status">{refreshState}</span>}
          </>
        ) : (
          <span>
            {e.location_status === "disabled"
              ? "Location was disabled for this event."
              : "No position was recorded for this event."}
          </span>
        )}
      </div>
      {e.is_mock && (
        <p className="inline-warning">The phone reported a mock location.</p>
      )}
      <p className="detail-note">
        Times shown in {timezone}. Phone and server clocks can differ.
      </p>
    </div>
  );
}
type PageParam = { cursor: string | null; index: number };
export function EventFeed({
  filters,
  devices,
  generation,
  onMap,
  onRestart,
}: {
  filters: Filters;
  devices: Device[];
  generation: number;
  onMap: (e: Event) => void;
  onRestart: () => void;
}) {
  const scroll = useRef<HTMLDivElement>(null),
    pageCursors = useRef(new Map<number, string | null>([[0, null]])),
    anchor = useRef<{ id: string; offset: number } | null>(null);
  const [expanded, setExpanded] = useState<Set<string>>(new Set()),
    [simple, setSimple] = useState(false);
  const params = parameters(filters).toString();
  const query = useInfiniteQuery<
    Page,
    APIError,
    { pages: Page[]; pageParams: PageParam[] },
    readonly unknown[],
    PageParam
  >({
    queryKey: ["events", params, generation],
    initialPageParam: { cursor: null, index: 0 },
    queryFn: async ({ pageParam, signal }) => {
      const q = new URLSearchParams(params);
      q.set("limit", "20");
      if (pageParam.cursor) q.set("cursor", pageParam.cursor);
      const data = await api<Page>("/api/events?" + q, { signal });
      if (data.page_cursor)
        pageCursors.current.set(pageParam.index, data.page_cursor);
      if (data.next_cursor)
        pageCursors.current.set(pageParam.index + 1, data.next_cursor);
      return data;
    },
    getNextPageParam: (last, _pages, lastParam) =>
      last.next_cursor
        ? { cursor: last.next_cursor, index: lastParam.index + 1 }
        : undefined,
    getPreviousPageParam: (_first, _pages, firstParam) =>
      firstParam.index > 0 && pageCursors.current.has(firstParam.index - 1)
        ? {
            cursor: pageCursors.current.get(firstParam.index - 1)!,
            index: firstParam.index - 1,
          }
        : undefined,
    maxPages: 10,
    staleTime: Infinity,
    gcTime: 0,
    retry: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  const events = useMemo(
    () => [
      ...new Map(
        (query.data?.pages.flatMap((p) => p.items) ?? []).map((e) => [
          e.event_id,
          e,
        ]),
      ).values(),
    ],
    [query.data],
  );
  const asOf = query.data?.pages[0]?.as_of;
  const changes = useQuery({
    queryKey: ["changes", filters.devices, asOf],
    enabled: !!asOf && filters.devices.length > 0,
    queryFn: ({ signal }) =>
      api<{ available: boolean }>(
        "/api/events/changes?" +
          new URLSearchParams({
            device_ids: filters.devices.join(","),
            as_of: asOf!,
          }),
        { signal },
      ),
    refetchInterval: 30000,
    refetchOnWindowFocus: false,
  });
  const virtual = useVirtualizer({
    count: events.length,
    getScrollElement: () => scroll.current,
    estimateSize: (i) => (expanded.has(events[i]?.event_id) ? 460 : 100),
    overscan: 5,
    getItemKey: (i) => events[i]?.event_id ?? i,
    enabled: !simple,
  });
  const visibleEvents = simple
    ? events
    : virtual
        .getVirtualItems()
        .map((v) => events[v.index])
        .filter(Boolean);
  const placeQuery = usePlaces(visibleEvents);
  const places = placeQuery.data?.items;
  const rememberAnchor = () => {
    const first = virtual
      .getVirtualItems()
      .find((v) => v.end > (scroll.current?.scrollTop ?? 0));
    if (first)
      anchor.current = {
        id: events[first.index].event_id,
        offset: (scroll.current?.scrollTop ?? 0) - first.start,
      };
  };
  const next = () => {
    if (query.hasNextPage && !query.isFetching && !query.isError) {
      rememberAnchor();
      void query.fetchNextPage();
    }
  };
  const previous = () => {
    if (!query.hasPreviousPage || query.isFetching || query.isError) return;
    const first = virtual
      .getVirtualItems()
      .find((v) => v.end > (scroll.current?.scrollTop ?? 0));
    if (first)
      anchor.current = {
        id: events[first.index].event_id,
        offset: (scroll.current?.scrollTop ?? 0) - first.start,
      };
    void query.fetchPreviousPage();
  };
  useLayoutEffect(() => {
    if (anchor.current) {
      const i = events.findIndex((e) => e.event_id === anchor.current!.id);
      if (i >= 0) {
        virtual.scrollToIndex(i, { align: "start" });
        if (scroll.current) scroll.current.scrollTop += anchor.current.offset;
      }
      anchor.current = null;
    }
  }, [events]);
  const bottom = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!bottom.current || !scroll.current) return;
    const observer = new IntersectionObserver(
      ([e]) => {
        if (
          e.isIntersecting &&
          query.hasNextPage &&
          !query.isFetching &&
          !query.isError
        )
          next();
      },
      { root: scroll.current, rootMargin: "180px" },
    );
    observer.observe(bottom.current);
    return () => observer.disconnect();
  }, [query.hasNextPage, query.isFetching, query.isError, query.fetchNextPage]);
  const toggle = (id: string) =>
    setExpanded((v) => {
      const next = new Set(v);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  const renderRow = (e: Event) => {
    const d = devices.find((d) => d.device_id === e.device_id),
      place = e.place ?? places?.[e.event_id]?.place;
    const coordinates = e.latitude !== null && e.longitude !== null;
    const area =
      place?.label ??
      (coordinates
        ? `${e.latitude!.toFixed(5)}, ${e.longitude!.toFixed(5)}`
        : e.location_status === "disabled"
          ? "Location was off"
          : "No position available");
    return (
      <article
        className={"event-row " + (expanded.has(e.event_id) ? "expanded" : "")}
        data-event-id={e.event_id}
        key={e.event_id}
      >
        <button
          className="event-main"
          aria-expanded={expanded.has(e.event_id)}
          onClick={() => toggle(e.event_id)}
        >
          <StatusIcon event={e} />
          <div className="event-body">
            <div className="event-topline">
              <b>{e.title}</b>
              <span className={"event-chip " + e.severity}>
                {severityLabels[e.severity]}
              </span>
            </div>
            <span className="event-area">{area}</span>
            <span className="event-meta">
              <i style={{ background: colour(e.device_id, devices) }} />
              {d?.device_name ?? "Device"}
              <span>·</span>
              {e.execution_status?.replaceAll("_", " ") ??
                "Outcome not recorded"}
              {coordinates && (
                <>
                  <span>·</span>±{e.accuracy_m} m
                </>
              )}
            </span>
          </div>
          <span className="event-date">
            {timeLabel(e.recorded_at, filters.timezone)}
            <small>
              {new Intl.DateTimeFormat("en-GB", {
                timeZone: filters.timezone,
                day: "numeric",
                month: "short",
              }).format(new Date(e.recorded_at))}
            </small>
            {expanded.has(e.event_id) ? (
              <ChevronUp size={13} />
            ) : (
              <ChevronDown size={13} />
            )}
          </span>
        </button>
        {expanded.has(e.event_id) && (
          <EventDetails
            event={{ ...e, place }}
            timezone={filters.timezone}
            onMap={onMap}
          />
        )}
      </article>
    );
  };
  return (
    <section className="feed-panel">
      <header className="feed-heading">
        <div>
          <span className="eyebrow">THE COMPLETE EVENT TRAIL</span>
          <h2>
            Activity logs <span>{events.length} loaded</span>
          </h2>
        </div>
        <label className="simple-list">
          <input
            type="checkbox"
            checked={simple}
            onChange={(e) => setSimple(e.target.checked)}
          />
          Simple list
        </label>
      </header>
      <div className="feed-subheading">
        <span>Newest event first · 20 records per page</span>
        <button className="text-button" onClick={onRestart}>
          <RefreshCw size={12} />
          Refresh
        </button>
      </div>
      {changes.data?.available && (
        <button className="new-events" onClick={onRestart}>
          New device events available · Refresh from the first 20
        </button>
      )}
      {query.isError && (
        <div role="alert" className="inline-error">
          <AlertCircle size={18} />
          <div>
            {query.error.message}
            <button
              onClick={() =>
                query.error.code.startsWith("cursor_")
                  ? onRestart()
                  : query.isFetchNextPageError
                    ? query.fetchNextPage()
                    : query.isFetchPreviousPageError
                      ? query.fetchPreviousPage()
                      : query.refetch()
              }
            >
              {query.error.code.startsWith("cursor_")
                ? "Restart first 20"
                : "Retry"}
            </button>
          </div>
        </div>
      )}
      <div
        className="feed-scroll"
        ref={scroll}
        aria-label="Activity event list"
        onScroll={() => {
          if ((scroll.current?.scrollTop ?? 999) < 60 && !simple) previous();
        }}
      >
        {query.hasPreviousPage && (
          <button
            className="load-more"
            onClick={previous}
            disabled={query.isFetching}
          >
            Load previous 20
          </button>
        )}
        {query.isPending ? (
          <div className="skeletons" aria-label="Loading first 20 events">
            {Array.from({ length: 6 }, (_, i) => (
              <div className="skeleton-row" key={i} />
            ))}
          </div>
        ) : !events.length && !query.isError ? (
          <div className="empty-state">
            <Info />
            <h3>No matching events</h3>
            <p>Choose devices or adjust the date and event filters.</p>
          </div>
        ) : simple ? (
          <div>{events.map(renderRow)}</div>
        ) : (
          <div
            style={{
              height: virtual.getTotalSize(),
              position: "relative",
              width: "100%",
            }}
          >
            {virtual.getVirtualItems().map((v) => (
              <div
                key={v.key}
                data-index={v.index}
                ref={virtual.measureElement}
                style={{
                  position: "absolute",
                  top: 0,
                  left: 0,
                  width: "100%",
                  transform: `translateY(${v.start}px)`,
                }}
              >
                {renderRow(events[v.index])}
              </div>
            ))}
          </div>
        )}
        <div ref={bottom} className="feed-end">
          {query.hasNextPage ? (
            <button
              className="load-more"
              disabled={query.isFetching || query.isError}
              onClick={next}
            >
              {query.isFetchingNextPage ? "Loading next 20…" : "Load 20 more"}
            </button>
          ) : events.length > 0 && !query.isError ? (
            <span>All matching events loaded for this browsing session.</span>
          ) : null}
        </div>
      </div>
      <footer className="feed-footer" aria-live="polite">
        {query.isFetchingNextPage
          ? "Loading the next page."
          : `${events.length} records loaded.`}{" "}
        {asOf &&
          `Records ${(query.data?.pageParams[0]?.index ?? 0) * 20 + 1}–${(query.data?.pageParams[0]?.index ?? 0) * 20 + events.length} · Snapshot ${timeLabel(asOf, filters.timezone)} · ${filters.timezone}`}
      </footer>
    </section>
  );
}
