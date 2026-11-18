# Location Keeper

A responsive React/Vite location explorer with a small Go API, a complete event feed, and optional Android ingestion integration. The frontend runs as a Cloudflare Worker with static assets; the Go API can run behind a Cloudflare Tunnel.

## Run locally

Requires **Go 1.26+** and **Node 22.12+**. Android work additionally requires JDK 21 and Android SDK 36.

In one terminal:

```sh
cd server
go mod download
go run ./cmd/api
```

In a second terminal:

```sh
cd web
npm ci
npm run dev
```

Open **http://127.0.0.1:5173**. The default workspace contains generated fictional devices and observations. It never connects to a private database, and the Go server refuses to expose demo mode beyond loopback.

For one local process after building the frontend:

```sh
cd web
npm run build
cd ../server
PUBLIC_BASE_URL=http://127.0.0.1:8080 go run ./cmd/api
```

Open **http://127.0.0.1:8080**. `WEB_DIR` can point to a different built frontend directory.

## Cloudflare Worker frontend with a tunneled Go API

The website is `https://admin-location-keep.rockyworks.in`; its upstream API is `https://api-admin-location-keep.rockyworks.in`.

The Worker serves the Vite build as static assets and forwards `/api/*`, `/auth/*` and `/healthz` to the API tunnel. The browser uses relative URLs on the website origin, so Google flow/session cookies stay on the website hostname and the Go server keeps its existing Origin checks. Static assets bypass Worker code; the configured API routes run through the Worker.

From `web/`, build and deploy the Worker using the authenticated Wrangler CLI:

```sh
npm ci
npm run build
npx wrangler whoami
npx wrangler deploy
```

`web/wrangler.jsonc` names the Worker `admin-history-keep`, serves `dist/`, and routes API/auth/health requests through `web/worker.ts`. It also sets the public API origin and attaches `admin-location-keep.rockyworks.in` as its custom domain. Run the commands after each frontend change. For deployment from a clean checkout, use Node 22.12 or newer.

Wrangler manages the website custom domain from the checked-in configuration. In the Cloudflare dashboard, it appears under **Workers & Pages → admin-history-keep → Settings → Domains & Routes**. Keep `api-admin-location-keep.rockyworks.in` routed to the existing tunnel. Cloudflare provisions TLS for the website hostname.

For the API tunnel's published application, set the local service URL to **`http://localhost:8080`** when the connector runs on the Go host. The Go server uses plain HTTP locally; the public API origin stays **`https://api-admin-location-keep.rockyworks.in`**, with TLS handled by Cloudflare. Setting the local service to `https://localhost:8080` causes a `502` because Go does not listen for TLS there. If the connector runs in a container, use a service address reachable from that container.

If Wrangler reports `UNABLE_TO_GET_ISSUER_CERT_LOCALLY` on a managed Mac, use a current Node version with the system trust store: `NODE_USE_SYSTEM_CA=1 npx wrangler whoami` (and the same prefix for `deploy`). This keeps certificate verification enabled.

If a newly attached hostname returns `NXDOMAIN` locally while the dashboard shows its Worker DNS record, the network's DNS resolver may still have a cached negative answer. Check `dig admin-location-keep.rockyworks.in`; the SOA TTL in a negative response shows the remaining cache lifetime. Retry after expiry. A browser `ERR_EMPTY_RESPONSE` can also involve a managed proxy; if it persists after DNS resolves, ask the network administrator to check the hostname's proxy path.

On the **Go host**, change and restart with:

```dotenv
PUBLIC_BASE_URL=https://admin-location-keep.rockyworks.in
```

This is the browser origin, even though Go is reached through the separate API hostname. Keep `DATA_SOURCE=tidb` and all private database/Google/geocoding/Gmail credentials on the Go host. Only the public `API_ORIGIN` belongs in the Worker configuration. Analytics access still follows the database `is_advanced` column.

Register both Google OAuth redirect URIs:

```text
https://admin-location-keep.rockyworks.in/auth/callback
https://admin-location-keep.rockyworks.in/auth/gmail/callback
```

Add the website origin to the Geoapify browser map key's allowed origins. Worker preview URLs are not production OAuth origins; preview sign-in is unsupported against a backend configured for the production domain. Use a separately configured backend/OAuth origin for staging sign-in.

Validate the origin service on its host with `curl -fsS http://127.0.0.1:8080/healthz`, then check `/healthz` on the API hostname from an allowed external network. Expected JSON: `{"ok":true}`. The API root `/` can show a missing-frontend message when only Go is installed; it is not the health endpoint. The tunnel connector must reach Go from its own host/container. A connector running in another container cannot reach a host process through its container's `localhost`.

Once the Worker is deployed, `/healthz` and `/api/session` on the **website** hostname should return API JSON. Verify Google sign-in, device access, the database analytics flag and logout there. API/auth responses are private/no-store; do not put cache-everything rules over those routes.

References: [Workers static assets](https://developers.cloudflare.com/workers/static-assets/), [Wrangler configuration](https://developers.cloudflare.com/workers/wrangler/configuration/), [custom domains](https://developers.cloudflare.com/workers/configuration/routing/custom-domains/).

## Included

- **Live:** latest measured position, latest observed event, latest received upload, battery/network context and explicitly observed airplane state.
- **Journeys:** date/device filters, worldwide vector map, clustering, accuracy circles, recorded-point playback, daily activity and explicit interruption boundaries. Broad histories disclose daily representative sampling.
- **Activity logs:** 20 records per API page, indexed keyset pagination, deterministic timestamp/ID ordering across devices, snapshot-bound signed cursors, filter resets, virtualised rows, an accessible simple list, expandable raw details and loading/retry/end states.
- **Event indicators:** small red attention, green report/recovery, amber limited and neutral context indicators. Colour is paired with text and icons.
- **Place labels:** Geoapify adapter, coalescing/rate limiting, memory or TiDB cache, expiry/backoff and accuracy-aware area/city labels. Log pagination never waits for an external lookup.
- **Alerts:** the two approved rules are visibly marked Coming soon. Automated monitoring remains outside the implemented scope.
- **Analytics:** persistent Google app users, tracked/revocable sessions, API/service usage, response times, errors, view opens and foreground activity. The dashboard and its APIs require the user's database `is_advanced` flag.
- **Access:** Google sign-in with PKCE and nonce validation, verified-account allowlists, encrypted cookies, database-validated sessions, optional per-device grants, origin checks on writes and tracking SELECT-only database queries.

The dashboard is a responsive **mobile web app**, opened in a phone or tablet browser using the same URL as the PC version. It is not a separate native dashboard app or an installable/offline PWA. The separate native `android/` app collects and uploads location reports; browser access to the dashboard does not start background phone tracking.

The initial demo uses OpenFreeMap's attributed vector style without credentials. `MAP_PUBLIC_KEY` switches the basemap to Geoapify. Synthetic area labels are explicitly identified as illustrative. The demo fixture is generated in Go and contains no imported private GPS export.

## Set up Geoapify maps and area names

Both keys below come from **Geoapify**, not Google Maps. Sign in to [Geoapify MyProjects](https://myprojects.geoapify.com/), create a project, and open **API keys**. Create separate browser and server keys so their restrictions can differ.

| Environment setting | Key description  | Restriction                                                                                                                              |
| ------------------- | ---------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `MAP_PUBLIC_KEY`    | Browser maps     | Configure **Allowed origins** for the actual website/dev origins below. This key is intentionally visible in browser requests.           |
| `GEOAPIFY_API_KEY`  | Server geocoding | Keep private. Restrict to the backend's outgoing public IP when it is stable. Browser-origin restrictions do not apply to Go's requests. |

For the browser key, add the origins you use, without a URL path:

```text
http://127.0.0.1:8080
http://127.0.0.1:5173
http://localhost:5173
https://admin-location-keep.rockyworks.in
```

These are entries for **Allowed origins**. If using **Allowed HTTP referrers** instead, Geoapify supports regular-expression patterns; follow that field's syntax. Its **CORS Access-Control-Allow-Origin** setting alone does not protect a key. Separate keys within one Geoapify project share the project's quota. See the [official API-key guide](https://myprojects.geoapify.com/help/api-keys/).

Save the values in the local `.env`:

```dotenv
MAP_PUBLIC_KEY=your_browser_maps_key
GEOAPIFY_API_KEY=your_private_server_geocoding_key
```

Start/restart Go with `ENV_FILE` pointing to that file. Browser maps use the key when requesting the Geoapify style and tiles. The private key resolves coordinates into approximate area/city labels; it is never returned to the frontend. Test it using the project's **Try API → Reverse Geocoding** tool. The demo deliberately retains illustrative labels; actual provider lookups are used in private-data mode. See [reverse-geocoding documentation](https://apidocs.geoapify.com/docs/geocoding/reverse-geocoding/).

Without `MAP_PUBLIC_KEY`, the app continues to use the attributed OpenFreeMap basemap. Without `GEOAPIFY_API_KEY`, private-data views show coordinates and a not-configured message instead of provider labels.

## Configure Google sign-in

Google is the only sign-in provider. It uses Google's standard server-side authorization-code flow, with PKCE, state and nonce validation. The issuer is fixed to `https://accounts.google.com`; there is no configurable identity-provider URL.

1. Create/select a project in [Google Cloud Console](https://console.cloud.google.com/auth/overview).
2. Configure **Google Auth Platform → Branding** with the app name and support/contact email. Under **Audience**, choose the suitable audience; while an external app is in Testing, add your intended sign-in account as a test user. For the production domain, complete any domain/branding requirements shown by Google.
3. Under **Clients**, create an OAuth client with type **Web application**.
4. Add the exact **Authorized redirect URI** for each environment you actually use:

   ```text
   http://127.0.0.1:8080/auth/callback
   http://127.0.0.1:5173/auth/callback
   http://localhost:5173/auth/callback
   https://admin-location-keep.rockyworks.in/auth/callback
   ```

   **With the default Vite development setup, register both `http://127.0.0.1:5173/auth/callback` and `http://127.0.0.1:5173/auth/gmail/callback`.** Registering only port 8080 produces `redirect_uri_mismatch` while the app uses port 5173. Put these in **Authorized redirect URIs**, not JavaScript origins. After saving, return to the app and begin sign-in again.

   `PUBLIC_BASE_URL` must match the browser origin. The Go API performs the redirect flow; a browser-side Google SDK is not required. Do not enter `/auth/login` as the callback.

5. Copy the OAuth client ID/secret into `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`. Keep the secret on the server. Set `GOOGLE_ADMIN_EMAILS` to your exact Gmail or Google Workspace email address; multiple administrator addresses can be comma-separated. No wildcard domains are accepted.

```dotenv
GOOGLE_CLIENT_ID=your_web_client.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=your_web_client_secret
GOOGLE_ADMIN_EMAILS=you@gmail.com
GOOGLE_ADMIN_SUBJECTS=
```

Google ID-token signatures, issuer, audience, expiry, nonce and `email_verified` are checked before creating an encrypted HTTP-only session. Ordinary sign-in requests only `openid email profile` and discards its OAuth API tokens. The separate sender connection below stores its own encrypted Gmail grant. See [Google's server sign-in documentation](https://developers.google.com/identity/openid-connect/openid-connect).

The signed-in identity uses Google's permanent `sub` account ID for cursors and `viewer_device_grants`. Exact email allowlisting is supported for verified Gmail addresses and Google Workspace accounts with a signed `hd` claim. A Google consumer account using a third-party address must instead be explicitly pinned in `GOOGLE_ADMIN_SUBJECTS`; Google may no longer be authoritative for that external address. Existing Google grants use the same immutable account ID. If grants were created for another identity provider, review and remap those subjects to the corresponding Google account IDs before allowing assigned users. Existing sessions from the previous provider are invalidated by this change.

Generic `OIDC_*` configuration is no longer read. Google account settings remain blank until supplied. The checked-in `.env.example` contains placeholders; `.env` stays private.

### Connect Gmail for email delivery

Enable **Gmail API** in the same Google Cloud project. Under **Google Auth Platform → Data Access**, add the sensitive scope `https://www.googleapis.com/auth/gmail.send` alongside the sign-in scopes. This permits sending plain text, HTML cards with links/buttons, and MIME attachments; inbox access and draft-management scopes are not needed. Interactive AMP email is a different feature requiring [sender registration](https://developers.google.com/workspace/gmail/ampemail/register).

Add a second **Authorized redirect URI** to the Web application client, matching the environment:

```text
http://127.0.0.1:8080/auth/gmail/callback
http://127.0.0.1:5173/auth/gmail/callback
http://localhost:5173/auth/gmail/callback
https://admin-location-keep.rockyworks.in/auth/gmail/callback
```

Configure the sender:

```dotenv
GMAIL_ENABLED=true
GMAIL_SENDER_EMAIL=you@gmail.com
GMAIL_TOKEN_FILE=../.private/gmail-sender.enc
```

Use the same Google account as an administrator. Set a stable `SESSION_SECRET`, restart the server, sign into the private-data workspace, then open **Alerts → Email delivery → Connect Gmail**. Demo mode keeps the sender disabled. Choose the configured sender account and approve sending. A successful connection stores an encrypted refresh token on the server, bound to the account and OAuth client. Ordinary sign-in still requests only identity access; adding a scope in Cloud Console alone does not grant it.

The consent flow requests offline access for future background jobs. External OAuth apps in **Testing** generally receive Gmail refresh tokens that expire after seven days. Configure the appropriate production publishing/verification settings before relying on unattended alerts; a connection can also be revoked or expire. See [Google refresh-token rules](https://developers.google.com/identity/protocols/oauth2#expiration) and [Gmail scopes](https://developers.google.com/workspace/gmail/api/auth/scopes).

The token file is created with mode `0600` inside a private directory and excluded from the source archive. Keep its storage durable and retain `SESSION_SECRET`; changing the secret/client/sender requires reconnecting Gmail. **Disconnect sender** removes the local stored grant. Google-account access can separately be revoked in the account's third-party connection settings.

The backend includes a MIME builder, escaped HTML-card template, attachments up to 20 MiB total, access-token refresh and a Gmail send adapter. Tests use synthetic provider responses; no email was sent. There is no public send endpoint and no automatic retry of an uncertain send. Automated alert evaluation/scheduling remains Coming soon. See [Google's sending guide](https://developers.google.com/workspace/gmail/api/guides/sending).

## Configure private TiDB data

Copy `.env.example` to a private `.env` only if you do not already have one. Set `ENV_FILE` to its absolute path when starting Go. Environment files and private data are excluded from Git.

```sh
cd server
ENV_FILE=/absolute/path/to/location-keeper/.env go run ./cmd/api
```

| Setting                                                   | Meaning                                                                                                                                           |
| --------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| `DATA_SOURCE=tidb`                                        | Use the SQL repository; demo authentication is unavailable in this mode.                                                                          |
| `TIDB_TRANSPORT`                                          | Explicit `mysql` (TCP 4000) or `https` (Cloud Starter/Essential HTTPS SQL).                                                                       |
| `TIDB_DSN`                                                | A dedicated SELECT-only viewer connection to the existing tracking tables. The Go driver enforces TLS.                                            |
| `SESSION_SECRET`                                          | A stable random secret of at least 32 bytes. Generate locally with `openssl rand -base64 48`.                                                     |
| `PUBLIC_BASE_URL`                                         | Exact browser origin, e.g. `http://127.0.0.1:5173` for Vite or `http://127.0.0.1:8080` for the built preview. HTTPS is required outside loopback. |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`                | Google OAuth Web application credentials as configured above.                                                                                     |
| `GOOGLE_ADMIN_EMAILS`                                     | Exact administrator Gmail/Google Workspace addresses.                                                                                             |
| `GOOGLE_ADMIN_SUBJECTS`                                   | Optional immutable Google account IDs; required for administrators with third-party consumer email addresses.                                     |
| `ALLOW_ASSIGNED_USERS=true`                               | Optionally permit other verified Google accounts, limited by `viewer_device_grants`. Default false.                                               |
| `GEOAPIFY_API_KEY`, `MAP_PUBLIC_KEY`                      | Private reverse-geocoding and public browser-map keys as configured above.                                                                        |
| `GMAIL_ENABLED`, `GMAIL_SENDER_EMAIL`, `GMAIL_TOKEN_FILE` | Optional Gmail sender consent and encrypted credential storage, configured above.                                                                 |
| `PLACE_CACHE_DSN`                                         | Optional separate connection with SELECT/INSERT/UPDATE privileges on `place_cache`. Without it, the cache is in memory.                           |
| `APP_DB_DSN`                                               | App-only connection with SELECT/INSERT/UPDATE on the four analytics tables. If blank, uses `TIDB_DSN` in a separate pool; that credential must permit the app writes. |

Private mode fails closed when Google identity configuration is missing. A device's upload token or friendly name never grants web-viewer access.

Apply reviewed additive scripts from `db/migrations` using a migration account when enabling the related integration. The API does not run migrations or modify tracking records. Use `SHOW CREATE TABLE tracker_locations` to review the installed reason constraint before changing it; current ingestion source already recognises airplane-mode reasons. The nullable airplane column is optional for viewer reads: explicit existing reason events still establish the observed on/off state, and older unknown states remain unknown.

### App users, sessions and usage analytics

Private mode requires four additive app tables. From `server/`, run once using a credential with CREATE privileges:

```sh
ENV_FILE=../.env go run ./cmd/migrateapp -apply
```

This command creates/verifies only `app_users`, `app_sessions`, `app_api_requests` and `app_view_events`; it does not alter tracking tables. Without `-apply`, it shows the migration summary. These four tables have already been applied and integration-tested in the owner's current local database.

Restart the API, then sign in again. Older stateless cookies cannot create a tracked session. Successful Google sign-in upserts the app account by Google subject and starts a saved session. Each authenticated request validates its hashed session token against TiDB; logout, expiry or `is_disabled` prevents further use. Authentication fails closed during a database outage. Sessions retain the existing maximum one-hour lifetime. An account can have concurrent sessions in different browsers.

`app_users.is_advanced` is the source of truth for **Analytics across all app users**. Set it directly in TiDB to grant or revoke advanced access for an existing app account:

```sql
SELECT id, email, is_advanced FROM app_users;
UPDATE app_users SET is_advanced = TRUE WHERE id = 'the-app-user-uuid';
-- Revoke later with is_advanced = FALSE.
```

Every authenticated API request reads the current flag from TiDB, including requests from already signed-in sessions. Refresh the browser to update the Analytics navigation. New users use the database's `FALSE` default, including Google administrators. Sign-in and API startup preserve manually assigned values; there is no synchronization from environment variables and no self-service role-changing API.

`GOOGLE_ADMIN_EMAILS` and `GOOGLE_ADMIN_SUBJECTS` retain their existing sign-in/device-administrator roles; they do not grant advanced analytics. An app viewer outside those administrator lists can have advanced access through the database flag. The viewer must still be allowed to sign in under the existing access policy (`ALLOW_ASSIGNED_USERS` for ordinary viewers). The advanced flag does not grant access to additional devices or Gmail sender permissions. `is_disabled = TRUE` still blocks the account.

Restart the Go API once after updating from the earlier email-based policy. Subsequent database flag changes need no API restart or new sign-in.

The dashboard offers 7/30/90-day reports, users ranked by user-driven requests, API frequency/latency/errors, service categories, view opens and session/client history with drill-down. Detail lists use signed keyset cursors and load 20 rows per page. Cursors bind the list and filters. Analytics lists are eventually consistent, without the location feed's historical snapshot guarantee. The registration list includes all accounts; its request counts use the selected period.

Records contain normalized route, service, app view, timestamp, account/session IDs, foreground/background classification, response status, elapsed milliseconds and response bytes. Client details are coarse browser/OS/device categories. GPS coordinates, query values, bodies, raw user agents, IP addresses, credentials and cookie/OAuth tokens are not copied into analytics. TiDB stores session token hashes. App viewers and device owners remain separate identities.

Navigation sends a view event per view change. A visible page sends a 30-second heartbeat, marked active only when new input occurred since the preceding heartbeat (and within five minutes). These estimate activity rather than precise time spent. Automatic polling is separate from user-driven requests and does not raise the primary user ranking. Browser metadata is advisory; analytics are not a tamper-proof audit. Service counts measure app API categories: place-resolution calls may use the cache, and direct browser map tiles are outside these counts.

Usage writes use bounded batches, normally every two seconds, with an idempotent retry. The queue holds 2,048 records; overload or exhausted writes can lose telemetry. Dashboard counters report dropped records/write failures since startup. User/session creation and role checks are synchronous. History starts with this update; there is no backfill. Rows remain until an explicit retention policy removes them; the 90-day dashboard limit does not delete older rows. Demo analytics use fictional in-memory data.

For separate database users, grant the tracking reader SELECT on tracking/grant tables and the app writer SELECT/INSERT/UPDATE only on the four `app_*` tables. Configure `APP_DB_DSN` for that writer. Runtime does not need CREATE or DELETE. The same `TIDB_TRANSPORT` applies to both pools.

The opt-in integration test creates and removes only its own synthetic fixtures. Its credential additionally needs DELETE on the four app tables:

```sh
ENV_FILE=/absolute/path/to/.env RUN_APP_DB_INTEGRATION=1 go test ./internal/app -run '^TestUsageSQLIntegration$' -count=1 -v
```

### TiDB connection transport

`TIDB_TRANSPORT=mysql` (default) uses the normal TLS MySQL connection on port 4000. `TIDB_TRANSPORT=https` uses TiDB Cloud Starter/Essential's SQL-over-HTTPS endpoint on port 443 with the same private DSN credentials. Select the transport explicitly; the app never retries a statement through another transport. The optional place-cache connection uses the same selected transport.

```dotenv
TIDB_TRANSPORT=https
```

The Go HTTPS adapter is maintained in `server/internal/tidbhttp` and follows the wire protocol of PingCAP's serverless driver. It requires no Node runtime, intermediary server or frontend database credentials. The provider protocol is in preview and supports public Starter/Essential endpoints; use MySQL for Dedicated/private endpoints. See [TiDB's serverless-driver documentation](https://docs.pingcap.com/developer/serverless-driver/).

The adapter preserves server session tokens and starts transaction snapshots with a fresh session, then restores the original connection session. Parameters use UTF-8 hex literals instead of backslash escaping. Provider error bodies and SQL values are not logged. Responses reaching the provider's 10,000-row boundary are rejected to prevent silent truncation. The HTTP API does not accept `START TRANSACTION READ ONLY`; this adapter enforces read-only transaction operations locally. Use a SELECT-only viewer account as the database permission boundary.

## Startup troubleshooting

The API reads a local environment file only when `ENV_FILE` is set. From `server/`, use:

```sh
ENV_FILE=../.env go run ./cmd/api
```

A plain `go run ./cmd/api` uses exported environment variables and otherwise defaults to synthetic demo mode. With Vite on port 5173, open `http://127.0.0.1:5173` and keep `PUBLIC_BASE_URL` set to that origin.

If port 8080 is occupied, `lsof -nP -iTCP:8080 -sTCP:LISTEN` identifies the process. Stop the earlier API instance before starting another. The API reports the actual bind error and only logs listening after a successful bind.

Use `?tls=true` in the DSN. TiDB Console's Go sample uses `tls=tidb` with a separate custom TLS registration; that named profile is not registered by this app.

For a read-only connection diagnostic, run `ENV_FILE=../.env go run ./cmd/checkdb` from `server/`. Credentials are redacted. If direct TCP reports `connect: bad file descriptor` before authentication, choose `TIDB_TRANSPORT=https` for a public Cloud Starter/Essential endpoint. The owner's local configuration now uses HTTPS, and the complete Go read diagnostic passes without database modifications.

## Event feed and cursor contract

`GET /api/events` accepts selected `device_ids`, UTC `from` inclusive / `to` exclusive, `timezone`, and optional comma-separated `reasons`, `severity`, `outcomes`, `statuses`, `transports`. Page size is fixed at 20. `cursor` is opaque and authenticated.

- The ordered key is `(recorded_at DESC, event_id DESC)`. Each response includes `page_cursor` to replay that exact page at its original snapshot, including page one.
- A bounded per-device `UNION ALL` merges the next 21 matching records in one query. Return 20 and use the extra only for `has_more`.
- Cursors bind the signed-in subject, current device access scope, normalised filters, last key and TiDB snapshot. They expire after four minutes by default, below the expected ten-minute GC history window. Verify the instance's actual history configuration; do not extend cursor expiry past it.
- A brief read-only transaction obtains a nonzero TSO. Each page borrows a connection, selects that read snapshot, and resets the connection before returning it to the pool. No transaction remains open while a person scrolls.
- Changed filters cancel the old query, clear rows/cursors, and start with 20 at the top. Returning to an old combination also starts over.
- New arrivals show a refresh banner. An expired cursor offers a restart with the same filters. It is never silently appended to an old list.
- At most ten row pages remain in the browser. Lightweight page-start cursors permit older pages to be recovered while the snapshot is valid.
- Map history is a separate request and does not depend on loaded event pages.

All historical colours describe events at their recorded time. `fresh` describes the fix when recorded; Live separately displays its current age. An internet outage does not establish airplane mode or a radio-setting change. Offline events appear after upload and retain their recorded timestamps.

`received_at`, `recorded_at`, `executed_at`, `scheduled_for` and `captured_at` remain distinct. Older nullable execution metadata displays Not recorded. Server data does not claim to know the phone's local upload queue length.

## Android and ingestion compatibility

`android/` is a clean source copy of the existing Android project, without local databases, credentials or generated builds. The current event collectors, including explicit airplane transitions, are preserved. Version **0.2.3 / code 8** adds:

- SQLite v5 nullable airplane-state storage.
- An additive v1–v4 upgrade path that preserves pending readings.
- Explicit observed state on newly produced airplane transition records.
- Omission of missing metadata from older stored upload projections, preserving immutable retries.

`integrations/ingest/` contains the matching validation/SQL adapter and tests. Apply the nullable database-column migration and update the ingestion handler before using the newer phone payload. No APK was installed, no existing ingestion service was replaced, and no tracking-table migration was applied. The separate app analytics migration above has been applied.

Run Android checks locally with a compatible `JAVA_HOME` and `ANDROID_HOME`:

```sh
cd android
./gradlew :app:testDebugUnitTest :app:assembleDebug
```

## Verification

```sh
cd server
go test -race ./...
go vet ./...
cd ../web
npm ci
npm test
npm run build
npx playwright test
cd ../integrations/ingest
npm ci
npm test
```

Browser tests expect the built app and Go API at `http://127.0.0.1:8080`; override `APP_URL` if needed. On macOS they use installed Edge; elsewhere install Playwright Chromium or set `BROWSER_EXECUTABLE_PATH`. The Google sign-in protocol test starts a trusted local test HTTPS server and may require network-listen permission in a restricted executor.

For optional read-only integration checks:

```sh
cd server
ENV_FILE=/private/path/to/database.env go run ./cmd/checkdb
```

The checker accepts `TIDB_DSN`, or `TIDB_HOST`, `TIDB_USERNAME`, `TIDB_PASSWORD`, `TIDB_DATABASE`. It reports aggregates and does not print rows, names or credentials. Direct outbound TCP port 4000 is unavailable on this workstation. The Go HTTPS adapter now passes the complete diagnostic, including keyset pagination, severity parity, map/summary/latest queries and pooled snapshot cleanup. The original JavaScript HTTPS verifier is also available:

```sh
ENV_FILE=/private/path/to/database.env node tools/check-sql-http.mjs
```

The HTTPS verifier needs the ingestion package installed and Go on PATH (`LK_GO_BIN` can override it). It uses SELECTs and rolled-back transactions only. See `docs/VALIDATION.md` for the recorded results and boundaries.

## Repository layout

```text
web/                    React/Vite UI, unit and browser tests
server/cmd/api/         Go API and static-asset server
server/internal/app/    auth, repositories, cursors, events and places
server/cmd/checkdb/     optional read-only direct-connection diagnostic
db/migrations/         reviewed additive database changes
api/openapi.yaml        API contract
android/               Android source and upgrade tests
integrations/ingest/    compatible ingestion handler and tests
tools/                 read-only SQL verification
docs/                  validation results and synthetic screenshots
```

Build outputs, environment files, local databases, credentials, phone exports and dependency directories are ignored. The repository is prepared for the owner to add to GitHub; no remote repository, push, hosting setup or deployment has been performed.
