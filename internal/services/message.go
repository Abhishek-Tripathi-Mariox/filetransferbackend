package services

import (
	"context"
	"fmt"

	"github.com/filetransfer/backend/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MessageService struct {
	db *pgxpool.Pool
}

func NewMessageService(db *pgxpool.Pool) *MessageService {
	return &MessageService{db: db}
}

// GetUserByID returns basic user info (id, username).
func (s *MessageService) GetUserByID(ctx context.Context, userID string) (*models.User, error) {
	var user models.User
	err := s.db.QueryRow(ctx, `
		SELECT id, username, created_at FROM users WHERE id = $1
	`, userID).Scan(&user.ID, &user.Username, &user.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// ListUsers returns the users excludeID can start a new chat with: everyone except
// themselves and anyone they already share a room with, ordered by username.
// Used by the client's "new chat" picker.
func (s *MessageService) ListUsers(ctx context.Context, excludeID string) ([]models.User, error) {
	rows, err := s.db.Query(ctx, `
		SELECT u.id, u.username, u.created_at FROM users u
		WHERE u.id::text <> $1
		  AND NOT EXISTS (
			SELECT 1 FROM rooms r
			WHERE (r.user1_id = u.id AND r.user2_id::text = $1)
			   OR (r.user2_id = u.id AND r.user1_id::text = $1)
		  )
		ORDER BY u.username
	`, excludeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := []models.User{}
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Username, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// GetOrCreateRoom finds an existing room between two users, or creates one.
// It always stores the smaller UUID as user1_id to ensure uniqueness.
func (s *MessageService) GetOrCreateRoom(ctx context.Context, userA, userB string) (*models.Room, error) {
	user1, user2 := userA, userB
	if user1 > user2 {
		user1, user2 = user2, user1
	}

	var room models.Room
	err := s.db.QueryRow(ctx, `
		INSERT INTO rooms (id, user1_id, user2_id)
		VALUES (gen_random_uuid(), $1, $2)
		ON CONFLICT (user1_id, user2_id) DO UPDATE SET user1_id = rooms.user1_id
		RETURNING id, user1_id, user2_id, created_at
	`, user1, user2).Scan(&room.ID, &room.User1ID, &room.User2ID, &room.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &room, nil
}

// GetRoomsForUser returns all rooms a user belongs to.
func (s *MessageService) GetRoomsForUser(ctx context.Context, userID string) ([]models.Room, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.user1_id, r.user2_id, u1.username, u2.username, r.created_at
		FROM rooms r
		JOIN users u1 ON u1.id = r.user1_id
		JOIN users u2 ON u2.id = r.user2_id
		WHERE r.user1_id = $1 OR r.user2_id = $1
		ORDER BY r.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []models.Room
	for rows.Next() {
		var r models.Room
		if err := rows.Scan(&r.ID, &r.User1ID, &r.User2ID, &r.User1Username, &r.User2Username, &r.CreatedAt); err != nil {
			return nil, err
		}
		rooms = append(rooms, r)
	}
	return rooms, nil
}

// StoreMessage saves an encrypted message and returns the created message.
func (s *MessageService) StoreMessage(ctx context.Context, clientMessageID, roomID, senderID, receiverID, ciphertext, encryptionKey, encryptionIV, msgType string) (*models.Message, error) {
	var msg models.Message
	err := s.db.QueryRow(ctx, `
		INSERT INTO messages (id, client_message_id, room_id, sender_id, receiver_id, ciphertext, encryption_key, encryption_iv, message_type)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, COALESCE(client_message_id, ''), room_id, sender_id, receiver_id, ciphertext, encryption_key, encryption_iv, message_type, delivered, read_at, created_at
	`, clientMessageID, roomID, senderID, receiverID, ciphertext, encryptionKey, encryptionIV, msgType,
	).Scan(&msg.ID, &msg.ClientMessageID, &msg.RoomID, &msg.SenderID, &msg.ReceiverID, &msg.Ciphertext, &msg.EncryptionKey, &msg.EncryptionIV, &msg.MessageType, &msg.Delivered, &msg.ReadAt, &msg.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

// GetMessageByID returns a single message by its ID.
func (s *MessageService) GetMessageByID(ctx context.Context, messageID string) (*models.Message, error) {
	var msg models.Message
	err := s.db.QueryRow(ctx, `
		SELECT id, COALESCE(client_message_id, ''), room_id, sender_id, receiver_id, ciphertext, encryption_key, encryption_iv, message_type, delivered, read_at, created_at
		FROM messages WHERE id = $1
	`, messageID).Scan(&msg.ID, &msg.ClientMessageID, &msg.RoomID, &msg.SenderID, &msg.ReceiverID, &msg.Ciphertext, &msg.EncryptionKey, &msg.EncryptionIV, &msg.MessageType, &msg.Delivered, &msg.ReadAt, &msg.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

// MarkDelivered marks a message as delivered.
func (s *MessageService) MarkDelivered(ctx context.Context, messageID string) error {
	_, err := s.db.Exec(ctx,
		"UPDATE messages SET delivered = TRUE WHERE id = $1", messageID,
	)
	return err
}

// GetPendingMessages returns all undelivered messages for a user (offline queue).
func (s *MessageService) GetPendingMessages(ctx context.Context, receiverID string) ([]models.Message, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, COALESCE(client_message_id, ''), room_id, sender_id, receiver_id, ciphertext, encryption_key, encryption_iv, message_type, delivered, read_at, created_at
		FROM messages
		WHERE receiver_id = $1 AND delivered = FALSE
		ORDER BY created_at ASC
	`, receiverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []models.Message
	for rows.Next() {
		var msg models.Message
		if err := rows.Scan(&msg.ID, &msg.ClientMessageID, &msg.RoomID, &msg.SenderID, &msg.ReceiverID, &msg.Ciphertext, &msg.EncryptionKey, &msg.EncryptionIV, &msg.MessageType, &msg.Delivered, &msg.ReadAt, &msg.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// GetMessagesByRoom returns messages for a room with cursor-based pagination.
// Pass empty cursor for the first page. Returns messages older than cursor, newest first.
func (s *MessageService) GetMessagesByRoom(ctx context.Context, roomID string, limit int, cursor string) ([]models.Message, error) {
	var query string
	var args []any

	if cursor == "" {
		query = `
			SELECT id, COALESCE(client_message_id, ''), room_id, sender_id, receiver_id, ciphertext, encryption_key, encryption_iv, message_type, delivered, read_at, created_at
			FROM messages
			WHERE room_id = $1
			ORDER BY created_at DESC
			LIMIT $2`
		args = []any{roomID, limit}
	} else {
		query = `
			SELECT id, COALESCE(client_message_id, ''), room_id, sender_id, receiver_id, ciphertext, encryption_key, encryption_iv, message_type, delivered, read_at, created_at
			FROM messages
			WHERE room_id = $1 AND created_at < (SELECT created_at FROM messages WHERE id = $3)
			ORDER BY created_at DESC
			LIMIT $2`
		args = []any{roomID, limit, cursor}
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []models.Message
	for rows.Next() {
		var msg models.Message
		if err := rows.Scan(&msg.ID, &msg.ClientMessageID, &msg.RoomID, &msg.SenderID, &msg.ReceiverID, &msg.Ciphertext, &msg.EncryptionKey, &msg.EncryptionIV, &msg.MessageType, &msg.Delivered, &msg.ReadAt, &msg.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// DeleteMessage hard-deletes a message from the database. Only the sender can delete.
// File transfers are cascade-deleted via FK constraint.
func (s *MessageService) DeleteMessage(ctx context.Context, messageID, senderID string) error {
	result, err := s.db.Exec(ctx, `
		DELETE FROM messages WHERE id = $1 AND sender_id = $2
	`, messageID, senderID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("message not found or not authorized")
	}
	return nil
}

// DeleteMessageByID hard-deletes a message (file_transfers cascade). No ownership
// check — callers must authorize first.
func (s *MessageService) DeleteMessageByID(ctx context.Context, messageID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM messages WHERE id = $1`, messageID)
	return err
}

// PendingDeletion is a message_deleted notification queued for an offline user.
type PendingDeletion struct {
	ID              string
	MessageID       string
	ClientMessageID string
	RoomID          string
}

// QueueDeletion stores a message_deleted notification to replay on userID's next connect.
func (s *MessageService) QueueDeletion(ctx context.Context, userID, messageID, clientMessageID, roomID string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO pending_deletions (user_id, message_id, client_message_id, room_id)
		VALUES ($1, $2, $3, $4)
	`, userID, messageID, clientMessageID, roomID)
	return err
}

// GetPendingDeletions returns queued message_deleted notifications for userID.
func (s *MessageService) GetPendingDeletions(ctx context.Context, userID string) ([]PendingDeletion, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, message_id, client_message_id, room_id
		FROM pending_deletions WHERE user_id = $1 ORDER BY created_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PendingDeletion
	for rows.Next() {
		var p PendingDeletion
		if err := rows.Scan(&p.ID, &p.MessageID, &p.ClientMessageID, &p.RoomID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClearPendingDeletion removes a delivered pending deletion.
func (s *MessageService) ClearPendingDeletion(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM pending_deletions WHERE id = $1`, id)
	return err
}

// GetMessageOwnership returns the sender_id and receiver_id for a message.
func (s *MessageService) GetMessageOwnership(ctx context.Context, messageID string) (senderID, receiverID, roomID string, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT sender_id, receiver_id, room_id FROM messages WHERE id = $1
	`, messageID).Scan(&senderID, &receiverID, &roomID)
	return
}

// ReadReceipt groups the server + client message IDs marked as read, per sender.
type ReadReceipt struct {
	SenderID         string
	MessageIDs       []string
	ClientMessageIDs []string
}

// MarkMessagesRead marks messages as read and returns read receipts grouped by sender.
func (s *MessageService) MarkMessagesRead(ctx context.Context, messageIDs []string, readerID string) ([]ReadReceipt, error) {
	if len(messageIDs) == 0 {
		return nil, nil
	}

	// Only mark messages where the reader is the receiver
	rows, err := s.db.Query(ctx, `
		UPDATE messages SET read_at = NOW()
		WHERE id = ANY($1) AND receiver_id = $2 AND read_at IS NULL
		RETURNING id, COALESCE(client_message_id, ''), sender_id
	`, messageIDs, readerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectReadReceipts(rows)
}

// MarkAllReadInRoom marks all unread messages in a room as read for the given user (as receiver).
func (s *MessageService) MarkAllReadInRoom(ctx context.Context, roomID, readerID string) ([]ReadReceipt, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE messages SET read_at = NOW()
		WHERE room_id = $1 AND receiver_id = $2 AND read_at IS NULL
		RETURNING id, COALESCE(client_message_id, ''), sender_id
	`, roomID, readerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectReadReceipts(rows)
}

type pgxRows interface {
	Next() bool
	Scan(dest ...any) error
}

func collectReadReceipts(rows pgxRows) ([]ReadReceipt, error) {
	bySender := make(map[string]*ReadReceipt)
	for rows.Next() {
		var id, clientID, senderID string
		if err := rows.Scan(&id, &clientID, &senderID); err != nil {
			return nil, err
		}
		r, ok := bySender[senderID]
		if !ok {
			r = &ReadReceipt{SenderID: senderID}
			bySender[senderID] = r
		}
		r.MessageIDs = append(r.MessageIDs, id)
		r.ClientMessageIDs = append(r.ClientMessageIDs, clientID)
	}

	result := make([]ReadReceipt, 0, len(bySender))
	for _, r := range bySender {
		result = append(result, *r)
	}
	return result, nil
}

// GetUnreadCountsForUser returns unread message counts grouped by room for a user.
func (s *MessageService) GetUnreadCountsForUser(ctx context.Context, userID string) (map[string]int, error) {
	rows, err := s.db.Query(ctx, `
		SELECT room_id, COUNT(*) as unread_count
		FROM messages
		WHERE receiver_id = $1 AND read_at IS NULL
		GROUP BY room_id
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var roomID string
		var count int
		if err := rows.Scan(&roomID, &count); err != nil {
			return nil, err
		}
		counts[roomID] = count
	}
	return counts, nil
}

// GetRoomsWithUnread returns rooms for a user with unread counts and last message.
func (s *MessageService) GetRoomsWithUnread(ctx context.Context, userID string) ([]models.RoomWithUnread, error) {
	rooms, err := s.GetRoomsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	unreadCounts, err := s.GetUnreadCountsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	var result []models.RoomWithUnread
	for _, room := range rooms {
		rw := models.RoomWithUnread{
			Room:        room,
			UnreadCount: unreadCounts[room.ID],
		}

		// Get last message for the room
		msgs, err := s.GetMessagesByRoom(ctx, room.ID, 1, "")
		if err == nil && len(msgs) > 0 {
			rw.LastMessage = &msgs[0]
		}

		result = append(result, rw)
	}
	return result, nil
}

// GetFileTransferForMessage returns the file transfer associated with a message.
func (s *MessageService) GetFileTransferForMessage(ctx context.Context, messageID string) (*models.FileTransfer, error) {
	var ft models.FileTransfer
	err := s.db.QueryRow(ctx, `
		SELECT id, message_id, encrypted_s3_url, s3_key, file_name, file_size,
		       content_type, content_subtype, caption, width, height, duration, thumbnail,
		       bucket, region, downloaded, deleted_from_s3, created_at
		FROM file_transfers WHERE message_id = $1
	`, messageID).Scan(&ft.ID, &ft.MessageID, &ft.EncryptedS3URL, &ft.S3Key, &ft.FileName, &ft.FileSize,
		&ft.ContentType, &ft.ContentSubtype, &ft.Caption, &ft.Width, &ft.Height, &ft.Duration, &ft.Thumbnail,
		&ft.Bucket, &ft.Region, &ft.Downloaded, &ft.DeletedFromS3, &ft.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &ft, nil
}

// StoreSecureFile persists a Signal-encrypted secure_file event for replay.
// The server stores the sealed metadata verbatim — it cannot and must not decrypt it.
func (s *MessageService) StoreSecureFile(ctx context.Context, clientMessageID, senderID, receiverID, sealedMetadata string, signalMsgType int) (*models.SecureFileMessage, error) {
	var m models.SecureFileMessage
	err := s.db.QueryRow(ctx, `
		INSERT INTO secure_file_messages (id, client_message_id, sender_id, receiver_id, sealed_metadata, signal_msg_type)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5)
		RETURNING id, COALESCE(client_message_id, ''), sender_id, receiver_id, sealed_metadata, signal_msg_type, delivered, created_at
	`, clientMessageID, senderID, receiverID, sealedMetadata, signalMsgType,
	).Scan(&m.ID, &m.ClientMessageID, &m.SenderID, &m.ReceiverID, &m.SealedMetadata, &m.SignalMsgType, &m.Delivered, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// MarkSecureFileDelivered flips the delivered flag for a secure_file message.
func (s *MessageService) MarkSecureFileDelivered(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `UPDATE secure_file_messages SET delivered = TRUE WHERE id = $1`, id)
	return err
}

// GetPendingSecureFiles returns undelivered secure_file events for a recipient.
func (s *MessageService) GetPendingSecureFiles(ctx context.Context, receiverID string) ([]models.SecureFileMessage, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, COALESCE(client_message_id, ''), sender_id, receiver_id, sealed_metadata, signal_msg_type, delivered, created_at
		FROM secure_file_messages
		WHERE receiver_id = $1 AND delivered = FALSE
		ORDER BY created_at ASC
	`, receiverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.SecureFileMessage
	for rows.Next() {
		var m models.SecureFileMessage
		if err := rows.Scan(&m.ID, &m.ClientMessageID, &m.SenderID, &m.ReceiverID, &m.SealedMetadata, &m.SignalMsgType, &m.Delivered, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// BuildMessagePayload returns a full payload map for a message, enriched with
// file_transfer fields when applicable. This is the canonical shape emitted to
// clients in new_message, delivery_status, ack_callback, messages_read, and
// the REST messages list — keeping every path in sync with the same keys.
func (s *MessageService) BuildMessagePayload(ctx context.Context, msg *models.Message) map[string]any {
	payload := map[string]any{
		"id":                msg.ID,
		"client_message_id": msg.ClientMessageID,
		"room_id":           msg.RoomID,
		"sender_id":         msg.SenderID,
		"receiver_id":       msg.ReceiverID,
		"message_type":      msg.MessageType,
		"ciphertext":        msg.Ciphertext,
		"encryption_key":    msg.EncryptionKey,
		"encryption_iv":     msg.EncryptionIV,
		"delivered":         msg.Delivered,
		"read_at":           msg.ReadAt,
		"created_at":        msg.CreatedAt,
	}

	if sender, err := s.GetUserByID(ctx, msg.SenderID); err == nil && sender != nil {
		payload["sender_username"] = sender.Username
	}
	if receiver, err := s.GetUserByID(ctx, msg.ReceiverID); err == nil && receiver != nil {
		payload["receiver_username"] = receiver.Username
	}

	if msg.MessageType == "file" {
		if ft, err := s.GetFileTransferForMessage(ctx, msg.ID); err == nil && ft != nil {
			payload["file_transfer_id"] = ft.ID
			payload["s3_key"] = ft.S3Key
			payload["encrypted_s3_url"] = ft.EncryptedS3URL
			payload["file_name"] = ft.FileName
			payload["file_size"] = ft.FileSize
			payload["content_type"] = ft.ContentType
			payload["content_subtype"] = ft.ContentSubtype
			payload["caption"] = ft.Caption
			payload["width"] = ft.Width
			payload["height"] = ft.Height
			payload["duration"] = ft.Duration
			payload["thumbnail"] = ft.Thumbnail
			payload["bucket"] = ft.Bucket
			payload["region"] = ft.Region
			payload["downloaded"] = ft.Downloaded
		}
	}

	return payload
}
