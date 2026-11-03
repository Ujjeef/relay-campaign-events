package services

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"campaign-events/internal/repository"
)

func newService() *IngestService {
	return NewIngestService(repository.NewMemoryRepository())
}

func makeRaw(id, campaign, contact, typ, ts string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(
		`{"event_id":%q,"campaign_id":%q,"contact_id":%q,"type":%q,"timestamp":%q}`,
		id, campaign, contact, typ, ts,
	))
}

func TestIngestDuplicateIsCountedOnce(t *testing.T) {
	svc := newService()
	ev := makeRaw("evt_1", "cmp_1", "ct_1", "delivered", "2026-08-10T06:15:00Z")

	resp := svc.Ingest([]json.RawMessage{ev, ev, ev})
	if resp.Received != 3 || resp.Accepted != 1 || resp.Duplicates != 2 || resp.Invalid != 0 {
		t.Fatalf("unexpected batch summary: %+v", resp)
	}

	stats, ok := svc.Stats("cmp_1")
	if !ok {
		t.Fatal("campaign should exist")
	}
	if stats.Delivered != 1 {
		t.Fatalf("delivered = %d, want 1", stats.Delivered)
	}
}

func TestIngestConflictingDuplicateKeepsFirst(t *testing.T) {
	svc := newService()
	first := makeRaw("evt_1", "cmp_1", "ct_1", "delivered", "2026-08-10T06:15:00Z")
	conflict := makeRaw("evt_1", "cmp_1", "ct_1", "opened", "2026-08-10T06:15:00Z")

	resp := svc.Ingest([]json.RawMessage{first, conflict})
	if resp.Accepted != 1 || resp.Duplicates != 1 {
		t.Fatalf("unexpected batch summary: %+v", resp)
	}
	if !resp.Results[1].Conflict {
		t.Fatalf("second result should be flagged as a conflict: %+v", resp.Results[1])
	}

	stats, _ := svc.Stats("cmp_1")
	if stats.Delivered != 1 || stats.Opened != 0 {
		t.Fatalf("first payload should win, got %+v", stats)
	}
}

func TestIngestInvalidEventsDoNotBreakTheBatch(t *testing.T) {
	svc := newService()
	valid := makeRaw("evt_ok", "cmp_1", "ct_1", "sent", "2026-08-10T06:15:00Z")

	invalid := []json.RawMessage{
		json.RawMessage(`{"campaign_id":"cmp_1","contact_id":"ct_1","type":"sent","timestamp":"2026-08-10T06:15:00Z"}`),
		json.RawMessage(`{"event_id":"evt_2","campaign_id":"","contact_id":"ct_1","type":"sent","timestamp":"2026-08-10T06:15:00Z"}`),
		json.RawMessage(`{"event_id":"evt_3","campaign_id":"cmp_1","type":"sent","timestamp":"2026-08-10T06:15:00Z"}`),
		json.RawMessage(`{"event_id":"evt_4","campaign_id":"cmp_1","contact_id":"ct_1","type":"bogus","timestamp":"2026-08-10T06:15:00Z"}`),
		json.RawMessage(`{"event_id":"evt_5","campaign_id":"cmp_1","contact_id":"ct_1","type":"OPENED","timestamp":"2026-08-10T06:15:00Z"}`),
		json.RawMessage(`{"event_id":"evt_6","campaign_id":"cmp_1","contact_id":"ct_1","type":"sent","timestamp":"not-a-time"}`),
		json.RawMessage(`{"event_id":"evt_7","campaign_id":"cmp_1","contact_id":"ct_1","type":"sent","timestamp":"10-08-2026 09:00"}`),
		json.RawMessage(`{"event_id":`),
	}

	batch := append([]json.RawMessage{valid}, invalid...)
	resp := svc.Ingest(batch)

	if resp.Received != len(batch) {
		t.Fatalf("received = %d, want %d", resp.Received, len(batch))
	}
	if resp.Accepted != 1 {
		t.Fatalf("accepted = %d, want 1", resp.Accepted)
	}
	if resp.Invalid != len(invalid) {
		t.Fatalf("invalid = %d, want %d", resp.Invalid, len(invalid))
	}

	stats, _ := svc.Stats("cmp_1")
	if stats.Sent != 1 {
		t.Fatalf("sent = %d, want 1", stats.Sent)
	}
}

func TestUniqueOpensArePerCampaign(t *testing.T) {
	svc := newService()
	batch := []json.RawMessage{
		makeRaw("evt_1", "cmp_a", "ct_1", "opened", "2026-08-10T06:00:00Z"),
		makeRaw("evt_2", "cmp_a", "ct_1", "opened", "2026-08-10T07:00:00Z"),
		makeRaw("evt_3", "cmp_a", "ct_2", "opened", "2026-08-10T08:00:00Z"),
		makeRaw("evt_4", "cmp_b", "ct_1", "opened", "2026-08-10T09:00:00Z"),
	}
	svc.Ingest(batch)

	a, _ := svc.Stats("cmp_a")
	if a.Opened != 3 || a.UniqueOpens != 2 {
		t.Fatalf("cmp_a opened=%d unique=%d, want 3/2", a.Opened, a.UniqueOpens)
	}
	b, _ := svc.Stats("cmp_b")
	if b.Opened != 1 || b.UniqueOpens != 1 {
		t.Fatalf("cmp_b opened=%d unique=%d, want 1/1", b.Opened, b.UniqueOpens)
	}
}

func TestOutOfOrderEventsCountCorrectly(t *testing.T) {
	svc := newService()
	batch := []json.RawMessage{
		makeRaw("evt_open", "cmp_1", "ct_1", "opened", "2026-08-10T09:00:00Z"),
		makeRaw("evt_sent", "cmp_1", "ct_1", "sent", "2026-08-10T06:00:00Z"),
		makeRaw("evt_deliv", "cmp_1", "ct_1", "delivered", "2026-08-10T07:00:00Z"),
	}
	svc.Ingest(batch)

	stats, _ := svc.Stats("cmp_1")
	if stats.Sent != 1 || stats.Delivered != 1 || stats.Opened != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestConcurrentDuplicateIngestion(t *testing.T) {
	svc := newService()

	const events = 100
	const goroutines = 10

	batch := make([]json.RawMessage, 0, events)
	for i := 0; i < events; i++ {
		batch = append(batch, makeRaw(
			fmt.Sprintf("evt_%d", i), "cmp_1", "ct_1", "sent", "2026-08-10T06:00:00Z",
		))
	}

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.Ingest(batch)
		}()
	}
	wg.Wait()

	stats, _ := svc.Stats("cmp_1")
	if stats.Sent != events {
		t.Fatalf("sent = %d, want %d (duplicates must not double count under concurrency)", stats.Sent, events)
	}
}
