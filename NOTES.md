# Notes — Campaign Events

## Part 1 — Read and think first

### 1. My interpretation of the problem
Relay receives webhook events from delivery providers about messages it has
handed off. Each event belongs to a campaign and a contact and says that
something happened (`sent`, `delivered`, `opened`, `clicked`). Relay must keep
one durable record per real-world event and serve per-campaign counts to a
dashboard.

The hard part is not counting. The hard part is counting *once* when:

- providers retry the same webhook (same `event_id`, possibly many times),
- events arrive late and out of order,
- providers sometimes send malformed or unknown data,
- several webhook requests may be processed at the same time.

"Correct" means the dashboard's numbers are derived from distinct events, not
from the number of HTTP requests we happened to receive.

### 2. Assumptions I am making
- `event_id` is assigned by the provider and is globally unique for one event.
  It is the only identity we can trust for idempotency.
- A retry is byte-for-byte equivalent or logically equivalent. If the same
  `event_id` arrives with a different payload, one of the two is a provider
  bug; the first stored version wins and the later one is reported as a
  conflicting duplicate.
- Valid event types are exactly the four lowercase strings in the contract.
  I treat `OPENED`, `spam_report`, etc. as invalid rather than silently
  normalising them, because hiding provider bugs makes the numbers untrustworthy.
- `timestamp` is RFC3339 and is the provider's event time, not our arrival
  time. I store it in UTC and never let arrival order affect counts.
- Counters are cumulative for the life of the process. The dashboard wants
  "how many so far", not "how many in the last five minutes".
- A campaign exists once it has at least one stored valid event.
- Losing in-memory data on restart is acceptable for this exercise (the brief
  allows in-memory or SQLite). The storage boundary is an interface, so a
  durable implementation can be added without changing HTTP handlers.

### 3. Ambiguities I noticed
- What exactly makes two events "the same"? Only `event_id`? Or
  `event_id` + `campaign_id`? What if a provider reuses an id with a different
  payload?
- Is "opened" the number of open events, or the number of distinct contacts
  who opened? Marketers often mean both, so I return both.
- Should the API reject a whole batch when one element is bad, or accept the
  good elements and report the bad ones?
- Should a duplicate return a non-2xx status? If yes, providers will retry
  forever, which is worse.
- Are events ever corrected or deleted? The brief only describes appends.
- How far in the future/past is an event still plausible? Is there a retention
  window?
- Do we need persistence across restarts? The brief explicitly leaves it open.
- Is "campaign stats" global or time-windowed? The dashboard shows live totals.

### 4. Questions I would ask the PM
1. For duplicates, should the first version win, or the latest version? (I chose
   first-wins and report conflicts.)
2. What is the retention period for raw events, and must stats survive restarts?
3. Does the dashboard want event counts or unique-contact counts for opens and
   clicks? (I expose both for opens.)
4. What should the provider see for a partially invalid batch? Is a 200 with a
   per-item report acceptable, or do they expect 4xx?
5. Are campaigns ever renamed or deleted? Is there a campaign catalogue to
   validate `campaign_id` against?
6. What are the latency requirements for the stats endpoint during a live send?
7. How should `metadata` be used? Do we need to index URLs for click tracking?

### 5. What I will implement first, and why
1. Event model + validation + per-item batch decoding (the contract and the
   "one bad element must not kill 199" trap).
2. Idempotent storage keyed by `event_id`, with concurrency safety.
3. Stats endpoint, with both `opened` and `unique_opens`.
4. Paginated event history (optional, but cheap and useful).
5. Tests for duplicates, invalid/mixed batches, concurrency, and pagination.
6. Then the debugging exercise and the written parts.

I prioritise idempotency and validation over UI, persistence, and scale
features. If I lose the numbers, nothing else matters.

### Design decisions (the questions the assignment says it will pay attention to)
- **Same event:** same `event_id`. This is the provider's idempotency key.
- **Duplicate response:** HTTP 200 with status `duplicate` for that element.
  Identical retries are silent; conflicting payloads are flagged with
  `conflict: true` and an explanation, but the original is never overwritten.
  Returning an error would cause providers to retry a permanent condition.
- **Invalid event / mixed batch:** decode the array as `[]json.RawMessage`, then
  validate each element. Good elements are stored; bad elements are reported
  with their array index and a reason. The batch returns 200. Only a malformed
  request body (not a JSON array) returns 400.
- **"How many opened":** `opened` counts distinct open events. `unique_opens`
  counts distinct contacts with at least one open in that campaign. A contact
  opening twice counts twice in `opened`, once in `unique_opens`; the same
  contact opening in two campaigns counts once in each campaign's
  `unique_opens`.
- **Storage:** in-memory maps behind a `Repository` interface. One map is the
  `event_id` index (idempotency); another keeps events per campaign. Stats are
  derived from stored events so there is a single source of truth and counters
  cannot drift. Trade-off: no persistence across restarts.
- **Concurrency:** a `sync.RWMutex` makes "check id then insert" atomic. Stats
  reads use a read lock. Tests exercise concurrent duplicate batches and run
  under `-race`.
- **Late/out-of-order:** ingestion never depends on order; timestamps are stored
  in UTC and only used for display/pagination sorting. Stats are set-based, so
  order cannot change totals.

## Part 4 — Scale memo: from a few thousand/day to 100 million/day

**What breaks first, specifically:** the in-memory design. Every event is kept
in RAM forever (100M/day is many GB/day), and stats scan every event for a
campaign on each read (O(n) per request). The single `RWMutex` serialises all
writes, so ingest throughput collapses under one process. Restarting loses
everything. The HTTP server itself is not the first bottleneck; storage and
locking are.

**What I would change, in order:**
1. Durable storage (Postgres) with a `UNIQUE` constraint on `event_id` and an
   index on `(campaign_id, timestamp)`.
2. Precomputed campaign counters updated in the same transaction as the event
   insert, so stats reads are O(1) instead of scanning.
3. A queue between the webhook and the database so a traffic spike does not
   block webhook responses.
4. Partition/shard events by `campaign_id` (or time) once a single database
   cannot keep up; separate hot counters from cold event history.
5. Cache stats per campaign with a short TTL or push updates to the dashboard.

**Would the API contract change?** Mostly no. Optional additions: a cursor
instead of offset pagination, an idempotency header for whole requests, and an
asynchronous acceptance response (`202`) if processing moves behind a queue.
The event payload itself stays fixed.

**Would storage design change?** Yes: from process memory to a durable,
indexed store with a unique key. Counters become derived state that can be
rebuilt from the immutable event log; the event log is the source of truth.

**Would I introduce a queue?** Yes, at this volume. It absorbs bursts, decouples
webhook latency from database latency, and enables retries/backpressure.
New problems: at-least-once delivery means the consumer sees duplicates; a
consumer can crash after writing to the DB but before acknowledging, producing
a replay; ordering is no longer guaranteed; a poison message needs a dead-letter
queue; queue lag becomes a user-visible freshness problem; and operating the
queue adds cost and complexity.

**Where can duplicates now sneak in that couldn't before?** Provider retries
still hit the webhook, the queue may deliver the same message twice, a consumer
may retry after a timeout that actually succeeded, rebalances/replays can
re-process a partition, and compensating jobs can re-insert events. The
database unique constraint on `event_id` is the final guard; the consumer is
also made idempotent.

**How to keep counters trustworthy:** treat the event table as the source of
truth and counters as a cache. Update events and counters in one transaction,
use idempotent upserts keyed by `event_id`, never do blind `count = count + 1`
without the uniqueness guard, and run periodic reconciliation jobs that
recompute counters from the event log and alert on drift. Make counters
monotonic where the product expects them to be, and expose corrections
explicitly rather than through silent decreases.

**What to monitor:** webhook request rate, latency and 5xx/4xx rates; batch
invalid/duplicate rates; queue depth and consumer lag; database write latency,
deadlocks and unique-constraint conflict counts; counter-vs-event-log drift;
stats endpoint latency and cache hit rate; memory/CPU; and freshness (how
old the newest event reflected in stats is). Alert on drift and lag, not just
on process liveness.

**What I would deliberately not solve yet:** exactly-once delivery, globally
ordered events, multi-region active-active, real-time streaming analytics,
custom retention/purge policies, auth, and dashboard UI. I would first make
the correctness and durability story solid, then optimise.

## Part 5 — The angry marketer

At 10:00: Sent 1,000,000, Delivered 970,000, Opened 250,000, Clicked 20,000.
At 10:30: Sent 1,000,000, Delivered 975,000, Opened 248,000, Clicked 20,000.

**Plausible explanations (before assuming the code is broken):**
1. **Late deliveries arriving.** Providers report delivery hours late. Delivered
   rising from 970k to 975k is expected in a live campaign.
2. **Opened is windowed, not cumulative.** If the dashboard shows "opens in the
   last 30 days" or "today", an old open can age out while new ones arrive,
   making the total fall.
3. **Opened is unique contacts, and identity/attribution was de-duplicated.**
   A dedupe or bot-filtering correction can legitimately reduce unique opens.
4. **Read from a stale/eventually consistent replica.** The two snapshots may
   come from different replicas with different lag.
5. **A data correction or reprocessing job** removed previously counted opens
   (invalid events, test traffic, provider retraction).
6. **A deploy/config change** altered attribution rules, filters, or the
   definition of "opened" between 10:00 and 10:30.
7. **Time-zone/window boundary** moved events in or out of the reporting period.
8. **A genuine bug:** counter reset, integer overflow, bad aggregation,
   cache invalidation that drops opens, or a race in a derived counter.

**What I would check first:** whether each metric is cumulative or windowed,
and then compare the dashboard numbers against the raw event log for that
campaign (count distinct `event_id`s by type, and distinct openers). That
single comparison tells me whether the source data changed or only the display
did.

**How I would decide bug vs expected:** recompute stats from the immutable
event log at both times. If the event log genuinely gained deliveries and the
open event set shrank because of a defined correction/window, it is expected.
If the event log is unchanged or grew while the displayed opens fell, it is a
bug in derivation, caching, or replication. I would also check deploy history
and replica lag around 10:00–10:30.

**Is rising delivered suspicious?** Not inherently. Delivery reports commonly
arrive late, so a cumulative delivered count *should* rise during a live send.
The suspicious metric is the falling opened count, because a cumulative count
of opens should be monotonic. That asymmetry is the clue: deliveries are
behaving normally while opens are not.

## What I completed / what I intentionally skipped

**Completed:** Part 1 notes; full Go service (`POST /events`,
`GET /campaigns/{id}/stats`, optional `GET /campaigns/{id}/events`); per-item
validation and mixed-batch handling; global idempotency with conflict
reporting; concurrency safety; unit/integration tests including concurrent
ingestion and the supplied seed; all four debugging fixes verified against
`expected_output.txt` (also under `-race`); Parts 4 and 5; `BUGS.md`,
`AI_USAGE.md`, `README.md`.

**Intentionally skipped / limited:** durable persistence (in-memory behind a
repository interface); authentication and rate limiting; campaign catalogue
validation; metrics/tracing; a frontend; and any deployment setup. These were
out of scope for the time box and are covered as future work rather than
half-built.

## If I had another day

- Add a SQLite repository implementing the same interface, with `event_id` as
  a `UNIQUE` column, and a `-store=sqlite` flag.
- Add cursor-based pagination and filtering by type/contact.
- Add request-level idempotency for whole batches and a maximum batch size.
- Add structured logging, metrics, and a reconciliation job.
- Add property-based/fuzz tests for the decoder and a load test for the ingest
  path.
- Revisit whether conflicting duplicate payloads should be quarantined for
  provider follow-up instead of just logged in the response.
