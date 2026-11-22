-- App-owned state only. The Android registration service owns tracking_email
-- and pin_hash on tracker_devices; this migration never changes tracker tables.
CREATE TABLE IF NOT EXISTS app_device_unlocks (
  session_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  device_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  pin_version CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  unlocked_at DATETIME(3) NOT NULL,
  PRIMARY KEY (session_id, device_id),
  CONSTRAINT app_device_unlock_session_fk FOREIGN KEY (session_id) REFERENCES app_sessions(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS app_device_pin_attempts (
  device_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  attempts INT NOT NULL DEFAULT 0,
  window_started_ms BIGINT NOT NULL,
  attempt_nonce CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL
);
