package db

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Connect(databaseURL string) *pgxpool.Pool {
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v", err)
	}

	if err := pool.Ping(context.Background()); err != nil {
		log.Fatalf("Unable to ping database: %v", err)
	}

	log.Println("Connected to PostgreSQL")
	return pool
}

func RunMigrations(pool *pgxpool.Pool) {
	ctx := context.Background()

	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		username VARCHAR(255) UNIQUE NOT NULL,
		password_hash VARCHAR(255) NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS identity_keys (
		user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		public_key TEXT NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS signed_prekeys (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		key_id INT NOT NULL,
		public_key TEXT NOT NULL,
		signature TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE(user_id, key_id)
	);

	CREATE TABLE IF NOT EXISTS one_time_prekeys (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		key_id INT NOT NULL,
		public_key TEXT NOT NULL,
		used BOOLEAN NOT NULL DEFAULT FALSE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE(user_id, key_id)
	);

	CREATE TABLE IF NOT EXISTS kyber_prekeys (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		key_id INT NOT NULL,
		public_key TEXT NOT NULL,
		signature TEXT NOT NULL,
		used BOOLEAN NOT NULL DEFAULT FALSE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE(user_id, key_id)
	);

	CREATE TABLE IF NOT EXISTS signal_devices (
		user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		registration_id INT NOT NULL,
		device_id INT NOT NULL DEFAULT 1,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS multipart_uploads (
		upload_id TEXT PRIMARY KEY,
		object_key TEXT NOT NULL,
		bucket TEXT NOT NULL,
		sender_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		recipient_id UUID REFERENCES users(id) ON DELETE SET NULL,
		file_size BIGINT NOT NULL DEFAULT 0,
		part_count INT NOT NULL DEFAULT 0,
		part_size INT NOT NULL DEFAULT 0,
		content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
		status TEXT NOT NULL DEFAULT 'in_progress',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		completed_at TIMESTAMPTZ,
		expires_at TIMESTAMPTZ
	);

	CREATE TABLE IF NOT EXISTS sft_files (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		object_key TEXT NOT NULL UNIQUE,
		bucket TEXT NOT NULL,
		sender_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		recipient_id UUID REFERENCES users(id) ON DELETE SET NULL,
		size_bytes BIGINT NOT NULL DEFAULT 0,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		expires_at TIMESTAMPTZ,
		deleted BOOLEAN NOT NULL DEFAULT FALSE,
		deleted_at TIMESTAMPTZ
	);

	CREATE TABLE IF NOT EXISTS secure_file_messages (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		client_message_id TEXT,
		sender_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		receiver_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		sealed_metadata TEXT NOT NULL,
		signal_msg_type INT NOT NULL DEFAULT 2,
		delivered BOOLEAN NOT NULL DEFAULT FALSE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_secure_file_messages_pending
		ON secure_file_messages(receiver_id) WHERE delivered = FALSE;

	CREATE TABLE IF NOT EXISTS rooms (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		user1_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		user2_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE(user1_id, user2_id)
	);

	CREATE TABLE IF NOT EXISTS messages (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		client_message_id TEXT,
		room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
		sender_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		receiver_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		ciphertext TEXT NOT NULL,
		encryption_key TEXT NOT NULL DEFAULT '',
		encryption_iv TEXT NOT NULL DEFAULT '',
		message_type VARCHAR(20) NOT NULL DEFAULT 'text',
		delivered BOOLEAN NOT NULL DEFAULT FALSE,
		read_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS file_transfers (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		message_id UUID UNIQUE NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
		encrypted_s3_url TEXT NOT NULL,
		s3_key VARCHAR(512) NOT NULL,
		file_name TEXT NOT NULL,
		file_size BIGINT NOT NULL DEFAULT 0,
		content_type VARCHAR(50) NOT NULL DEFAULT '',
		content_subtype VARCHAR(50) NOT NULL DEFAULT '',
		caption TEXT NOT NULL DEFAULT '',
		width INT NOT NULL DEFAULT 0,
		height INT NOT NULL DEFAULT 0,
		duration INT NOT NULL DEFAULT 0,
		thumbnail TEXT NOT NULL DEFAULT '',
		bucket VARCHAR(255) NOT NULL DEFAULT '',
		region VARCHAR(64) NOT NULL DEFAULT '',
		downloaded BOOLEAN NOT NULL DEFAULT FALSE,
		deleted_from_s3 BOOLEAN NOT NULL DEFAULT FALSE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	-- message_deleted notifications for users who were offline when a
	-- view-once media message was destroyed; replayed on their next connect.
	CREATE TABLE IF NOT EXISTS pending_deletions (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		message_id TEXT NOT NULL,
		client_message_id TEXT NOT NULL DEFAULT '',
		room_id TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_pending_deletions_user ON pending_deletions(user_id);

	CREATE INDEX IF NOT EXISTS idx_messages_receiver_undelivered
		ON messages(receiver_id);
	CREATE INDEX IF NOT EXISTS idx_one_time_prekeys_available
		ON one_time_prekeys(user_id) WHERE used = FALSE;
	CREATE INDEX IF NOT EXISTS idx_file_transfers_pending
		ON file_transfers(id) WHERE downloaded = FALSE;

	-- Add new columns if they don't exist (safe for re-runs)
	ALTER TABLE messages ADD COLUMN IF NOT EXISTS read_at TIMESTAMPTZ;
	ALTER TABLE messages ADD COLUMN IF NOT EXISTS client_message_id TEXT;
	ALTER TABLE messages ADD COLUMN IF NOT EXISTS encryption_key TEXT NOT NULL DEFAULT '';
	ALTER TABLE messages ADD COLUMN IF NOT EXISTS encryption_iv TEXT NOT NULL DEFAULT '';
	ALTER TABLE file_transfers ADD COLUMN IF NOT EXISTS content_type VARCHAR(50) NOT NULL DEFAULT '';
	ALTER TABLE file_transfers ADD COLUMN IF NOT EXISTS content_subtype VARCHAR(50) NOT NULL DEFAULT '';
	ALTER TABLE file_transfers ADD COLUMN IF NOT EXISTS caption TEXT NOT NULL DEFAULT '';
	ALTER TABLE file_transfers ADD COLUMN IF NOT EXISTS width INT NOT NULL DEFAULT 0;
	ALTER TABLE file_transfers ADD COLUMN IF NOT EXISTS height INT NOT NULL DEFAULT 0;
	ALTER TABLE file_transfers ADD COLUMN IF NOT EXISTS duration INT NOT NULL DEFAULT 0;
	ALTER TABLE file_transfers ADD COLUMN IF NOT EXISTS thumbnail TEXT NOT NULL DEFAULT '';
	ALTER TABLE file_transfers ADD COLUMN IF NOT EXISTS bucket VARCHAR(255) NOT NULL DEFAULT '';
	ALTER TABLE file_transfers ADD COLUMN IF NOT EXISTS region VARCHAR(64) NOT NULL DEFAULT '';

	CREATE INDEX IF NOT EXISTS idx_messages_room_created
		ON messages(room_id, created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_messages_receiver_unread
		ON messages(receiver_id) WHERE read_at IS NULL;

	-- Purge old chat data (requested: drop existing chats only)
	DELETE FROM messages;
	`

	if _, err := pool.Exec(ctx, schema); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	fmt.Println("Database migrations completed")
}
