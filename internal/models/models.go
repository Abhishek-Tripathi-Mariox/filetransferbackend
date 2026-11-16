package models

import "time"

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type IdentityKey struct {
	UserID    string    `json:"user_id"`
	PublicKey string    `json:"public_key"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SignedPreKey struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	KeyID     int       `json:"key_id"`
	PublicKey string    `json:"public_key"`
	Signature string    `json:"signature"`
	CreatedAt time.Time `json:"created_at"`
}

type OneTimePreKey struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	KeyID     int       `json:"key_id"`
	PublicKey string    `json:"public_key"`
	Used      bool      `json:"used"`
	CreatedAt time.Time `json:"created_at"`
}

type Room struct {
	ID            string    `json:"id"`
	User1ID       string    `json:"user1_id"`
	User2ID       string    `json:"user2_id"`
	User1Username string    `json:"user1_username,omitempty"`
	User2Username string    `json:"user2_username,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type Message struct {
	ID              string `json:"id"`
	ClientMessageID string `json:"client_message_id"`
	RoomID          string `json:"room_id"`
	SenderID        string `json:"sender_id"`
	ReceiverID      string `json:"receiver_id"`
	Ciphertext      string `json:"ciphertext"`
	EncryptionKey   string `json:"encryption_key"`
	EncryptionIV    string `json:"encryption_iv"`
	MessageType     string `json:"message_type"` // "text" or "file"
	// SignalMessageType is 2 (whisper) or 3 (prekey) when Ciphertext is a Signal
	// message sealed for the receiver, 0 for legacy messages.
	SignalMessageType int        `json:"signal_message_type"`
	Delivered         bool       `json:"delivered"`
	ReadAt            *time.Time `json:"read_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

type FileTransfer struct {
	ID             string    `json:"id"`
	MessageID      string    `json:"message_id"`
	EncryptedS3URL string    `json:"encrypted_s3_url"`
	S3Key          string    `json:"s3_key"`
	FileName       string    `json:"file_name"`
	FileSize       int64     `json:"file_size"`
	ContentType    string    `json:"content_type"`
	ContentSubtype string    `json:"content_subtype"`
	Caption        string    `json:"caption"`
	Width          int       `json:"width"`
	Height         int       `json:"height"`
	Duration       int       `json:"duration"`
	Thumbnail      string    `json:"thumbnail"`
	Bucket         string    `json:"bucket"`
	Region         string    `json:"region"`
	Downloaded     bool      `json:"downloaded"`
	DeletedFromS3  bool      `json:"deleted_from_s3"`
	CreatedAt      time.Time `json:"created_at"`
}

// --- Request/Response types ---

type RegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Token  string `json:"token"`
	UserID string `json:"user_id"`
}

type UploadKeysRequest struct {
	RegistrationID int                `json:"registration_id"`
	DeviceID       int                `json:"device_id"`
	IdentityKey    string             `json:"identity_key"`
	SignedPreKey   SignedPreKeyReq    `json:"signed_prekey"`
	OneTimePreKeys []OneTimePreKeyReq `json:"one_time_prekeys"`
	KyberPreKeys   []KyberPreKeyReq   `json:"kyber_prekeys"`
}

type SignedPreKeyReq struct {
	KeyID     int    `json:"key_id"`
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"`
}

type OneTimePreKeyReq struct {
	KeyID     int    `json:"key_id"`
	PublicKey string `json:"public_key"`
}

type KyberPreKeyReq struct {
	KeyID     int    `json:"key_id"`
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"`
}

type KyberPreKey struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	KeyID     int       `json:"key_id"`
	PublicKey string    `json:"public_key"`
	Signature string    `json:"signature"`
	Used      bool      `json:"used"`
	CreatedAt time.Time `json:"created_at"`
}

type KeyBundleResponse struct {
	UserID         string         `json:"user_id"`
	RegistrationID int            `json:"registration_id"`
	DeviceID       int            `json:"device_id"`
	IdentityKey    string         `json:"identity_key"`
	SignedPreKey   SignedPreKey   `json:"signed_prekey"`
	OneTimePreKey  *OneTimePreKey `json:"one_time_prekey,omitempty"`
	KyberPreKey    *KyberPreKey   `json:"kyber_prekey,omitempty"`
}

// --- Secure File Transfer (multipart) types ---

type MultipartInitiateRequest struct {
	FileSize    int64  `json:"file_size"`
	PartCount   int    `json:"part_count"`
	PartSize    int    `json:"part_size"`
	ContentType string `json:"content_type"`
	RecipientID string `json:"recipient_id"`
}

type MultipartPartURL struct {
	PartNumber   int    `json:"part_number"`
	PresignedURL string `json:"presigned_url"`
}

type MultipartInitiateResponse struct {
	UploadID         string             `json:"upload_id"`
	Bucket           string             `json:"bucket"`
	ObjectKey        string             `json:"object_key"`
	ExpiresInSeconds int                `json:"expires_in_seconds"`
	Parts            []MultipartPartURL `json:"parts"`
}

type CompletedPart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
}

type MultipartCompleteRequest struct {
	UploadID  string          `json:"upload_id"`
	ObjectKey string          `json:"object_key"`
	Bucket    string          `json:"bucket"`
	Parts     []CompletedPart `json:"parts"`
}

type MultipartCompleteResponse struct {
	Status    string     `json:"status"`
	FileID    string     `json:"file_id"`
	ObjectKey string     `json:"object_key"`
	Bucket    string     `json:"bucket"`
	SizeBytes int64      `json:"size_bytes"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// MultipartPresignPartsRequest asks for fresh upload URLs for some parts of an
// in-progress upload (used when resuming after the first URLs expired).
type MultipartPresignPartsRequest struct {
	UploadID    string `json:"upload_id"`
	PartNumbers []int  `json:"part_numbers"`
}

type MultipartPresignPartsResponse struct {
	UploadID         string             `json:"upload_id"`
	ExpiresInSeconds int                `json:"expires_in_seconds"`
	Parts            []MultipartPartURL `json:"parts"`
}

// ClientConfig is served by GET /api/config so limits can change without an app release.
type ClientConfig struct {
	MaxFileSizeBytes int64 `json:"max_file_size_bytes"`
	MinFileSizeBytes int64 `json:"min_file_size_bytes"`
	MaxFilesPerSend  int   `json:"max_files_per_send"`
	MaxFileNameBytes int   `json:"max_file_name_bytes"`
	ThumbnailMaxB    int   `json:"thumbnail_max_bytes"`
	ThumbnailMaxPx   int   `json:"thumbnail_max_px"`
	FileTTLHours     int   `json:"file_ttl_hours"`
}

type MultipartAbortRequest struct {
	UploadID  string `json:"upload_id"`
	ObjectKey string `json:"object_key"`
}

type MultipartListedPart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
	SizeBytes  int64  `json:"size_bytes"`
}

type MultipartListPartsResponse struct {
	UploadID string                `json:"upload_id"`
	Parts    []MultipartListedPart `json:"parts"`
}

type MultipartDownloadRequest struct {
	ObjectKey string `json:"object_key"`
	Bucket    string `json:"bucket"`
}

type MultipartDownloadResponse struct {
	PresignedURL     string `json:"presigned_url"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
	SizeBytes        int64  `json:"size_bytes"`
}

// --- secure_file WS payload ---

type SecureFilePayload struct {
	ClientMessageID string `json:"client_message_id"`
	ReceiverID      string `json:"receiver_id"`
	SealedMetadata  string `json:"sealed_metadata"`
	SignalMsgType   int    `json:"signal_msg_type"`
}

type SecureFileMessage struct {
	ID              string    `json:"id"`
	ClientMessageID string    `json:"client_message_id"`
	SenderID        string    `json:"sender_id"`
	ReceiverID      string    `json:"receiver_id"`
	SealedMetadata  string    `json:"sealed_metadata"`
	SignalMsgType   int       `json:"signal_msg_type"`
	Delivered       bool      `json:"delivered"`
	CreatedAt       time.Time `json:"created_at"`
}

// WebSocket message envelope
type WSMessage struct {
	Type    string `json:"type"` // "message", "file", "ack", "typing", "key_request"
	Payload any    `json:"payload"`
}

type SendMessagePayload struct {
	ClientMessageID string `json:"client_message_id"`
	ReceiverID      string `json:"receiver_id"`
	Ciphertext      string `json:"ciphertext"`
	EncryptionKey   string `json:"encryption_key"`
	EncryptionIV    string `json:"encryption_iv"`
	MessageType     string `json:"message_type"` // "text" or "file"
}

type SendFilePayload struct {
	ClientMessageID string `json:"client_message_id"`
	ReceiverID      string `json:"receiver_id"`
	MessageType     string `json:"message_type"`
	Ciphertext      string `json:"ciphertext"`
	EncryptionKey   string `json:"encryption_key"`
	EncryptionIV    string `json:"encryption_iv"`
	// SignalMessageType > 0: Ciphertext is the Signal-sealed file record holding the
	// file key and all metadata; the plaintext metadata fields below are left empty.
	SignalMessageType int    `json:"signal_message_type"`
	EncryptedS3URL    string `json:"encrypted_s3_url"`
	S3Key             string `json:"s3_key"`
	FileName          string `json:"file_name"`
	FileSize          int64  `json:"file_size"`
	ContentType       string `json:"content_type"`
	ContentSubtype    string `json:"content_subtype"`
	Caption           string `json:"caption"`
	Width             int    `json:"width"`
	Height            int    `json:"height"`
	Duration          int    `json:"duration"`
	Thumbnail         string `json:"thumbnail"`
	Bucket            string `json:"bucket"`
	Region            string `json:"region"`
}

type AckPayload struct {
	MessageID string `json:"message_id"`
}

type FileDownloadedPayload struct {
	FileTransferID string `json:"file_transfer_id"`
}

type DeleteMessagePayload struct {
	MessageID string `json:"message_id"`
}

// MediaViewedPayload — the receiver has viewed a media message; destroy it everywhere.
// ThumbnailKey is the thumbnail's S3 key (the server only stores it encrypted).
type MediaViewedPayload struct {
	MessageID    string `json:"message_id"`
	ThumbnailKey string `json:"thumbnail_key"`
}

type MarkReadPayload struct {
	MessageIDs []string `json:"message_ids"`
	RoomID     string   `json:"room_id"`
}

// MessageWithFile is used for the messages list API response
type MessageWithFile struct {
	Message
	FileTransfer *FileTransfer `json:"file_transfer,omitempty"`
}

// RoomWithUnread is used for room list with unread badge counts
type RoomWithUnread struct {
	Room
	UnreadCount int      `json:"unread_count"`
	LastMessage *Message `json:"last_message,omitempty"`
}
