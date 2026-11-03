package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"campaign-events/internal/services"
)

const maxBodyBytes = 10 << 20 // 10 MiB

// Handler exposes the campaign-events HTTP API.
type Handler struct {
	svc *services.IngestService
}

// New returns a handler backed by svc.
func New(svc *services.IngestService) *Handler {
	return &Handler{svc: svc}
}

// Register wires the routes onto mux (Go 1.22 method+pattern routing).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /events", h.postEvents)
	mux.HandleFunc("GET /campaigns/{campaignID}/stats", h.getStats)
	mux.HandleFunc("GET /campaigns/{campaignID}/events", h.getEvents)
}

// postEvents accepts a JSON array. Each element is validated independently, so
// a bad element is reported without discarding its neighbours.
func (h *Handler) postEvents(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var rawEvents []json.RawMessage
	if err := json.NewDecoder(body).Decode(&rawEvents); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be a JSON array of events: "+err.Error())
		return
	}

	resp := h.svc.Ingest(rawEvents)
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getStats(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("campaignID")
	if campaignID == "" {
		writeError(w, http.StatusBadRequest, "campaign_id is required")
		return
	}

	stats, ok := h.svc.Stats(campaignID)
	if !ok {
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h *Handler) getEvents(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("campaignID")
	if campaignID == "" {
		writeError(w, http.StatusBadRequest, "campaign_id is required")
		return
	}

	limit, err := intParam(r, "limit", 20)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if limit < 1 || limit > 100 {
		writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
		return
	}
	offset, err := intParam(r, "offset", 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if offset < 0 {
		writeError(w, http.StatusBadRequest, "offset must be >= 0")
		return
	}

	events, total, ok := h.svc.List(campaignID, offset, limit)
	if !ok {
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"campaign_id": campaignID,
		"total":       total,
		"limit":       limit,
		"offset":      offset,
		"events":      events,
	})
}

func intParam(r *http.Request, name string, fallback int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New(name + " must be an integer")
	}
	return v, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
