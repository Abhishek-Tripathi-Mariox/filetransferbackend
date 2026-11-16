package handlers

import (
	"net/http"

	"github.com/filetransfer/backend/internal/models"
)

// ConfigHandler serves client limits (spec: limits are server-configurable).
type ConfigHandler struct {
	cfg models.ClientConfig
}

func NewConfigHandler(cfg models.ClientConfig) *ConfigHandler {
	return &ConfigHandler{cfg: cfg}
}

// GET /api/config
func (h *ConfigHandler) Get(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.cfg)
}
