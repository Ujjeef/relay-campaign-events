# Campaign Events

A small Go service that receives delivery-provider webhook events for marketing
campaigns and serves per-campaign statistics for a live dashboard.

The service is deliberately boring in the right places:

- **Idempotent ingestion** — an `event_id` is stored at most once, so provider
  retries never double-count.
- **Per-item batch handling** — one malformed event does not discard the other
  199 events in the request.
- **Order-independent stats** — late and out-of-order events are stored by their
  timestamp; totals do not depend on arrival order.
- **Concurrency-safe** — duplicate checks and inserts are atomic, and stats
  reads are safe while ingestion continues.

## Technologies

- Go 1.22+ (standard library only)
- In-memory storage behind a `Repository` interface (`internal/repository`)
- `net/http` with Go 1.22 method + path-pattern routing

No third-party dependencies, so there is no `go.sum`. Swapping the in-memory
store for SQLite only requires implementing the `Repository` interface.

## Project structure

```
campaign-events/
├── main.go                        # wiring: repo -> service -> handlers -> server
├── go.mod
├── internal/
│   ├── models/event.go            # Event, Stats, validation helpers
│   ├── repository/
│   │   ├── repository.go          # storage interface
│   │   └── memory.go              # in-memory, concurrency-safe implementation
│   ├── services/ingest.go         # batch parsing, validation, per-item results
│   └── handlers/handlers.go       # HTTP endpoints
├── starter/                       # supplied skeleton (reference)
│   └── seed/events.json           # realistic dirty provider traffic
├── debugging/                     # Part 3 program with all four fixes applied
├── NOTES.md                       # Parts 1, 4, 5 + completion notes
├── BUGS.md                        # Part 3 findings
├── AI_USAGE.md                    # AI usage disclosure
└── DOCUMENTATION.md               # step-by-step engineering walkthrough
```

## Install dependencies

There are none beyond the Go toolchain:

```bash
go version          # must be 1.22 or later
go mod download     # no-op, but safe
```

## Run the service

From the `campaign-events/` directory:

```bash
go run .
```

The server listens on `:8080`. Override with `PORT=9090 go run .`.

Data is in memory and is reset when the process restarts.

## API endpoints

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/events` | Accept a JSON array of provider events |
| `GET` | `/campaigns/{campaign_id}/stats` | Per-campaign totals |
| `GET` | `/campaigns/{campaign_id}/events?limit=20&offset=0` | Paginated recent activity (newest event time first) |

### `POST /events`

Every element is validated independently. The batch returns `200` when the body
is a JSON array, with a per-item report. Only a malformed body (not an array)
returns `400`, so providers are not asked to retry permanent data problems.

```bash
curl -s -X POST localhost:8080/events \
  -H 'Content-Type: application/json' \
  -d '[
    {"event_id":"evt_1","campaign_id":"cmp_summer_sale","contact_id":"ct_001","type":"sent","timestamp":"2026-08-10T06:15:00Z"},
    {"event_id":"evt_2","campaign_id":"cmp_summer_sale","contact_id":"ct_001","type":"delivered","timestamp":"2026-08-10T06:16:00Z"},
    {"event_id":"evt_2","campaign_id":"cmp_summer_sale","contact_id":"ct_001","type":"delivered","timestamp":"2026-08-10T06:16:00Z"},
    {"event_id":"","campaign_id":"cmp_summer_sale","contact_id":"ct_001","type":"opened","timestamp":"bad"}
  ]'
```

```json
{
  "received": 4,
  "accepted": 2,
  "duplicates": 1,
  "invalid": 1,
  "results": [
    {"index": 0, "event_id": "evt_1", "status": "accepted"},
    {"index": 1, "event_id": "evt_2", "status": "accepted"},
    {"index": 2, "event_id": "evt_2", "status": "duplicate"},
    {"index": 3, "status": "invalid", "error": "event_id is required"}
  ]
}
```

A duplicate whose payload differs from the stored event is reported as
`"status": "duplicate"` with `"conflict": true` and an explanation; the stored
version is never overwritten.

### `GET /campaigns/{campaign_id}/stats`

```bash
curl -s localhost:8080/campaigns/cmp_summer_sale/stats
```

```json
{
  "campaign_id": "cmp_summer_sale",
  "sent": 1,
  "delivered": 1,
  "opened": 0,
  "clicked": 0,
  "unique_opens": 0
}
```

`opened` is the number of open events; `unique_opens` is the number of distinct
contacts who opened. Both are provided because dashboards use both.

Returns `404` if the campaign has no stored events.

### `GET /campaigns/{campaign_id}/events`

```bash
curl -s 'localhost:8080/campaigns/cmp_summer_sale/events?limit=2&offset=0'
```

```json
{
  "campaign_id": "cmp_summer_sale",
  "total": 2,
  "limit": 2,
  "offset": 0,
  "events": [
    {"event_id":"evt_2","campaign_id":"cmp_summer_sale","contact_id":"ct_001","type":"delivered","timestamp":"2026-08-10T06:16:00Z"},
    {"event_id":"evt_1","campaign_id":"cmp_summer_sale","contact_id":"ct_001","type":"sent","timestamp":"2026-08-10T06:15:00Z"}
  ]
}
```

`limit` defaults to 20 and must be 1–100. `offset` must be `>= 0`. Events are
ordered by event timestamp descending (ties broken by `event_id`).

### Load the supplied seed

`starter/seed/events.json` is a day of realistic traffic including retries,
conflicts, an empty `event_id`, a missing `contact_id`, a bad timestamp, an
unexpected timestamp format, an unknown type and an uppercase type. The service
survives all of it: valid events are stored, the rest are reported per item.

```bash
curl -s -X POST localhost:8080/events \
  -H 'Content-Type: application/json' \
  --data-binary @starter/seed/events.json
```

## Run the tests

```bash
go test ./...
go test -race ./...     # duplicate/concurrent ingestion is race-tested
go vet ./...
```

Covered behaviour: identical duplicates counted once; conflicting duplicates
keeping the first payload; invalid fields/types/timestamps; mixed valid/invalid
batches; per-campaign unique opens; out-of-order events; concurrent duplicate
ingestion; stats and pagination HTTP behaviour; the supplied seed fixture.

## Debugging exercise (Part 3)

```bash
cd debugging
go run . events.jsonl > actual.txt
diff actual.txt expected_output.txt        # must be empty
go run -race . events.jsonl > /dev/null    # must print no DATA RACE
```

The four fixes are documented in `../BUGS.md`.
