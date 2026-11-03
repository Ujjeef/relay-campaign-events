package models

import "time"

// Event is one provider webhook event. It is stored only after it has passed
// validation, so every string is non-empty and every timestamp is UTC.
type Event struct {
	EventID    string            `json:"event_id"`
	CampaignID string            `json:"campaign_id"`
	ContactID  string            `json:"contact_id"`
	Type       string            `json:"type"`
	Timestamp  time.Time         `json:"timestamp"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// ValidTypes is the closed set of event types defined by the provider contract.
var ValidTypes = map[string]struct{}{
	"sent":      {},
	"delivered": {},
	"opened":    {},
	"clicked":   {},
}

// IsValidType reports whether t is one of the four contract event types.
func IsValidType(t string) bool {
	_, ok := ValidTypes[t]
	return ok
}

// SamePayload reports whether two events describe the same thing. It is used
// to tell an identical retry apart from a conflicting reuse of an event_id.
func (e Event) SamePayload(other Event) bool {
	if e.EventID != other.EventID ||
		e.CampaignID != other.CampaignID ||
		e.ContactID != other.ContactID ||
		e.Type != other.Type ||
		!e.Timestamp.Equal(other.Timestamp) {
		return false
	}
	if len(e.Metadata) != len(other.Metadata) {
		return false
	}
	for k, v := range e.Metadata {
		if other.Metadata[k] != v {
			return false
		}
	}
	return true
}

// Stats is the per-campaign aggregate served to the dashboard.
//
// Opened is the number of distinct "opened" events (events). UniqueOpens is
// the number of distinct contacts that opened (people). A dashboard needs both:
// the first is activity volume, the second is audience reach.
type Stats struct {
	CampaignID  string `json:"campaign_id"`
	Sent        int    `json:"sent"`
	Delivered   int    `json:"delivered"`
	Opened      int    `json:"opened"`
	Clicked     int    `json:"clicked"`
	UniqueOpens int    `json:"unique_opens"`
}
