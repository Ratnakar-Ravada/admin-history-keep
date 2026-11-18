import { useEffect, useRef, useState } from "react";
import * as maplibregl from "maplibre-gl";
import type { Map as GLMap, GeoJSONSource } from "maplibre-gl";
import workerUrl from "maplibre-gl/dist/maplibre-gl-worker.mjs?worker&url";
import { Crosshair, Globe, Layers, MapPin } from "lucide-react";
import type { Device, Event } from "./types";
import { mapFeatures, colour } from "./mapData";

maplibregl.setWorkerUrl(workerUrl);

export function MapView({
  events,
  devices,
  registry = devices,
  style,
  onSelect,
  lines = true,
  focused,
  breaks = [],
  live = false,
}: {
  events: Event[];
  devices: Device[];
  registry?: Device[];
  style: string;
  onSelect: (e: Event) => void;
  lines?: boolean;
  focused?: Event | null;
  breaks?: { device_id: string; recorded_at: string }[];
  live?: boolean;
}) {
  const container = useRef<HTMLDivElement>(null),
    map = useRef<GLMap | null>(null),
    eventsRef = useRef(events),
    selectRef = useRef(onSelect);
  const [loaded, setLoaded] = useState(false),
    [failed, setFailed] = useState(false),
    [accuracy, setAccuracy] = useState(false),
    [tileError, setTileError] = useState(false);
  const refitted = useRef("");
  eventsRef.current = events;
  selectRef.current = onSelect;
  const fit = () => {
    const points = eventsRef.current.filter(
      (e) => e.latitude !== null && e.longitude !== null,
    );
    if (!points.length || !map.current) return;
    const bounds = new maplibregl.LngLatBounds();
    for (const e of points) bounds.extend([e.longitude!, e.latitude!]);
    map.current.fitBounds(bounds, { padding: 65, maxZoom: 14, duration: 0 });
  };
  useEffect(() => {
    if (!container.current) return;
    let gl: GLMap;
    try {
      gl = new maplibregl.Map({
        container: container.current,
        style,
        center: [-0.12, 51.51],
        zoom: 11,
        attributionControl: { compact: true },
        renderWorldCopies: true,
      });
      map.current = gl;
      gl.addControl(
        new maplibregl.NavigationControl({ showCompass: false }),
        "top-right",
      );
      gl.on("load", () => {
        gl.addSource("observations", {
          type: "geojson",
          data: { type: "FeatureCollection", features: [] },
        });
        gl.addSource("point-clusters", {
          type: "geojson",
          data: { type: "FeatureCollection", features: [] },
          cluster: true,
          clusterMaxZoom: 11,
          clusterRadius: 45,
        });
        gl.addLayer({
          id: "accuracy-fill",
          type: "fill",
          source: "observations",
          filter: ["==", ["geometry-type"], "Polygon"],
          paint: { "fill-color": ["get", "color"], "fill-opacity": 0.08 },
        });
        gl.addLayer({
          id: "links",
          type: "line",
          source: "observations",
          filter: ["==", ["geometry-type"], "LineString"],
          paint: {
            "line-color": ["get", "color"],
            "line-width": 2.5,
            "line-dasharray": [2, 3],
            "line-opacity": 0.75,
          },
        });
        gl.addLayer({
          id: "clusters",
          type: "circle",
          source: "point-clusters",
          filter: ["has", "point_count"],
          paint: {
            "circle-color": "#517c55",
            "circle-radius": [
              "step",
              ["get", "point_count"],
              16,
              25,
              21,
              100,
              27,
            ],
            "circle-stroke-width": 2,
            "circle-stroke-color": "#ffffff",
          },
        });
        gl.addLayer({
          id: "cluster-labels",
          type: "symbol",
          source: "point-clusters",
          filter: ["has", "point_count"],
          layout: {
            "text-field": ["to-string", ["get", "point_count_abbreviated"]],
            "text-font": ["Noto Sans Regular"],
            "text-size": 11,
          },
          paint: { "text-color": "#ffffff" },
        });
        gl.addLayer({
          id: "points",
          type: "circle",
          source: "point-clusters",
          filter: ["!", ["has", "point_count"]],
          paint: {
            "circle-radius": ["case", ["get", "latest"], 9, 4.5],
            "circle-color": ["get", "color"],
            "circle-stroke-color": "#fff",
            "circle-stroke-width": 2,
          },
        });
        gl.on("click", "points", (e) => {
          const id = e.features?.[0]?.properties?.event_id;
          const event = eventsRef.current.find((x) => x.event_id === id);
          if (event) selectRef.current(event);
        });
        gl.on("click", "clusters", async (e) => {
          const f = e.features?.[0];
          if (f?.geometry.type !== "Point") return;
          try {
            const zoom = await (
              gl.getSource("point-clusters") as GeoJSONSource
            ).getClusterExpansionZoom(f.properties.cluster_id);
            if (map.current === gl)
              gl.easeTo({
                center: f.geometry.coordinates as [number, number],
                zoom,
              });
          } catch {
            /* An updated/removed source needs no recovery. */
          }
        });
        for (const name of ["points", "clusters"]) {
          gl.on("mouseenter", name, () => {
            gl.getCanvas().style.cursor = "pointer";
          });
          gl.on("mouseleave", name, () => {
            gl.getCanvas().style.cursor = "";
          });
        }
        setLoaded(true);
        fit();
      });
      gl.on("error", () => setTileError(true));
      const observer = new ResizeObserver(() => gl.resize());
      observer.observe(container.current);
      return () => {
        observer.disconnect();
        gl.remove();
        map.current = null;
        setLoaded(false);
      };
    } catch {
      setFailed(true);
    }
  }, [style]);
  useEffect(() => {
    if (!loaded || !map.current) return;
    const data =
      focused && !events.some((e) => e.event_id === focused.event_id)
        ? [...events, focused]
        : events;
    const features = mapFeatures(data, registry, lines, accuracy, breaks);
    (map.current.getSource("observations") as GeoJSONSource)?.setData(features);
    (map.current.getSource("point-clusters") as GeoJSONSource)?.setData({
      type: "FeatureCollection",
      features: features.features.filter((f) => f.geometry.type === "Point"),
    });
    const key =
      devices.map((d) => d.device_id).join() +
      events.length +
      events[0]?.event_id +
      events.at(-1)?.event_id;
    if (refitted.current !== key) {
      refitted.current = key;
      fit();
    }
  }, [events, devices, registry, loaded, accuracy, lines, breaks, focused]);
  useEffect(() => {
    if (
      focused?.longitude !== null &&
      focused?.latitude !== null &&
      focused &&
      map.current
    )
      map.current.easeTo({
        center: [focused.longitude!, focused.latitude!],
        zoom: 14,
        duration: 0,
      });
  }, [focused]);
  return (
    <section
      className="map-shell"
      aria-label="Location map"
      aria-busy={!loaded && !failed}
    >
      <div className="map-container" ref={container} />
      <div className="map-caption">
        <MapPin size={17} />
        <div>
          <b>{live ? "Latest reported locations" : "Observed locations"}</b>
          <span>{events.length.toLocaleString()} displayed observations</span>
        </div>
      </div>
      <div className="map-actions">
        <button
          title="Fit selected observations"
          aria-label="Fit map"
          onClick={fit}
        >
          <Crosshair size={18} />
        </button>
        <button
          title="World view"
          aria-label="World view"
          onClick={() => map.current?.jumpTo({ center: [15, 15], zoom: 1.5 })}
        >
          <Globe size={18} />
        </button>
      </div>
      <button
        className={"accuracy-toggle " + (accuracy ? "is-active" : "")}
        aria-pressed={accuracy}
        onClick={() => setAccuracy(!accuracy)}
      >
        <Layers size={14} /> Accuracy circles
      </button>
      <div className="map-legend">
        {devices.map((d) => (
          <span key={d.device_id}>
            <i style={{ background: colour(d.device_id, registry) }} />
            {d.device_name}
          </span>
        ))}
      </div>
      {(!events.length || failed) && (
        <div className="map-message">
          <MapPin />
          <b>
            {failed
              ? "Map rendering is unavailable"
              : "No positions in this view"}
          </b>
          <span>
            {failed
              ? "The event list still provides coordinates and location details."
              : "Choose devices and dates with recorded fixes."}
          </span>
        </div>
      )}
      {tileError && !failed && (
        <span className="map-provider-note">
          Some map tiles could not load. Observations remain available.
        </span>
      )}
    </section>
  );
}
