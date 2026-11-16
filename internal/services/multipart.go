package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/filetransfer/backend/internal/models"
)

func randomKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const (
	multipartURLTTL    = time.Hour
	downloadURLTTL     = 2 * time.Hour
	multipartMinPartMB = 4
	multipartMaxParts  = 10000
	multipartResumeTTL = 24 * time.Hour // an unfinished upload can be resumed this long
)

var allowedPartSizesMB = map[int]bool{4: true, 8: true, 16: true}

// InitiateMultipart starts an S3 multipart upload, persists tracking metadata,
// and returns presigned PUT URLs for every part.
func (s *FileService) InitiateMultipart(ctx context.Context, senderID string, req models.MultipartInitiateRequest) (*models.MultipartInitiateResponse, int, error) {
	if req.PartCount <= 0 || req.PartCount > multipartMaxParts {
		return nil, 400, fmt.Errorf("part_count must be 1..%d", multipartMaxParts)
	}
	if req.PartSize <= 0 || !allowedPartSizesMB[req.PartSize/(1024*1024)] {
		return nil, 400, errors.New("part_size must be 4, 8, or 16 MiB")
	}
	if req.FileSize <= 0 {
		return nil, 400, errors.New("file_size must be positive")
	}
	if req.FileSize > s.maxCiphertextSize() {
		return nil, 413, fmt.Errorf("file_size exceeds cap of %d bytes", s.maxCiphertextSize())
	}
	if int64(req.PartCount-1)*int64(req.PartSize) >= req.FileSize || int64(req.PartCount)*int64(req.PartSize) < req.FileSize {
		return nil, 400, errors.New("part_count does not match file_size / part_size")
	}
	// Attachments are client-encrypted: the server never records their real type.
	req.ContentType = "application/octet-stream"

	objectKey := fmt.Sprintf("uploads/%s/%s", senderID, randomKey())

	out, err := s.s3Client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(objectKey),
		ContentType: aws.String(req.ContentType),
	})
	if err != nil {
		return nil, 500, fmt.Errorf("s3 CreateMultipartUpload: %w", err)
	}

	uploadID := aws.ToString(out.UploadId)
	expiresAt := time.Now().Add(multipartResumeTTL)

	var recipientID any
	if req.RecipientID != "" {
		recipientID = req.RecipientID
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO multipart_uploads (upload_id, object_key, bucket, sender_id, recipient_id, file_size, part_count, part_size, content_type, status, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'in_progress', $10)
	`, uploadID, objectKey, s.bucket, senderID, recipientID, req.FileSize, req.PartCount, req.PartSize, req.ContentType, expiresAt); err != nil {
		// best-effort abort if persistence fails
		_, _ = s.s3Client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
			Bucket: aws.String(s.bucket), Key: aws.String(objectKey), UploadId: aws.String(uploadID),
		})
		return nil, 500, fmt.Errorf("persist upload: %w", err)
	}

	parts := make([]models.MultipartPartURL, 0, req.PartCount)
	for i := 1; i <= req.PartCount; i++ {
		req, err := s.presignClient.PresignUploadPart(ctx, &s3.UploadPartInput{
			Bucket:     aws.String(s.bucket),
			Key:        aws.String(objectKey),
			UploadId:   aws.String(uploadID),
			PartNumber: aws.Int32(int32(i)),
		}, s3.WithPresignExpires(multipartURLTTL))
		if err != nil {
			return nil, 500, fmt.Errorf("presign part %d: %w", i, err)
		}
		parts = append(parts, models.MultipartPartURL{PartNumber: i, PresignedURL: req.URL})
	}

	return &models.MultipartInitiateResponse{
		UploadID:         uploadID,
		Bucket:           s.bucket,
		ObjectKey:        objectKey,
		ExpiresInSeconds: int(multipartURLTTL.Seconds()),
		Parts:            parts,
	}, 200, nil
}

// CompleteMultipart finalizes the upload on S3 and records the file row.
func (s *FileService) CompleteMultipart(ctx context.Context, callerID string, req models.MultipartCompleteRequest) (*models.MultipartCompleteResponse, int, error) {
	var (
		senderID    string
		recipientID *string
		status      string
		bucket      string
		objectKey   string
	)
	err := s.db.QueryRow(ctx, `
		SELECT sender_id, recipient_id, status, bucket, object_key
		FROM multipart_uploads WHERE upload_id = $1
	`, req.UploadID).Scan(&senderID, &recipientID, &status, &bucket, &objectKey)
	if err != nil {
		return nil, 404, errors.New("upload_id not found")
	}
	if senderID != callerID {
		return nil, 403, errors.New("only the original sender may complete this upload")
	}
	if status == "aborted" {
		return nil, 409, errors.New("upload was aborted")
	}
	if status == "expired" {
		return nil, 410, errors.New("upload was auto-aborted by lifecycle policy")
	}
	if status == "completed" {
		// idempotent: return existing file row
		var fileID string
		var size int64
		var expiresAt *time.Time
		_ = s.db.QueryRow(ctx, `SELECT id, size_bytes, expires_at FROM sft_files WHERE object_key = $1`, objectKey).Scan(&fileID, &size, &expiresAt)
		return &models.MultipartCompleteResponse{Status: "ok", FileID: fileID, ObjectKey: objectKey, Bucket: bucket, SizeBytes: size, ExpiresAt: expiresAt}, 200, nil
	}

	if len(req.Parts) == 0 {
		return nil, 400, errors.New("parts is required")
	}

	sort.Slice(req.Parts, func(i, j int) bool { return req.Parts[i].PartNumber < req.Parts[j].PartNumber })
	completed := make([]s3types.CompletedPart, 0, len(req.Parts))
	for _, p := range req.Parts {
		completed = append(completed, s3types.CompletedPart{
			ETag:       aws.String(p.ETag),
			PartNumber: aws.Int32(int32(p.PartNumber)),
		})
	}

	_, err = s.s3Client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(bucket),
		Key:             aws.String(objectKey),
		UploadId:        aws.String(req.UploadID),
		MultipartUpload: &s3types.CompletedMultipartUpload{Parts: completed},
	})
	if err != nil {
		return nil, 409, fmt.Errorf("s3 CompleteMultipartUpload: %w", err)
	}

	// Read final size from S3 (HEAD)
	var sizeBytes int64
	if head, hErr := s.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(objectKey),
	}); hErr == nil && head.ContentLength != nil {
		sizeBytes = *head.ContentLength
	}

	var fileID string
	expiresAt := time.Now().Add(s.ttl()).UTC()
	err = s.db.QueryRow(ctx, `
		INSERT INTO sft_files (object_key, bucket, sender_id, recipient_id, size_bytes, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (object_key) DO UPDATE SET size_bytes = EXCLUDED.size_bytes
		RETURNING id, expires_at
	`, objectKey, bucket, senderID, recipientID, sizeBytes, expiresAt).Scan(&fileID, &expiresAt)
	if err != nil {
		return nil, 500, fmt.Errorf("persist sft_file: %w", err)
	}

	if _, err := s.db.Exec(ctx,
		`UPDATE multipart_uploads SET status = 'completed', completed_at = NOW() WHERE upload_id = $1`,
		req.UploadID,
	); err != nil {
		return nil, 500, fmt.Errorf("update multipart status: %w", err)
	}

	return &models.MultipartCompleteResponse{
		Status:    "ok",
		FileID:    fileID,
		ObjectKey: objectKey,
		Bucket:    bucket,
		SizeBytes: sizeBytes,
		ExpiresAt: &expiresAt,
	}, 200, nil
}

// PresignParts returns fresh upload URLs for the given parts of an in-progress
// upload, so a client can resume after the URLs from Initiate have expired.
func (s *FileService) PresignParts(ctx context.Context, callerID string, req models.MultipartPresignPartsRequest) (*models.MultipartPresignPartsResponse, int, error) {
	var senderID, status, bucket, objectKey string
	var partCount int
	err := s.db.QueryRow(ctx, `
		SELECT sender_id, status, bucket, object_key, part_count
		FROM multipart_uploads WHERE upload_id = $1
	`, req.UploadID).Scan(&senderID, &status, &bucket, &objectKey, &partCount)
	if err != nil {
		return nil, 404, errors.New("upload_id not found")
	}
	if senderID != callerID {
		return nil, 403, errors.New("only the original sender may upload parts")
	}
	if status != "in_progress" {
		return nil, 409, fmt.Errorf("upload is %s", status)
	}
	if len(req.PartNumbers) == 0 || len(req.PartNumbers) > multipartMaxParts {
		return nil, 400, errors.New("part_numbers is required")
	}

	parts := make([]models.MultipartPartURL, 0, len(req.PartNumbers))
	for _, n := range req.PartNumbers {
		if n < 1 || n > partCount {
			return nil, 400, fmt.Errorf("part_number %d out of range", n)
		}
		out, err := s.presignClient.PresignUploadPart(ctx, &s3.UploadPartInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String(objectKey),
			UploadId:   aws.String(req.UploadID),
			PartNumber: aws.Int32(int32(n)),
		}, s3.WithPresignExpires(multipartURLTTL))
		if err != nil {
			return nil, 500, fmt.Errorf("presign part %d: %w", n, err)
		}
		parts = append(parts, models.MultipartPartURL{PartNumber: n, PresignedURL: out.URL})
	}
	return &models.MultipartPresignPartsResponse{
		UploadID:         req.UploadID,
		ExpiresInSeconds: int(multipartURLTTL.Seconds()),
		Parts:            parts,
	}, 200, nil
}

// RunJanitor deletes attachments past their expiry and aborts uploads that were
// never finished, every interval until ctx is cancelled.
func (s *FileService) RunJanitor(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		s.cleanupExpired(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *FileService) cleanupExpired(ctx context.Context) {
	rows, err := s.db.Query(ctx, `
		SELECT id, bucket, object_key FROM sft_files
		WHERE NOT deleted AND expires_at IS NOT NULL AND expires_at < NOW()
		LIMIT 500`)
	if err == nil {
		type expired struct{ id, bucket, key string }
		var list []expired
		for rows.Next() {
			var e expired
			if rows.Scan(&e.id, &e.bucket, &e.key) == nil {
				list = append(list, e)
			}
		}
		rows.Close()
		for _, e := range list {
			if _, err := s.s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(e.bucket), Key: aws.String(e.key),
			}); err != nil {
				continue
			}
			_, _ = s.db.Exec(ctx, `UPDATE sft_files SET deleted = TRUE, deleted_at = NOW() WHERE id = $1`, e.id)
		}
		if len(list) > 0 {
			log.Printf("Janitor: deleted %d expired attachments", len(list))
		}
	}

	rows, err = s.db.Query(ctx, `
		SELECT upload_id, bucket, object_key FROM multipart_uploads
		WHERE status = 'in_progress' AND expires_at < NOW()
		LIMIT 500`)
	if err == nil {
		type stale struct{ id, bucket, key string }
		var list []stale
		for rows.Next() {
			var u stale
			if rows.Scan(&u.id, &u.bucket, &u.key) == nil {
				list = append(list, u)
			}
		}
		rows.Close()
		for _, u := range list {
			_, _ = s.s3Client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket: aws.String(u.bucket), Key: aws.String(u.key), UploadId: aws.String(u.id),
			})
			_, _ = s.db.Exec(ctx, `UPDATE multipart_uploads SET status = 'expired' WHERE upload_id = $1`, u.id)
		}
		if len(list) > 0 {
			log.Printf("Janitor: aborted %d stale uploads", len(list))
		}
	}
}

// AbortMultipart aborts the S3 upload and marks the row aborted. Idempotent.
func (s *FileService) AbortMultipart(ctx context.Context, callerID string, req models.MultipartAbortRequest) (int, error) {
	var senderID, status, bucket, objectKey string
	err := s.db.QueryRow(ctx, `
		SELECT sender_id, status, bucket, object_key
		FROM multipart_uploads WHERE upload_id = $1
	`, req.UploadID).Scan(&senderID, &status, &bucket, &objectKey)
	if err != nil {
		// idempotent: nothing to abort
		return 200, nil
	}
	if senderID != callerID {
		return 403, errors.New("only the original sender may abort this upload")
	}
	if status == "aborted" {
		return 200, nil
	}

	if _, err := s.s3Client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(objectKey), UploadId: aws.String(req.UploadID),
	}); err != nil {
		// log but don't fail — caller wants idempotency
		_ = err
	}

	if _, err := s.db.Exec(ctx,
		`UPDATE multipart_uploads SET status = 'aborted' WHERE upload_id = $1`, req.UploadID,
	); err != nil {
		return 500, fmt.Errorf("update multipart status: %w", err)
	}
	return 200, nil
}

// ListUploadedParts returns the parts S3 has acknowledged for a given upload.
func (s *FileService) ListUploadedParts(ctx context.Context, callerID, uploadID string) (*models.MultipartListPartsResponse, int, error) {
	var senderID, status, bucket, objectKey string
	err := s.db.QueryRow(ctx, `
		SELECT sender_id, status, bucket, object_key
		FROM multipart_uploads WHERE upload_id = $1
	`, uploadID).Scan(&senderID, &status, &bucket, &objectKey)
	if err != nil {
		return nil, 404, errors.New("upload_id not found")
	}
	if senderID != callerID {
		return nil, 403, errors.New("only the original sender may list parts")
	}
	if status == "expired" {
		return nil, 410, errors.New("upload was auto-aborted by lifecycle policy")
	}

	out, err := s.s3Client.ListParts(ctx, &s3.ListPartsInput{
		Bucket: aws.String(bucket), Key: aws.String(objectKey), UploadId: aws.String(uploadID),
	})
	if err != nil {
		return nil, 500, fmt.Errorf("s3 ListParts: %w", err)
	}

	parts := make([]models.MultipartListedPart, 0, len(out.Parts))
	for _, p := range out.Parts {
		var size int64
		if p.Size != nil {
			size = *p.Size
		}
		parts = append(parts, models.MultipartListedPart{
			PartNumber: int(aws.ToInt32(p.PartNumber)),
			ETag:       aws.ToString(p.ETag),
			SizeBytes:  size,
		})
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })

	return &models.MultipartListPartsResponse{UploadID: uploadID, Parts: parts}, 200, nil
}

// PresignMultipartDownload returns a long-lived GET URL for ranged downloads.
// Authorization: the caller must be the recorded recipient (or the sender, for self-test).
func (s *FileService) PresignMultipartDownload(ctx context.Context, callerID string, req models.MultipartDownloadRequest) (*models.MultipartDownloadResponse, int, error) {
	bucket := req.Bucket
	if bucket == "" {
		bucket = s.bucket
	}

	var senderID string
	var recipientID *string
	var sizeBytes int64
	var deleted bool
	err := s.db.QueryRow(ctx, `
		SELECT sender_id, recipient_id, size_bytes, deleted
		FROM sft_files WHERE object_key = $1
	`, req.ObjectKey).Scan(&senderID, &recipientID, &sizeBytes, &deleted)
	if err != nil {
		return nil, 404, errors.New("object_key not found")
	}
	if deleted {
		return nil, 410, errors.New("object has been deleted")
	}
	if callerID != senderID && (recipientID == nil || *recipientID != callerID) {
		return nil, 403, errors.New("not authorized to download this object")
	}

	out, err := s.presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(req.ObjectKey),
	}, s3.WithPresignExpires(downloadURLTTL))
	if err != nil {
		return nil, 500, fmt.Errorf("presign GET: %w", err)
	}

	return &models.MultipartDownloadResponse{
		PresignedURL:     out.URL,
		ExpiresInSeconds: int(downloadURLTTL.Seconds()),
		SizeBytes:        sizeBytes,
	}, 200, nil
}

// DeleteSftFile removes the assembled object from S3 and marks the row deleted. Idempotent.
func (s *FileService) DeleteSftFile(ctx context.Context, callerID, fileID string) (int, error) {
	var senderID, bucket, objectKey string
	var recipientID *string
	var deleted bool
	err := s.db.QueryRow(ctx, `
		SELECT sender_id, recipient_id, bucket, object_key, deleted
		FROM sft_files WHERE id = $1
	`, fileID).Scan(&senderID, &recipientID, &bucket, &objectKey, &deleted)
	if err != nil {
		// idempotent
		return 200, nil
	}
	if callerID != senderID && (recipientID == nil || *recipientID != callerID) {
		return 403, errors.New("only sender or recipient may delete this file")
	}
	if deleted {
		return 200, nil
	}

	if _, err := s.s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(objectKey),
	}); err != nil {
		return 500, fmt.Errorf("s3 DeleteObject: %w", err)
	}

	if _, err := s.db.Exec(ctx,
		`UPDATE sft_files SET deleted = TRUE, deleted_at = NOW() WHERE id = $1`, fileID,
	); err != nil {
		return 500, fmt.Errorf("update sft_file: %w", err)
	}
	return 200, nil
}
