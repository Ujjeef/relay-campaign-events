package repository

import "campaign-events/internal/models"

// InsertResult describes what happened when an event was offered to storage.
type InsertResult struct {
	// Stored is true when this call was the first to persist the event_id.
	Stored bool
	// Conflict is true for a duplicate event_id whose payload differs from the
	// stored version. The stored version is never overwritten.
	Conflict bool
}

// Repository is the storage boundary. The in-memory implementation is used
// here; swapping in SQLite only requires implementing this interface.
type Repository interface {
	// Insert stores ev exactly once. Concurrency-safe and atomic per event_id.
	Insert(ev models.Event) InsertResult
	// Stats returns aggregates for a campaign and whether the campaign exists.
	Stats(campaignID string) (models.Stats, bool)
	// List returns a page of events for a campaign, newest event time first.
	// found is false when the campaign has no stored events.
	List(campaignID string, offset, limit int) (events []models.Event, total int, found bool)
}
