# Hospital Middleware

A Go (Gin) middleware API for searching patient records from Hospital Information Systems (HIS).
Hospital staff log in with their hospital, and every search is limited to that hospital's patients.

- **Design, API spec and ER diagram:** [docs/design.md](docs/design.md)
- **Stack:** Go 1.27 · Gin · PostgreSQL 17 · Nginx · Docker Compose

## Quick start

Requirements: Docker with Compose v2.

```bash
cp .env.example .env          # local settings; change the secrets for anything beyond a demo
docker compose up --build     # nginx + api + postgres + mock-his
```

The API is served through Nginx at **http://localhost:8080**. On start-up the service applies the database
migrations, seeds two hospitals (`hospital-a`, `hospital-b`) and, with `SEED_DEMO_DATA=true` (the `.env.example`
value), a few synthetic patients. `docker compose down -v` resets the database.

| Service | Role | Exposed |
|---|---|---|
| `nginx` | Reverse proxy, rate limiting, PII-safe logging | `localhost:8080` |
| `app` | Go API service | internal only |
| `db` | PostgreSQL 17 | internal only |
| `mock-his` | Stand-in for the Hospital A HIS (`hospital-a.api.co.th` is not reachable) | internal only |

## Try it

The commands work in any bash (macOS, Linux, Git Bash on Windows) and need only `curl`.

```bash
# 1. Create a staff member of Hospital A (the hospital code or its name both work)
curl -s -X POST http://localhost:8080/staff/create \
  -H 'Content-Type: application/json' \
  -d '{"username":"nurse.a","password":"S3cure-Passw0rd","hospital":"Hospital A"}'

# 2. Log in and keep the token
TOKEN=$(curl -s -X POST http://localhost:8080/staff/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"nurse.a","password":"S3cure-Passw0rd","hospital":"hospital-a"}' \
  | sed -nE 's/.*"access_token":"([^"]+)".*/\1/p')
[ -n "$TOKEN" ] && echo "logged in" || echo "login failed: check username, password and hospital"

# 3. Search by name (Thai or English, partial, case-insensitive)
curl -s -X POST http://localhost:8080/patient/search \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"first_name":"som"}'

# 4. Search by national ID: the record is fetched from the HIS and stored locally
curl -s -X POST http://localhost:8080/patient/search \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"national_id":"1100000000032"}'

# 5. Look up one patient, the same way the HIS route works
curl -s http://localhost:8080/patient/search/AA1234567 -H "Authorization: Bearer $TOKEN"

# GET with query parameters works too (POST is recommended so national IDs stay out of URLs)
curl -s "http://localhost:8080/patient/search?last_name=jaidee" -H "Authorization: Bearer $TOKEN"
```

Demo data (all synthetic):

| Hospital | HN | Name | Identity |
|---|---|---|---|
| hospital-a | HN-A-000001 | สมชาย ใจดี / Somchai Jaidee | national ID 1100000000016 |
| hospital-a | HN-A-000002 | สมหญิง มณี รักไทย / Somying Manee Rakthai | national ID 1100000000024 |
| hospital-a | HN-A-000003 | จอห์น ไมเคิล สมิธ / John Michael Smith | passport AA1234567 |
| hospital-a (HIS only) | HN-A-000004 | วิชัย มั่นคง / Wichai Mankong | national ID 1100000000032 — appears after a search by this ID |
| hospital-b | HN-B-000001 | สมชาย ใจดี / Somchai Jaidee (same person, own HN) | national ID 1100000000016 |
| hospital-b | HN-B-000002 | มาลี ศรีสุข / Malee Srisuk | national ID 1100000000041 |

A staff member of `hospital-b` searching `{"first_name":"som"}` gets only `HN-B-000001`: the same person's
Hospital A record is never visible to Hospital B.

**Search scope:** the Hospital A API looks up one patient by national ID or passport ID only, so a search by name,
date of birth, phone or email covers the patients the middleware has already received from the HIS (the demo seed,
or an earlier search by identity number). For example, `{"first_name":"wichai"}` finds nothing on a fresh stack
and finds Wichai once step 4 has fetched him. See [docs/design.md §1](docs/design.md#1-architecture).

## Tests

```bash
make test               # unit tests, no database needed
make cover              # unit-test coverage (coverage.out) and total
make test-integration   # repository tests against a throw-away PostgreSQL container (needs Docker)
make cover-all          # coverage of unit + integration tests
make test-e2e           # end-to-end tests through Nginx against the running stack (docker compose up first)
make vuln               # known-vulnerability scan of the dependencies (govulncheck)
```

Coverage at the submitted commit: **89.6%** of statements in `internal/` with the unit tests alone, **97.5%** with the
integration tests.

| Suite | What it proves |
|---|---|
| Unit (`go test ./...`) | Every layer with fakes for the layer below; positive and negative cases for every endpoint |
| Integration (`-tags integration`) | The real SQL: upserts, Thai/English partial name search, wildcard escaping, phone formats, per-hospital uniqueness, pagination |
| End-to-end (`-tags e2e`) | The whole stack through Nginx: staff create/login, hospital isolation, HIS fetch-and-store, every filter, error codes |

The e2e check that a patient comes from the HIS is conclusive on a fresh database (`docker compose down -v` first);
on a reused one it notes that the record was already stored by an earlier run.

## Configuration

Set in `.env` (see `.env.example`); `docker-compose.yml` fixes `APP_PORT`, `GIN_MODE` and the mock HIS port.

| Variable | Default (service) | Description |
|---|---|---|
| `POSTGRES_USER` / `POSTGRES_PASSWORD` | `hospital` / — (required by compose) | Database credentials; keep the password URL-safe |
| `DATABASE_URL` | — (required; built by compose) | PostgreSQL connection URL |
| `JWT_SECRET` | — (required, ≥ 32 chars) | HMAC secret for access tokens; a warning is logged if it is the sample value |
| `JWT_TTL` | `1h` | Access token lifetime |
| `STAFF_REGISTRATION_KEY` | empty | When set, `/staff/create` requires the header `X-Registration-Key` with this value |
| `HIS_BASE_URLS` | empty (`hospital-a=http://mock-his:8081` in compose) | `hospital-code=base-url` pairs, comma-separated; the real Hospital A API would be `hospital-a=https://hospital-a.api.co.th` |
| `HIS_TIMEOUT` | `3s` | Timeout of one HIS call |
| `RUN_MIGRATIONS` | `true` | Apply migrations on start-up |
| `SEED_DEMO_DATA` | `false` (`true` in `.env.example`) | Load the synthetic demo patients |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |

## Project layout

See [docs/design.md §2](docs/design.md#2-project-structure).
