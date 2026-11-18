-- Review SHOW INDEX first. This index supports the new latest-upload and
-- new-arrival queries; it is separate from the existing recorded/captured indexes.
CREATE INDEX tracker_locations_device_received_idx
  ON tracker_locations(device_id, received_at);
-- No redundant event-ID suffix index is added: clustered PK handles already
-- participate in TiDB's secondary index ordering. Verify actual query plans.
