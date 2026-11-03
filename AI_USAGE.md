# AI usage disclosure

## Tools used
- opencode, used as an AI coding assistant.
- Go's standard toolchain (`gofmt`, `go vet`, `go test`, `go test -race`) to
  verify the result.

## My contribution
- I defined the problem in my own words, the assumptions, and the priorities for
  the time available (see `NOTES.md`).
- I made the design decisions the code implements: `event_id` as the
  idempotency key, first-wins handling of conflicting duplicates, per-item
  reporting for invalid events, returning both `opened` and `unique_opens`,
  in-memory storage behind a repository interface, and a mutex around the
  check-and-insert.
- I reviewed the implementation file by file (entry point, model, repository,
  service, handlers) and confirmed how duplicate prevention, validation,
  concurrency, pagination, and the stats calculation work.
- I confirmed the verification results: `go vet`, `go test`, `go test -race`,
  the seed-file smoke test, and the debugging output matching
  `expected_output.txt`.
- I chose the submission and repository settings.

## What I used the AI assistant for
- Drafting the package layout (`models`, `repository`, `services`, `handlers`)
  and the HTTP wiring.
- Turning the design decisions above into Go code.
- Generating first drafts of the tests (duplicates, mixed batches, concurrency,
  pagination) and the written sections, which I reviewed.
- Explaining the four bugs in the supplied debugging program and checking the
  minimal fixes.

## One suggestion I rejected or changed
The assistant initially suggested decoding the request body straight into a
`[]Event`. I rejected that: one malformed element would fail the entire decode
and discard the other events in the batch, which is exactly the trap the
assignment points out. I kept the `[]json.RawMessage` approach so each element
is validated independently.

I also changed the storage approach it first proposed (incrementing counters on
write) to deriving stats from stored events. Fewer moving parts, one source of
truth, and no counter drift.

## One thing AI helped me understand
The distinction between *event identity* and *payload identity* for idempotency:
`event_id` is the dedupe key, but a duplicate with a different payload is a
provider bug that should be surfaced (first-wins + `conflict: true`) rather than
silently ignored or overwritten.

## AI-generated code I had to debug
- A test helper named `rawEvent` collided with the existing `rawEvent` type in
  the services package; `go vet` caught the redeclaration and I renamed the
  helper.
- An early handler test used an unnecessary `io`/`fmt` indirection and imports
  that did not compile cleanly; I removed it and used `os.ReadFile` directly.
- The assistant's first debugging-analysis sketch targeted the wrong line for
  the UTC bug; running the program on a UTC+5:30 machine exposed the real
  behaviour (an extra `2026-08-08` bucket) and confirmed the `.UTC()` fix.

All generated code was run through `gofmt`, `go vet`, `go test` and
`go test -race`, and the debugging output was diffed against
`expected_output.txt`; nothing was accepted on the assistant's word alone.
