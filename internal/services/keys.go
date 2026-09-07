package services

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/filetransfer/backend/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type KeyService struct {
	db *pgxpool.Pool
}

func NewKeyService(db *pgxpool.Pool) *KeyService {
	return &KeyService{db: db}
}

// validateSignalKey performs structural sanity checks on a base64-encoded
// public key or signature. It does NOT perform XEd25519 signature verification —
// that requires a Go libsignal binding which is not available here. The Android
// client signs with libsignal; verifying server-side would need the same library.
// We accept syntactically well-formed inputs and reject obviously malformed ones.
func validateSignalKey(label, b64 string, minLen int) error {
	if b64 == "" {
		return fmt.Errorf("%s is required", label)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		// Try URL encoding too
		if raw, err = base64.RawStdEncoding.DecodeString(b64); err != nil {
			return fmt.Errorf("%s is not valid base64", label)
		}
	}
	if len(raw) < minLen {
		return fmt.Errorf("%s is too short", label)
	}
	return nil
}

// UploadKeys persists the user's full key bundle. On re-upload it replaces the
// identity + signed prekey, and APPENDs new one-time / Kyber prekeys.
func (s *KeyService) UploadKeys(ctx context.Context, userID string, req models.UploadKeysRequest) (map[string]any, error) {
	if err := validateSignalKey("identity_key", req.IdentityKey, 32); err != nil {
		return nil, err
	}
	if err := validateSignalKey("signed_prekey.public_key", req.SignedPreKey.PublicKey, 32); err != nil {
		return nil, err
	}
	if err := validateSignalKey("signed_prekey.signature", req.SignedPreKey.Signature, 32); err != nil {
		return nil, err
	}
	for _, k := range req.KyberPreKeys {
		if err := validateSignalKey("kyber_prekey.public_key", k.PublicKey, 32); err != nil {
			return nil, err
		}
		if err := validateSignalKey("kyber_prekey.signature", k.Signature, 32); err != nil {
			return nil, err
		}
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Device + registration id
	deviceID := req.DeviceID
	if deviceID == 0 {
		deviceID = 1
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO signal_devices (user_id, registration_id, device_id, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id) DO UPDATE SET registration_id = $2, device_id = $3, updated_at = NOW()
	`, userID, req.RegistrationID, deviceID); err != nil {
		return nil, err
	}

	// Replace identity key
	if _, err := tx.Exec(ctx, `
		INSERT INTO identity_keys (user_id, public_key, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_id) DO UPDATE SET public_key = $2, updated_at = NOW()
	`, userID, req.IdentityKey); err != nil {
		return nil, err
	}

	// Replace signed prekey
	if _, err := tx.Exec(ctx, `
		INSERT INTO signed_prekeys (user_id, key_id, public_key, signature)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, key_id) DO UPDATE SET public_key = $3, signature = $4
	`, userID, req.SignedPreKey.KeyID, req.SignedPreKey.PublicKey, req.SignedPreKey.Signature); err != nil {
		return nil, err
	}

	// Append one-time prekeys (skip duplicates by (user_id, key_id))
	for _, otk := range req.OneTimePreKeys {
		if _, err := tx.Exec(ctx, `
			INSERT INTO one_time_prekeys (user_id, key_id, public_key)
			VALUES ($1, $2, $3)
			ON CONFLICT (user_id, key_id) DO NOTHING
		`, userID, otk.KeyID, otk.PublicKey); err != nil {
			return nil, err
		}
	}

	// Append Kyber prekeys
	for _, k := range req.KyberPreKeys {
		if _, err := tx.Exec(ctx, `
			INSERT INTO kyber_prekeys (user_id, key_id, public_key, signature)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id, key_id) DO NOTHING
		`, userID, k.KeyID, k.PublicKey, k.Signature); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return map[string]any{
		"identity_key":            true,
		"signed_prekey":           true,
		"one_time_prekeys_count":  len(req.OneTimePreKeys),
		"kyber_prekeys_count":     len(req.KyberPreKeys),
	}, nil
}

// GetKeyBundle returns the bundle for sender→target session setup. It POPs
// (deletes) one one-time prekey and one Kyber prekey so they are never reused.
func (s *KeyService) GetKeyBundle(ctx context.Context, targetUserID string) (*models.KeyBundleResponse, error) {
	resp := &models.KeyBundleResponse{UserID: targetUserID}

	// device + registration id
	if err := s.db.QueryRow(ctx, `
		SELECT registration_id, device_id FROM signal_devices WHERE user_id = $1
	`, targetUserID).Scan(&resp.RegistrationID, &resp.DeviceID); err != nil {
		// Default registration id 0, device id 1 — older clients may not have uploaded one
		resp.DeviceID = 1
	}

	if err := s.db.QueryRow(ctx,
		`SELECT public_key FROM identity_keys WHERE user_id = $1`, targetUserID,
	).Scan(&resp.IdentityKey); err != nil {
		return nil, errors.New("user has no identity key uploaded")
	}

	if err := s.db.QueryRow(ctx, `
		SELECT id, user_id, key_id, public_key, signature, created_at
		FROM signed_prekeys WHERE user_id = $1
		ORDER BY created_at DESC LIMIT 1
	`, targetUserID).Scan(&resp.SignedPreKey.ID, &resp.SignedPreKey.UserID, &resp.SignedPreKey.KeyID,
		&resp.SignedPreKey.PublicKey, &resp.SignedPreKey.Signature, &resp.SignedPreKey.CreatedAt); err != nil {
		return nil, errors.New("user has no signed prekey")
	}

	// POP one one-time prekey atomically
	var otk models.OneTimePreKey
	err := s.db.QueryRow(ctx, `
		DELETE FROM one_time_prekeys
		WHERE id = (
			SELECT id FROM one_time_prekeys
			WHERE user_id = $1 AND used = FALSE
			ORDER BY created_at ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, user_id, key_id, public_key, used, created_at
	`, targetUserID).Scan(&otk.ID, &otk.UserID, &otk.KeyID, &otk.PublicKey, &otk.Used, &otk.CreatedAt)
	if err == nil {
		resp.OneTimePreKey = &otk
	}

	// POP one Kyber prekey atomically
	var kpk models.KyberPreKey
	err = s.db.QueryRow(ctx, `
		DELETE FROM kyber_prekeys
		WHERE id = (
			SELECT id FROM kyber_prekeys
			WHERE user_id = $1 AND used = FALSE
			ORDER BY created_at ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, user_id, key_id, public_key, signature, used, created_at
	`, targetUserID).Scan(&kpk.ID, &kpk.UserID, &kpk.KeyID, &kpk.PublicKey, &kpk.Signature, &kpk.Used, &kpk.CreatedAt)
	if err == nil {
		resp.KyberPreKey = &kpk
	}

	return resp, nil
}

// GetKeyCounts returns remaining one-time and Kyber prekey counts.
func (s *KeyService) GetKeyCounts(ctx context.Context, userID string) (oneTime, kyber int, err error) {
	if err = s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM one_time_prekeys WHERE user_id = $1`, userID,
	).Scan(&oneTime); err != nil {
		return
	}
	err = s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM kyber_prekeys WHERE user_id = $1`, userID,
	).Scan(&kyber)
	return
}

func (s *KeyService) GetOneTimeKeyCount(ctx context.Context, userID string) (int, error) {
	one, _, err := s.GetKeyCounts(ctx, userID)
	return one, err
}
