package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"campaign-events/internal/repository"
	"campaign-events/internal/services"
)

func newTestServer() *httptest.Server {
	svc := services.NewIngestService(repository.NewMemoryRepository())
	mux := http.NewServeMux()
	New(svc).Register(mux)
	return httptest.NewServer(mux)
}

func postEvents(t *testing.T, srv *httptest.Server, body string) (*http.Response, services.IngestResponse) {
	t.Helper()
	resp, err := http.Post(srv.URL+"/events", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var out services.IngestResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp, out
}

func TestPostEventsMixedBatchThenStats(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	body := `[
		{"event_id":"evt_1","campaign_id":"cmp_1","contact_id":"ct_1","type":"sent","timestamp":"2026-08-10T06:00:00Z"},
		{"event_id":"evt_1","campaign_id":"cmp_1","contact_id":"ct_1","type":"sent","timestamp":"2026-08-10T06:00:00Z"},
		{"event_id":"evt_2","campaign_id":"cmp_1","contact_id":"ct_1","type":"delivered","timestamp":"2026-08-10T06:05:00Z"},
		{"event_id":"evt_3","campaign_id":"cmp_1","contact_id":"ct_1","type":"opened","timestamp":"2026-08-10T06:10:00Z"},
		{"event_id":"evt_4","campaign_id":"cmp_1","contact_id":"ct_1","type":"clicked","timestamp":"2026-08-10T06:15:00Z"},
		{"event_id":"","campaign_id":"cmp_1","contact_id":"ct_1","type":"clicked","timestamp":"2026-08-10T06:15:00Z"}
	]`

	resp, summary := postEvents(t, srv, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if summary.Accepted != 4 || summary.Duplicates != 1 || summary.Invalid != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	statsResp, err := http.Get(srv.URL + "/campaigns/cmp_1/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer statsResp.Body.Close()

	var stats struct {
		Sent      int `json:"sent"`
		Delivered int `json:"delivered"`
		Opened    int `json:"opened"`
		Clicked   int `json:"clicked"`
	}
	if err := json.NewDecoder(statsResp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if stats.Sent != 1 || stats.Delivered != 1 || stats.Opened != 1 || stats.Clicked != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestGetEventsPaginationNewestFirst(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	body := `[
		{"event_id":"evt_1","campaign_id":"cmp_1","contact_id":"ct_1","type":"sent","timestamp":"2026-08-10T06:00:00Z"},
		{"event_id":"evt_2","campaign_id":"cmp_1","contact_id":"ct_1","type":"delivered","timestamp":"2026-08-10T07:00:00Z"},
		{"event_id":"evt_3","campaign_id":"cmp_1","contact_id":"ct_1","type":"opened","timestamp":"2026-08-10T08:00:00Z"}
	]`
	if resp, _ := postEvents(t, srv, body); resp.StatusCode != http.StatusOK {
		t.Fatalf("seed status = %d", resp.StatusCode)
	}

	resp, err := http.Get(srv.URL + "/campaigns/cmp_1/events?limit=2&offset=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var page struct {
		Total  int `json:"total"`
		Events []struct {
			EventID string `json:"event_id"`
		} `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Events) != 2 {
		t.Fatalf("unexpected page: %+v", page)
	}
	if page.Events[0].EventID != "evt_3" || page.Events[1].EventID != "evt_2" {
		t.Fatalf("events not newest-first: %+v", page.Events)
	}
}

func TestErrorResponses(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{"malformed body", http.MethodPost, "/events", `{"event_id":"x"}`, http.StatusBadRequest},
		{"non-array body", http.MethodPost, "/events", `"nope"`, http.StatusBadRequest},
		{"unknown campaign stats", http.MethodGet, "/campaigns/nope/stats", "", http.StatusNotFound},
		{"unknown campaign events", http.MethodGet, "/campaigns/nope/events", "", http.StatusNotFound},
		{"bad limit", http.MethodGet, "/campaigns/nope/events?limit=abc", "", http.StatusBadRequest},
		{"limit too high", http.MethodGet, "/campaigns/nope/events?limit=101", "", http.StatusBadRequest},
		{"negative offset", http.MethodGet, "/campaigns/nope/events?offset=-1", "", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var body *bytes.Buffer
			if tc.body == "" {
				body = bytes.NewBuffer(nil)
			} else {
				body = bytes.NewBufferString(tc.body)
			}
			req, err := http.NewRequest(tc.method, srv.URL+tc.path, body)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
		})
	}
}

func TestSeedFixtureLoads(t *testing.T) {
	// The fixture is shipped one directory above the module; this test verifies
	// the whole realistic mess is handled without a batch-wide failure.
	srv := newTestServer()
	defer srv.Close()

	seedBytes, err := os.ReadFile("../../starter/seed/events.json")
	if err != nil {
		t.Skipf("seed fixture not available: %v", err)
	}
	seed := string(seedBytes)

	resp, summary := postEvents(t, srv, seed)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if summary.Received != summary.Accepted+summary.Duplicates+summary.Invalid {
		t.Fatalf("counts do not add up: %+v", summary)
	}
	if summary.Accepted == 0 {
		t.Fatalf("expected some valid events in fixture: %+v", summary)
	}
}
