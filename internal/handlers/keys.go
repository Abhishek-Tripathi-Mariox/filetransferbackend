package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/filetransfer/backend/internal/middleware"
	"github.com/filetransfer/backend/internal/models"
	"github.com/filetransfer/backend/internal/services"
)

type KeyHandler struct {
	keySvc *services.KeyService
}

func NewKeyHandler(keySvc *services.KeyService) *KeyHandler {
	return &KeyHandler{keySvc: keySvc}
}

// UploadKeys handles POST /api/keys/upload
func (h *KeyHandler) UploadKeys(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())

	var req models.UploadKeysRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	stored, err := h.keySvc.UploadKeys(r.Context(), userID, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"stored": stored,
	})
}

// GetKeyBundle handles GET /api/keys/bundle/{userID}
func (h *KeyHandler) GetKeyBundle(w http.ResponseWriter, r *http.Request) {
	targetUserID := r.PathValue("userID")
	if targetUserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id is required"})
		return
	}

	bundle, err := h.keySvc.GetKeyBundle(r.Context(), targetUserID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, bundle)
}

// GetKeyCounts handles GET /api/keys/count
func (h *KeyHandler) GetKeyCounts(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())

	oneTime, kyber, err := h.keySvc.GetKeyCounts(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get key counts"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]int{
		"one_time_prekeys": oneTime,
		"kyber_prekeys":    kyber,
	})
}
