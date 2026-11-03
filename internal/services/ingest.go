package services

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"campaign-events/internal/models"
	"campaign-events/internal/repository"
)

// ItemStatus is the per-event outcome returned to the provider.
const (
	StatusAccepted  = "accepted"
	StatusDuplicate = "duplicate"
	StatusInvalid   = "invalid"
)

// ItemResult reports what happened to one element of the request array. The
// index ties it back to the provider's original payload.
type ItemResult struct {
	Index    int    `json:"index"`
	EventID  string `json:"event_id,omitempty"`
	Status   string `json:"status"`
	Conflict bool   `json:"conflict,omitempty"`
	Error    string `json:"error,omitempty"`
}

// IngestResponse summarises a batch. Providers can log the counts and inspect
// individual failures without the whole batch being rejected.
type IngestResponse struct {
	Received   int          `json:"received"`
	Accepted   int          `json:"accepted"`
	Duplicates int          `json:"duplicates"`
	Invalid    int          `json:"invalid"`
	Results    []ItemResult `json:"results"`
}

// IngestService validates provider events and writes them through the
// repository. It is stateless apart from the repository, so it is safe for
// concurrent HTTP handlers.
type IngestService struct {
	repo repository.Repository
}

// NewIngestService wires a service to a storage implementation.
func NewIngestService(repo repository.Repository) *IngestService {
	return &IngestService{repo: repo}
}

// Ingest processes a batch element by element. One malformed element never
// prevents the remaining elements from being stored.
func (s *IngestService) Ingest(rawEvents []json.RawMessage) IngestResponse {
	resp := IngestResponse{
		Received: len(rawEvents),
		Results:  make([]ItemResult, 0, len(rawEvents)),
	}

	for i, raw := range rawEvents {
		ev, err := ParseEvent(raw)
		if err != nil {
			resp.Invalid++
			resp.Results = append(resp.Results, ItemResult{
				Index:  i,
				Status: StatusInvalid,
				Error:  err.Error(),
			})
			continue
		}

		insert := s.repo.Insert(ev)
		if insert.Stored {
			resp.Accepted++
			resp.Results = append(resp.Results, ItemResult{
				Index:   i,
				EventID: ev.EventID,
				Status:  StatusAccepted,
			})
			continue
		}

		resp.Duplicates++
		result := ItemResult{
			Index:    i,
			EventID:  ev.EventID,
			Status:   StatusDuplicate,
			Conflict: insert.Conflict,
		}
		if insert.Conflict {
			result.Error = "event_id already stored with a different payload; stored version kept"
		}
		resp.Results = append(resp.Results, result)
	}

	return resp
}

// Stats proxies campaign aggregates from storage.
func (s *IngestService) Stats(campaignID string) (models.Stats, bool) {
	return s.repo.Stats(campaignID)
}

// List proxies a page of campaign events from storage.
func (s *IngestService) List(campaignID string, offset, limit int) ([]models.Event, int, bool) {
	return s.repo.List(campaignID, offset, limit)
}

// rawEvent mirrors the wire contract but keeps the timestamp as a string so a
// bad value produces a per-event validation error instead of a batch-wide
// JSON decode failure.
type rawEvent struct {
	EventID    string            `json:"event_id"`
	CampaignID string            `json:"campaign_id"`
	ContactID  string            `json:"contact_id"`
	Type       string            `json:"type"`
	Timestamp  string            `json:"timestamp"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// ParseEvent validates one raw event and returns the stored representation.
func ParseEvent(raw json.RawMessage) (models.Event, error) {
	var in rawEvent
	if err := json.Unmarshal(raw, &in); err != nil {
		return models.Event{}, fmt.Errorf("malformed JSON: %v", err)
	}

	if strings.TrimSpace(in.EventID) == "" {
		return models.Event{}, fmt.Errorf("event_id is required")
	}
	if strings.TrimSpace(in.CampaignID) == "" {
		return models.Event{}, fmt.Errorf("campaign_id is required")
	}
	if strings.TrimSpace(in.ContactID) == "" {
		return models.Event{}, fmt.Errorf("contact_id is required")
	}
	if !models.IsValidType(in.Type) {
		return models.Event{}, fmt.Errorf("invalid type %q: must be sent, delivered, opened or clicked", in.Type)
	}
	if strings.TrimSpace(in.Timestamp) == "" {
		return models.Event{}, fmt.Errorf("timestamp is required")
	}

	ts, err := time.Parse(time.RFC3339, in.Timestamp)
	if err != nil {
		return models.Event{}, fmt.Errorf("timestamp %q is not RFC3339: %v", in.Timestamp, err)
	}

	return models.Event{
		EventID:    strings.TrimSpace(in.EventID),
		CampaignID: strings.TrimSpace(in.CampaignID),
		ContactID:  strings.TrimSpace(in.ContactID),
		Type:       in.Type,
		Timestamp:  ts.UTC(),
		Metadata:   in.Metadata,
	}, nil
}
