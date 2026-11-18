-- App identities are Google viewers, distinct from tracker_devices/phone users.
-- Additive only: no existing tracker table or record is altered.
CREATE TABLE IF NOT EXISTS app_users (
  id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  google_subject VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
  email VARCHAR(254) NOT NULL,
  display_name VARCHAR(255) NOT NULL,
  hosted_domain VARCHAR(255) NOT NULL DEFAULT '',
  is_advanced BOOLEAN NOT NULL DEFAULT FALSE,
  is_disabled BOOLEAN NOT NULL DEFAULT FALSE,
  created_at DATETIME(3) NOT NULL,
  last_login_at DATETIME(3) NOT NULL,
  last_seen_at DATETIME(3) NOT NULL,
  INDEX app_users_created_idx (created_at, id)
);
CREATE TABLE IF NOT EXISTS app_sessions (
  id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  user_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  token_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
  started_at DATETIME(3) NOT NULL,
  last_seen_at DATETIME(3) NOT NULL,
  last_active_at DATETIME(3) NOT NULL,
  expires_at DATETIME(3) NOT NULL,
  ended_at DATETIME(3) NULL,
  end_reason VARCHAR(24) NULL,
  browser VARCHAR(32) NOT NULL,
  operating_system VARCHAR(32) NOT NULL,
  device_type VARCHAR(16) NOT NULL,
  INDEX app_sessions_started_idx (started_at, id),
  INDEX app_sessions_user_idx (user_id, started_at),
  INDEX app_sessions_active_idx (expires_at, last_active_at)
);
CREATE TABLE IF NOT EXISTS app_api_requests (
  id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  user_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NULL,
  session_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NULL,
  occurred_at DATETIME(3) NOT NULL,
  method VARCHAR(8) NOT NULL,
  route VARCHAR(100) NOT NULL,
  service VARCHAR(32) NOT NULL,
  app_view VARCHAR(16) NOT NULL,
  is_background BOOLEAN NOT NULL,
  status_code SMALLINT NOT NULL,
  duration_ms INT UNSIGNED NOT NULL,
  response_bytes BIGINT UNSIGNED NOT NULL,
  INDEX app_requests_time_idx (occurred_at, id),
  INDEX app_requests_user_idx (user_id, occurred_at),
  INDEX app_requests_session_idx (session_id, occurred_at),
  INDEX app_requests_service_idx (service, occurred_at)
);
CREATE TABLE IF NOT EXISTS app_view_events (
  id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  user_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  session_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  occurred_at DATETIME(3) NOT NULL,
  app_view VARCHAR(16) NOT NULL,
  INDEX app_views_time_idx (occurred_at, id),
  INDEX app_views_user_idx (user_id, occurred_at)
);
