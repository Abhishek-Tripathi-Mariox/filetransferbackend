package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/filetransfer/backend/internal/ws"
)

type StatusHandler struct {
	wsHandler *ws.Handler
}

func NewStatusHandler(wsHandler *ws.Handler) *StatusHandler {
	return &StatusHandler{wsHandler: wsHandler}
}

// GetOnlineStatus handles POST /api/users/online
// Body: { "user_ids": ["uuid1", "uuid2"] }
// Returns: { "online": { "uuid1": true, "uuid2": false } }
func (h *StatusHandler) GetOnlineStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserIDs []string `json:"user_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if len(req.UserIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_ids is required"})
		return
	}

	status := h.wsHandler.GetOnlineUsers(req.UserIDs)

	writeJSON(w, http.StatusOK, map[string]any{
		"online": status,
	})
}
