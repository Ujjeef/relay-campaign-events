# BUGS.md — debugging exercise

`debugging/main.go` compiles and produces plausible output but violates the
four rules in `debugging/README.md`. I made four minimal changes; the rest of
the program is untouched. Final verification:

```bash
cd debugging
go run . events.jsonl > actual.txt
diff actual.txt expected_output.txt        # empty
go run -race . events.jsonl > /dev/null    # no race report
```

All four fixes together produce `expected_output.txt` exactly and
deterministically (verified again with three consecutive runs and a `-race`
run).

---

## Bug 1 — De-duplication only spans one batch

**What the bug is:** `seen` (the set of processed `event_id`s) was declared
*inside* `processBatch`, so it was reset every 200 events. A duplicate of an
event from an earlier batch was counted again.

**Why it happens:** the requirement is "each `event_id` at most once across the
whole file", but the map's lifetime was one batch. This is the classic
scope-of-a-cache bug: the data structure was correct, its lifetime was not.

**Fix:** move the set to package scope so it lives for the whole run. It is only
touched from `processBatch`, which runs on the main goroutine, so no locking is
needed.

```go
// package level
var seen = map[string]bool{}

// processBatch: delete the per-batch declaration
// seen := make(map[string]bool)
```

**How I verified:** the original output over-counted every type
(`cmp_A sent=2024`, expected `1987`; `delivered=1963`, expected `1914`). After
the fix the totals match `expected_output.txt`.

---

## Bug 2 — Unique opens keyed only by contact, not campaign

**What the bug is:** `openedBy` used `ev.ContactID` as its key. A contact who
opened in `cmp_A` was then considered "already counted" in `cmp_B`, so the
second campaign's `unique_opens` was too low.

**Why it happens:** the spec says "the same contact opening in two campaigns
counts once *in each*". A contact is only unique within a campaign; the map
needed a composite `(campaign, contact)` key.

**Fix:** build a composite key with a separator that cannot appear in IDs.

```go
key := ev.CampaignID + "\x00" + ev.ContactID
if !openedBy[key] {
    openedBy[key] = true
    cs.UniqueOpens++
}
```

**How I verified:** before the fix `cmp_A unique_opens=549` (expected `942`),
because almost every contact had been "claimed" by the first campaign seen.
After the fix every campaign matches, e.g. `cmp_A unique_opens=942`,
`cmp_B=833`, `cmp_E=380`.

---

## Bug 3 — Daily delivered buckets use local time instead of UTC

**What the bug is:** `ev.Timestamp.Local().Format("2006-01-02")` buckets by the
machine's time zone. The README requires the event's **UTC** date.

**Why it happens:** `time.Time` carries a location. Parsing `...Z` yields UTC,
but `.Local()` converts it. On a machine at UTC+5:30 (IST), events after
18:30 UTC roll into the next calendar day.

**Fix:** use `.UTC()`.

```go
day := ev.Timestamp.UTC().Format("2006-01-02")
```

**How I verified:** the original output contained an extra
`2026-08-08 delivered=...` bucket for every campaign and the 08-01..07 numbers
were shuffled between days. After the fix there are exactly the seven UTC days
in `expected_output.txt`. This bug is invisible on a machine whose local zone is
UTC, which is why it may "not show up" locally.

---

## Bug 4 — Data race on shared counters

**What the bug is:** `apply` increments plain `int` fields of the shared
`*CampaignStats` from eight goroutines with no synchronisation.

**Why it happens:** `++` is a read-modify-write. Two workers can read the same
value and both write `value+1`, losing an update. Go's race detector reports it
as a data race; the visible totals can also be wrong. It is the bug that may not
change the numbers on a given run, but is still a real bug.

**Fix:** guard the counter updates with a mutex. `apply` is the only writer, so
one mutex is sufficient and keeps the change minimal.

```go
var statsMu sync.Mutex

func apply(ev Event) {
    statsMu.Lock()
    defer statsMu.Unlock()
    cs := stats[ev.CampaignID]
    // ...switch as before...
}
```

**How I verified:** before the fix `go run -race . events.jsonl` printed
`WARNING: DATA RACE` in `main.apply` (read vs previous write by another
goroutine). After the fix the race detector is silent and the output still
matches `expected_output.txt` exactly.

---

## Note on scope

I did not restructure the program: no new files, no changes to the worker-pool
design, no renames beyond the four fixes. The only other change is `gofmt`
normalising the doc-comment indentation.
