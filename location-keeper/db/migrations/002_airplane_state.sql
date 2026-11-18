-- Apply once before running the updated ingestion adapter / Android client.
-- Existing events remain NULL; do not manufacture historical observations.
ALTER TABLE tracker_locations ADD COLUMN airplane_mode_enabled BOOLEAN NULL;
ALTER TABLE tracker_locations ADD CONSTRAINT tracker_airplane_boolean
  CHECK (airplane_mode_enabled IS NULL OR airplane_mode_enabled IN (0,1));
-- If an older reason allowlist is still installed, add the broader constraint first,
-- then remove its older subset constraint using the reviewed name from SHOW CREATE TABLE.
-- Current ingestion source already recognises these reasons:
-- airplane_mode_on, airplane_mode_off.
