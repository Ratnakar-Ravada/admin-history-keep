-- Additive viewer-only tables. Run with a migration account, not the read API account.
CREATE TABLE IF NOT EXISTS place_cache (
  cache_key CHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
  payload JSON NOT NULL,
  expires_at DATETIME(3) NOT NULL,
  INDEX place_cache_expiry_idx (expires_at)
);
CREATE TABLE IF NOT EXISTS viewer_device_grants (
  subject VARCHAR(255) NOT NULL,
  device_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  PRIMARY KEY (subject, device_id),
  CONSTRAINT viewer_grants_device_fk FOREIGN KEY (device_id) REFERENCES tracker_devices(device_id)
);
-- Use a dedicated SELECT-only viewer account on tracker_devices/tracker_locations.
-- Grant SELECT on viewer_device_grants only when ALLOW_ASSIGNED_USERS=true.
-- PLACE_CACHE_DSN uses a separate account with SELECT/INSERT/UPDATE on place_cache.
