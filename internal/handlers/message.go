package handlers

import (
	"encoding/json"
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

// GetUsers handles GET /api/users — users the caller has no chat with yet, for the
// "new chat" picker.
func (h *MessageHandler) GetUsers(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())

	users, err := h.msgSvc.ListUsers(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch users"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"users": users,
	})
}

// CreateRoom handles POST /api/rooms — body { "user_id": "<uuid>" }.
// Returns the existing room with that user, or creates it.
func (h *MessageHandler) CreateRoom(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())

	var req struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id is required"})
		return
	}
	if req.UserID == userID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot start a chat with yourself"})
		return
	}

	other, err := h.msgSvc.GetUserByID(r.Context(), req.UserID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	me, err := h.msgSvc.GetUserByID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load current user"})
		return
	}

	room, err := h.msgSvc.GetOrCreateRoom(r.Context(), userID, req.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create room"})
		return
	}

	// Fill usernames to match the GET /api/rooms shape
	if room.User1ID == me.ID {
		room.User1Username, room.User2Username = me.Username, other.Username
	} else {
		room.User1Username, room.User2Username = other.Username, me.Username
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"room": room,
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
