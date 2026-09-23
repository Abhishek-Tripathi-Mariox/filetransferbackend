package ws

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/filetransfer/backend/internal/models"
	"github.com/filetransfer/backend/internal/services"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Configure properly in production
	},
}

type Handler struct {
	hub        *Hub
	authSvc    *services.AuthService
	msgSvc     *services.MessageService
	fileSvc    *services.FileService
}

func NewHandler(hub *Hub, authSvc *services.AuthService, msgSvc *services.MessageService, fileSvc *services.FileService) *Handler {
	return &Handler{
		hub:     hub,
		authSvc: authSvc,
		msgSvc:  msgSvc,
		fileSvc: fileSvc,
	}
}

// IsUserOnline checks if a user has an active WebSocket connection.
func (h *Handler) IsUserOnline(userID string) bool {
	return h.hub.IsOnline(userID)
}

// GetOnlineUsers returns online status for a list of user IDs.
func (h *Handler) GetOnlineUsers(userIDs []string) map[string]bool {
	result := make(map[string]bool, len(userIDs))
	for _, uid := range userIDs {
		result[uid] = h.hub.IsOnline(uid)
	}
	return result
}

// ServeWS upgrades HTTP to WebSocket. Auth via query param: ?token=<jwt>
func (h *Handler) ServeWS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}

	userID, err := h.authSvc.ValidateToken(token)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}

	client := &Client{
		UserID: userID,
		Conn:   conn,
		Hub:    h.hub,
		Send:   make(chan []byte, 256),
	}

	h.hub.Register(client)
	go client.WritePump()
	go h.readPump(client)

	// Deliver pending messages (offline queue)
	h.deliverPendingMessages(client)
}

func (h *Handler) readPump(client *Client) {
	defer func() {
		h.hub.Unregister(client)
		client.Conn.Close()
	}()

	for {
		_, rawMsg, err := client.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("WebSocket read error for user %s: %v", client.UserID, err)
			}
			return
		}

		var wsMsg models.WSMessage
		if err := json.Unmarshal(rawMsg, &wsMsg); err != nil {
			log.Printf("Invalid WS message from user %s: %v", client.UserID, err)
			continue
		}

		h.handleMessage(client, wsMsg)
	}
}

func (h *Handler) handleMessage(client *Client, wsMsg models.WSMessage) {
	ctx := context.Background()

	payloadBytes, err := json.Marshal(wsMsg.Payload)
	if err != nil {
		log.Printf("Failed to re-marshal payload: %v", err)
		return
	}

	switch wsMsg.Type {
	case "message":
		var payload models.SendMessagePayload
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			log.Printf("Invalid message payload: %v", err)
			return
		}
		h.handleTextMessage(ctx, client, payload)

	case "file":
		var payload models.SendFilePayload
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			log.Printf("Invalid file payload: %v", err)
			return
		}
		h.handleFileMessage(ctx, client, payload)

	case "ack":
		var payload models.AckPayload
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			log.Printf("Invalid ack payload: %v", err)
			return
		}
		h.handleAck(ctx, client, payload)

	case "delete_message":
		var payload models.DeleteMessagePayload
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			log.Printf("Invalid delete_message payload: %v", err)
			return
		}
		h.handleDeleteMessage(ctx, client, payload)

	case "media_viewed":
		var payload models.MediaViewedPayload
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			log.Printf("Invalid media_viewed payload: %v", err)
			return
		}
		h.handleMediaViewed(ctx, client, payload)

	case "mark_read":
		var payload models.MarkReadPayload
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			log.Printf("Invalid mark_read payload: %v", err)
			return
		}
		h.handleMarkRead(ctx, client, payload)

	case "file_downloaded":
		var payload models.FileDownloadedPayload
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			log.Printf("Invalid file_downloaded payload: %v", err)
			return
		}
		h.handleFileDownloaded(ctx, client, payload)

	case "secure_file":
		var payload models.SecureFilePayload
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			log.Printf("Invalid secure_file payload: %v", err)
			return
		}
		h.handleSecureFile(ctx, client, payload)

	default:
		log.Printf("Unknown message type from user %s: %s", client.UserID, wsMsg.Type)
	}
}

func (h *Handler) handleTextMessage(ctx context.Context, client *Client, payload models.SendMessagePayload) {
	room, err := h.msgSvc.GetOrCreateRoom(ctx, client.UserID, payload.ReceiverID)
	if err != nil {
		log.Printf("Failed to get/create room: %v", err)
		return
	}

	// "location" carries an encrypted "lat,lng" body; everything else is plain text.
	msgType := "text"
	if payload.MessageType == "location" {
		msgType = "location"
	}

	msg, err := h.msgSvc.StoreMessage(ctx, payload.ClientMessageID, room.ID, client.UserID, payload.ReceiverID, payload.Ciphertext, payload.EncryptionKey, payload.EncryptionIV, msgType)
	if err != nil {
		log.Printf("Failed to store message: %v", err)
		return
	}

	fullPayload := h.msgSvc.BuildMessagePayload(ctx, msg)

	delivered := h.hub.SendToUser(payload.ReceiverID, models.WSMessage{
		Type:    "new_message",
		Payload: fullPayload,
	})

	if delivered {
		h.msgSvc.MarkDelivered(ctx, msg.ID)
		fullPayload["delivered"] = true
	}

	h.hub.SendToUser(client.UserID, models.WSMessage{
		Type:    "delivery_status",
		Payload: fullPayload,
	})
}

func (h *Handler) handleFileMessage(ctx context.Context, client *Client, payload models.SendFilePayload) {
	room, err := h.msgSvc.GetOrCreateRoom(ctx, client.UserID, payload.ReceiverID)
	if err != nil {
		log.Printf("Failed to get/create room: %v", err)
		return
	}

	msg, err := h.msgSvc.StoreMessage(ctx, payload.ClientMessageID, room.ID, client.UserID, payload.ReceiverID, payload.Ciphertext, payload.EncryptionKey, payload.EncryptionIV, "file")
	if err != nil {
		log.Printf("Failed to store file message: %v", err)
		return
	}

	if _, err := h.fileSvc.StoreFileTransfer(ctx, msg.ID, payload); err != nil {
		log.Printf("Failed to store file transfer: %v", err)
		return
	}

	fullPayload := h.msgSvc.BuildMessagePayload(ctx, msg)

	delivered := h.hub.SendToUser(payload.ReceiverID, models.WSMessage{
		Type:    "new_message",
		Payload: fullPayload,
	})

	if delivered {
		h.msgSvc.MarkDelivered(ctx, msg.ID)
		fullPayload["delivered"] = true
	}

	h.hub.SendToUser(client.UserID, models.WSMessage{
		Type:    "delivery_status",
		Payload: fullPayload,
	})
}

func (h *Handler) handleAck(ctx context.Context, client *Client, payload models.AckPayload) {
	if err := h.msgSvc.MarkDelivered(ctx, payload.MessageID); err != nil {
		log.Printf("Failed to mark message delivered: %v", err)
		return
	}

	msg, err := h.msgSvc.GetMessageByID(ctx, payload.MessageID)
	if err != nil {
		log.Printf("Failed to get message for ack: %v", err)
		return
	}
	msg.Delivered = true

	fullPayload := h.msgSvc.BuildMessagePayload(ctx, msg)

	h.hub.SendToUser(client.UserID, models.WSMessage{
		Type:    "ack_callback",
		Payload: fullPayload,
	})

	h.hub.SendToUser(msg.SenderID, models.WSMessage{
		Type:    "delivery_status",
		Payload: fullPayload,
	})
}

func (h *Handler) handleFileDownloaded(ctx context.Context, client *Client, payload models.FileDownloadedPayload) {
	err := h.fileSvc.MarkDownloadedAndDelete(ctx, payload.FileTransferID, client.UserID)
	if err != nil {
		log.Printf("Failed to process file download: %v", err)
		h.hub.SendToUser(client.UserID, models.WSMessage{
			Type: "error",
			Payload: map[string]any{
				"error": err.Error(),
			},
		})
		return
	}

	h.hub.SendToUser(client.UserID, models.WSMessage{
		Type: "file_deleted",
		Payload: map[string]any{
			"file_transfer_id": payload.FileTransferID,
			"status":           "deleted",
		},
	})
}

func (h *Handler) handleDeleteMessage(ctx context.Context, client *Client, payload models.DeleteMessagePayload) {
	// Get the receiver before deleting so we can notify them
	_, receiverID, roomID, err := h.msgSvc.GetMessageOwnership(ctx, payload.MessageID)
	if err != nil {
		log.Printf("Failed to get message ownership: %v", err)
		h.hub.SendToUser(client.UserID, models.WSMessage{
			Type:    "error",
			Payload: map[string]any{"error": "message not found"},
		})
		return
	}

	if err := h.msgSvc.DeleteMessage(ctx, payload.MessageID, client.UserID); err != nil {
		log.Printf("Failed to delete message: %v", err)
		h.hub.SendToUser(client.UserID, models.WSMessage{
			Type:    "error",
			Payload: map[string]any{"error": err.Error()},
		})
		return
	}

	// Notify both sender and receiver about the deletion
	deleteNotif := models.WSMessage{
		Type: "message_deleted",
		Payload: map[string]any{
			"message_id": payload.MessageID,
			"room_id":    roomID,
		},
	}
	h.hub.SendToUser(client.UserID, deleteNotif)
	h.hub.SendToUser(receiverID, deleteNotif)
}

// handleMediaViewed destroys a view-once media message after its receiver has seen
// it: S3 objects (file + thumbnail), the DB row, and both clients' local copies via
// message_deleted. Offline participants get the notification on their next connect.
func (h *Handler) handleMediaViewed(ctx context.Context, client *Client, payload models.MediaViewedPayload) {
	msg, err := h.msgSvc.GetMessageByID(ctx, payload.MessageID)
	if err != nil {
		// Already destroyed (e.g. retried after a reconnect) — make sure the caller drops it too
		h.hub.SendToUser(client.UserID, models.WSMessage{
			Type:    "message_deleted",
			Payload: map[string]any{"message_id": payload.MessageID, "reason": "media_viewed"},
		})
		return
	}
	if msg.ReceiverID != client.UserID || msg.MessageType != "file" {
		log.Printf("media_viewed rejected: user %s on message %s", client.UserID, payload.MessageID)
		return
	}

	// S3: the attachment, plus its thumbnail if the key belongs to the sender's uploads
	keys := []string{}
	if ft, ftErr := h.msgSvc.GetFileTransferForMessage(ctx, msg.ID); ftErr == nil {
		keys = append(keys, ft.S3Key)
	}
	if payload.ThumbnailKey != "" &&
		strings.HasPrefix(payload.ThumbnailKey, "uploads/"+msg.SenderID+"/") &&
		!strings.Contains(payload.ThumbnailKey, "..") {
		keys = append(keys, payload.ThumbnailKey)
	}
	h.fileSvc.DeleteUploadObjects(ctx, keys...)

	if err := h.msgSvc.DeleteMessageByID(ctx, msg.ID); err != nil {
		log.Printf("media_viewed: failed to delete message %s: %v", msg.ID, err)
		return
	}

	notif := models.WSMessage{
		Type: "message_deleted",
		Payload: map[string]any{
			"message_id":        msg.ID,
			"client_message_id": msg.ClientMessageID,
			"room_id":           msg.RoomID,
			"reason":            "media_viewed",
		},
	}
	for _, uid := range []string{msg.SenderID, msg.ReceiverID} {
		if !h.hub.SendToUser(uid, notif) {
			if err := h.msgSvc.QueueDeletion(ctx, uid, msg.ID, msg.ClientMessageID, msg.RoomID); err != nil {
				log.Printf("media_viewed: failed to queue deletion for %s: %v", uid, err)
			}
		}
	}
	log.Printf("media_viewed: destroyed message %s (%d S3 objects)", msg.ID, len(keys))
}

func (h *Handler) handleMarkRead(ctx context.Context, client *Client, payload models.MarkReadPayload) {
	var receipts []services.ReadReceipt
	var err error

	if payload.RoomID != "" && len(payload.MessageIDs) == 0 {
		receipts, err = h.msgSvc.MarkAllReadInRoom(ctx, payload.RoomID, client.UserID)
	} else {
		receipts, err = h.msgSvc.MarkMessagesRead(ctx, payload.MessageIDs, client.UserID)
	}

	if err != nil {
		log.Printf("Failed to mark messages read: %v", err)
		return
	}

	for _, r := range receipts {
		messages := make([]map[string]any, 0, len(r.MessageIDs))
		for _, mid := range r.MessageIDs {
			msg, mErr := h.msgSvc.GetMessageByID(ctx, mid)
			if mErr != nil {
				continue
			}
			messages = append(messages, h.msgSvc.BuildMessagePayload(ctx, msg))
		}

		notif := models.WSMessage{
			Type: "messages_read",
			Payload: map[string]any{
				"room_id":            payload.RoomID,
				"reader_id":          client.UserID,
				"message_ids":        r.MessageIDs,
				"client_message_ids": r.ClientMessageIDs,
				"messages":           messages,
			},
		}
		h.hub.SendToUser(r.SenderID, notif)
		h.hub.SendToUser(client.UserID, notif)
	}
}

// handleSecureFile persists the Signal-encrypted metadata blob and forwards it
// verbatim to the recipient. The server MUST NOT inspect or modify sealed_metadata.
func (h *Handler) handleSecureFile(ctx context.Context, client *Client, payload models.SecureFilePayload) {
	if payload.ReceiverID == "" || payload.SealedMetadata == "" {
		log.Printf("secure_file from %s missing required fields", client.UserID)
		return
	}

	stored, err := h.msgSvc.StoreSecureFile(ctx, payload.ClientMessageID, client.UserID, payload.ReceiverID, payload.SealedMetadata, payload.SignalMsgType)
	if err != nil {
		log.Printf("Failed to persist secure_file: %v", err)
		return
	}

	out := map[string]any{
		"id":                stored.ID,
		"sender_id":         stored.SenderID,
		"receiver_id":       stored.ReceiverID,
		"client_message_id": stored.ClientMessageID,
		"sealed_metadata":   stored.SealedMetadata,
		"signal_msg_type":   stored.SignalMsgType,
		"delivered":         false,
		"created_at":        stored.CreatedAt,
	}

	delivered := h.hub.SendToUser(payload.ReceiverID, models.WSMessage{
		Type:    "secure_file",
		Payload: out,
	})
	if delivered {
		_ = h.msgSvc.MarkSecureFileDelivered(ctx, stored.ID)
		out["delivered"] = true
	}

	// Sender delivery_status mirror
	h.hub.SendToUser(client.UserID, models.WSMessage{
		Type: "delivery_status",
		Payload: map[string]any{
			"id":                stored.ID,
			"client_message_id": stored.ClientMessageID,
			"receiver_id":       stored.ReceiverID,
			"delivered":         delivered,
			"kind":              "secure_file",
		},
	})
}

func (h *Handler) deliverPendingMessages(client *Client) {
	ctx := context.Background()

	// Replay view-once deletions that happened while this user was offline
	if deletions, err := h.msgSvc.GetPendingDeletions(ctx, client.UserID); err == nil {
		for _, d := range deletions {
			if h.hub.SendToUser(client.UserID, models.WSMessage{
				Type: "message_deleted",
				Payload: map[string]any{
					"message_id":        d.MessageID,
					"client_message_id": d.ClientMessageID,
					"room_id":           d.RoomID,
					"reason":            "media_viewed",
				},
			}) {
				_ = h.msgSvc.ClearPendingDeletion(ctx, d.ID)
			}
		}
	} else {
		log.Printf("Failed to fetch pending deletions for user %s: %v", client.UserID, err)
	}
	messages, err := h.msgSvc.GetPendingMessages(ctx, client.UserID)
	if err != nil {
		log.Printf("Failed to fetch pending messages for user %s: %v", client.UserID, err)
		return
	}

	for _, msg := range messages {
		m := msg
		payload := h.msgSvc.BuildMessagePayload(ctx, &m)
		if h.hub.SendToUser(client.UserID, models.WSMessage{
			Type:    "new_message",
			Payload: payload,
		}) {
			h.msgSvc.MarkDelivered(ctx, msg.ID)
		}
	}

	if len(messages) > 0 {
		log.Printf("Delivered %d pending messages to user %s", len(messages), client.UserID)
	}

	// Replay pending secure_file events
	secureFiles, err := h.msgSvc.GetPendingSecureFiles(ctx, client.UserID)
	if err != nil {
		log.Printf("Failed to fetch pending secure_file events for user %s: %v", client.UserID, err)
		return
	}
	for _, m := range secureFiles {
		out := map[string]any{
			"id":                m.ID,
			"sender_id":         m.SenderID,
			"receiver_id":       m.ReceiverID,
			"client_message_id": m.ClientMessageID,
			"sealed_metadata":   m.SealedMetadata,
			"signal_msg_type":   m.SignalMsgType,
			"delivered":         false,
			"created_at":        m.CreatedAt,
		}
		if h.hub.SendToUser(client.UserID, models.WSMessage{
			Type:    "secure_file",
			Payload: out,
		}) {
			_ = h.msgSvc.MarkSecureFileDelivered(ctx, m.ID)
		}
	}
	if len(secureFiles) > 0 {
		log.Printf("Replayed %d pending secure_file events to user %s", len(secureFiles), client.UserID)
	}
}
