// Optional read-only verification via the existing official HTTPS SQL driver.
// Uses ENV_FILE; never prints credentials, device IDs, names, or location rows.
import { connect } from "../integrations/ingest/node_modules/@tidbcloud/serverless/dist/index.js";
import { loadEnvFile } from "node:process";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const go = process.env.LK_GO_BIN || "go";
let stage = "configuration",
  transaction;
try {
  if (!process.env.ENV_FILE) throw Error("ENV_FILE is required");
  loadEnvFile(process.env.ENV_FILE);
  const cases = spawnSync(go, ["run", "./cmd/queryfixtures"], {
    cwd: path.join(root, "server"),
    encoding: "utf8",
  });
  if (cases.status !== 0) throw Error("Build SQL fixtures first");
  const queries = JSON.parse(cases.stdout);
  const db = connect({
    host: process.env.TIDB_HOST,
    username: process.env.TIDB_USERNAME,
    password: process.env.TIDB_PASSWORD,
    database: process.env.TIDB_DATABASE,
  });
  stage = "read-only snapshot";
  transaction = await db.begin();
  const registry = await transaction.execute(
    "SELECT device_id FROM tracker_devices ORDER BY device_id LIMIT 2",
  );
  const replace = (args) =>
    args.map((v) =>
      v === "fixture-device-1"
        ? registry[0]?.device_id || "00000000-0000-4000-8000-000000000001"
        : v === "fixture-device-2"
          ? registry[1]?.device_id || "00000000-0000-4000-8000-000000000002"
          : v === "fixture-cursor-time"
            ? last.recorded_at
            : v === "fixture-cursor-id"
              ? last.event_id
              : v,
    );
  let last;
  const first = await transaction.execute(
    queries.first.sql,
    replace(queries.first.args),
  );
  last = first[Math.min(19, first.length - 1)];
  let next = [];
  if (last) {
    stage = "keyset continuation";
    next = await transaction.execute(
      queries.next.sql,
      replace(queries.next.args),
    );
    const ids = new Set(first.slice(0, 20).map((e) => e.event_id));
    if (next.some((e) => ids.has(e.event_id)))
      throw Error("Duplicate event across cursor boundary");
  }
  stage = "SQL/Go classification parity";
  for (const severity of ["attention", "positive", "limited", "neutral"]) {
    const events = await transaction.execute(
      queries[severity].sql,
      replace(queries[severity].args),
    );
    for (const e of events) {
      e.is_mock = Number(e.is_mock) === 1;
      e.battery_percent =
        e.battery_percent === null ? null : Number(e.battery_percent);
      e.fix_age_ms = e.fix_age_ms === null ? null : Number(e.fix_age_ms);
    }
    const checked = spawnSync(go, ["run", "./cmd/queryfixtures", "classify"], {
      cwd: path.join(root, "server"),
      encoding: "utf8",
      input: JSON.stringify(events),
    });
    if (
      checked.status !== 0 ||
      JSON.parse(checked.stdout).some((v) => v !== severity)
    )
      throw Error("Severity mismatch");
  }
  await transaction.rollback();
  transaction = null;
  const snapshot = await db.begin();
  const read = await snapshot.execute(
    "SELECT CAST(TIDB_CURRENT_TSO() AS CHAR) tso",
  );
  await snapshot.rollback();
  const tso = String(read[0].tso);
  if (!/^[1-9][0-9]+$/.test(tso)) throw Error("No snapshot");
  await db.execute(
    "SELECT COUNT(*) n FROM tracker_locations AS OF TIMESTAMP TIDB_PARSE_TSO(" +
      tso +
      ")",
  );
  console.log(
    JSON.stringify({
      ok: true,
      read_only: true,
      keyset_sql_verified: true,
      severity_parity_verified: true,
      transactional_snapshot_verified: true,
      first_query_rows: first.length,
      next_query_rows: next.length,
    }),
  );
} catch (e) {
  if (transaction) await transaction.rollback().catch(() => {});
  console.error(
    JSON.stringify({
      ok: false,
      failed_stage: stage,
      code: e.code ?? e.details?.code ?? e.name,
    }),
  );
  process.exitCode = 1;
}
