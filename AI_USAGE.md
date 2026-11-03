# AI usage disclosure

## Tools used
- An AI coding assistant (opencode, `big-pickle` model) for scaffolding,
  implementation, tests, and documentation review.
- Go's standard toolchain (`gofmt`, `go vet`, `go test`, `go test -race`) to
  verify everything the assistant produced.

## What I used it for
- Drafting the package layout (`models`, `repository`, `services`, `handlers`)
  and the HTTP wiring.
- Turning the design decisions I had already made into Go code: per-item
  `[]json.RawMessage` decoding, `event_id`-keyed idempotent insert behind a
  `sync.RWMutex`, and both `opened` and `unique_opens` in stats.
- Generating first drafts of the tests (duplicates, mixed batches, concurrency,
  pagination) and the written sections, which I then reviewed and adjusted.
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
