package handlers

import (
	"net/http"
	"strconv"

	"github.com/filetransfer/backend/internal/middleware"
	"github.com/filetransfer/backend/internal/services"
)

type MessageHandler struct {
	msgSvc  *services.MessageService
	fileSvc *services.FileService
}

func NewMessageHandler(msgSvc *services.MessageService, fileSvc *services.FileService) *MessageHandler {
	return &MessageHandler{msgSvc: msgSvc, fileSvc: fileSvc}
}

// GetMessages handles GET /api/messages/{roomID}?limit=50&cursor=<message_id>
func (h *MessageHandler) GetMessages(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())
	roomID := r.PathValue("roomID")
	if roomID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "room_id is required"})
		return
	}

	// Verify user belongs to this room
	rooms, err := h.msgSvc.GetRoomsForUser(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify room access"})
		return
	}
	authorized := false
	for _, room := range rooms {
		if room.ID == roomID {
			authorized = true
			break
		}
	}
	if !authorized {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a member of this room"})
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	cursor := r.URL.Query().Get("cursor")

	messages, err := h.msgSvc.GetMessagesByRoom(r.Context(), roomID, limit, cursor)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch messages"})
		return
	}

	var nextCursor string
	if len(messages) == limit {
		nextCursor = messages[len(messages)-1].ID
	}

	enriched := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		m := msg
		item := h.msgSvc.BuildMessagePayload(r.Context(), &m)

		if msg.MessageType == "file" && h.fileSvc != nil {
			if downloaded, ok := item["downloaded"].(bool); ok && !downloaded {
				if s3Key, ok := item["s3_key"].(string); ok && s3Key != "" {
					if url, urlErr := h.fileSvc.GetPresignedDownloadURL(r.Context(), s3Key); urlErr == nil {
						item["encrypted_s3_url"] = url
					}
				}
			}
		}

		enriched = append(enriched, item)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"messages":    enriched,
		"next_cursor": nextCursor,
	})
}

// GetRooms handles GET /api/rooms — returns rooms with unread counts and last message
func (h *MessageHandler) GetRooms(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())

	rooms, err := h.msgSvc.GetRoomsWithUnread(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch rooms"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"rooms": rooms,
	})
}

// GetUnreadCounts handles GET /api/messages/unread — returns unread counts per room for badges
func (h *MessageHandler) GetUnreadCounts(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())

	counts, err := h.msgSvc.GetUnreadCountsForUser(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch unread counts"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"unread_counts": counts,
	})
}
