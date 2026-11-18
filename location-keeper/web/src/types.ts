export type Device = {
  device_id: string;
  device_name: string;
  detected_device_name: string;
  label: string;
  revoked: boolean;
};
export type Place = {
  label: string;
  city?: string;
  country?: string;
  level: string;
  provider: string;
  looked_up_at: string;
  expires_at: string;
  attribution: string;
};
export type Event = {
  event_id: string;
  device_id: string;
  recorded_at: string;
  received_at: string;
  executed_at: string | null;
  execution_status: string | null;
  scheduled_for: string | null;
  captured_at: string | null;
  latitude: number | null;
  longitude: number | null;
  accuracy_m: number | null;
  battery_percent: number | null;
  fix_age_ms: number | null;
  is_mock: boolean;
  permission_accuracy: string;
  accuracy_mode: string;
  reason: string;
  location_status: string;
  network_transport: string;
  airplane_mode_enabled: boolean | null;
  severity: "attention" | "positive" | "limited" | "neutral";
  title: string;
  place?: Place;
};
export type Latest = {
  device_id: string;
  latest_event: Event | null;
  latest_observed_event: Event | null;
  latest_fix: Event | null;
  airplane_mode_enabled: boolean | null;
  airplane_observed_at: string | null;
};
export type Page = {
  items: Event[];
  page_cursor: string | null;
  next_cursor: string | null;
  has_more: boolean;
  as_of: string;
  expires_at: string;
  order: string;
};
export type Filters = {
  devices: string[];
  from: string;
  to: string;
  timezone: string;
  reasons: string;
  severity: string;
  outcomes: string;
  statuses: string;
  transports: string;
};
export type Session = {
  authenticated: boolean;
  user?: {
    subject: string;
    name: string;
    email?: string;
    admin: boolean;
    user_id?: string;
    is_advanced?: boolean;
  };
  analytics_enabled?: boolean;
  demo: boolean;
  now: string;
  map_style: string;
  geocoding_enabled: boolean;
  max_devices: number;
  login_url: string;
  reasons: string[];
  outcomes: string[];
  statuses: string[];
  transports: string[];
};
export type History = {
  items: Event[];
  total_observations: number;
  returned_observations: number;
  simplified: boolean;
  limit: number;
  as_of: string;
};
export type Summary = {
  days: { date: string; events: number; fixes: number; attention: number }[];
  timezone: string;
};
