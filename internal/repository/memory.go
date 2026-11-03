package repository

import (
	"sort"
	"sync"

	"campaign-events/internal/models"
)

// MemoryRepository keeps events in memory behind a RWMutex.
//
// The event_id index is the source of truth for idempotency: an insert only
// succeeds if the id is unseen. Stats are computed on read from the stored
// events, which keeps a single source of truth and avoids counter drift.
type MemoryRepository struct {
	mu         sync.RWMutex
	byID       map[string]models.Event
	byCampaign map[string][]models.Event
}

// NewMemoryRepository returns an empty, ready-to-use repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		byID:       make(map[string]models.Event),
		byCampaign: make(map[string][]models.Event),
	}
}

func (r *MemoryRepository) Insert(ev models.Event) InsertResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.byID[ev.EventID]; ok {
		return InsertResult{Stored: false, Conflict: !existing.SamePayload(ev)}
	}

	r.byID[ev.EventID] = ev
	r.byCampaign[ev.CampaignID] = append(r.byCampaign[ev.CampaignID], ev)
	return InsertResult{Stored: true}
}

func (r *MemoryRepository) Stats(campaignID string) (models.Stats, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	events, ok := r.byCampaign[campaignID]
	if !ok {
		return models.Stats{}, false
	}

	stats := models.Stats{CampaignID: campaignID}
	openers := make(map[string]struct{})
	for _, ev := range events {
		switch ev.Type {
		case "sent":
			stats.Sent++
		case "delivered":
			stats.Delivered++
		case "opened":
			stats.Opened++
			openers[ev.ContactID] = struct{}{}
		case "clicked":
			stats.Clicked++
		}
	}
	stats.UniqueOpens = len(openers)
	return stats, true
}

func (r *MemoryRepository) List(campaignID string, offset, limit int) ([]models.Event, int, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stored, ok := r.byCampaign[campaignID]
	if !ok {
		return nil, 0, false
	}

	// Copy before sorting so we never mutate storage order under a read lock.
	ordered := make([]models.Event, len(stored))
	copy(ordered, stored)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Timestamp.Equal(ordered[j].Timestamp) {
			return ordered[i].EventID < ordered[j].EventID
		}
		return ordered[i].Timestamp.After(ordered[j].Timestamp)
	})

	total := len(ordered)
	if offset >= total {
		return []models.Event{}, total, true
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return ordered[offset:end], total, true
}
