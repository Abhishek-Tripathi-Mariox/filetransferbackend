package services

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/filetransfer/backend/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type FileService struct {
	db            *pgxpool.Pool
	s3Client      *s3.Client
	presignClient *s3.PresignClient
	bucket        string
	region        string
}

func NewFileService(db *pgxpool.Pool, region, accessKeyID, secretAccessKey, bucket string) *FileService {
	var opts []func(*awsconfig.LoadOptions) error
	opts = append(opts, awsconfig.WithRegion(region))

	if accessKeyID != "" && secretAccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
		))
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		log.Fatalf("Failed to load AWS config: %v", err)
	}

	s3Client := s3.NewFromConfig(cfg)

	return &FileService{
		db:            db,
		s3Client:      s3Client,
		presignClient: s3.NewPresignClient(s3Client),
		bucket:        bucket,
		region:        region,
	}
}

// Bucket returns the default S3 bucket name configured for this service.
func (s *FileService) Bucket() string { return s.bucket }

// Region returns the AWS region configured for this service.
func (s *FileService) Region() string { return s.region }

// GetPresignedUploadURL generates a presigned PUT URL for uploading a file to S3.
func (s *FileService) GetPresignedUploadURL(ctx context.Context, s3Key string) (string, error) {
	req, err := s.presignClient.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s3Key),
	}, s3.WithPresignExpires(15*time.Minute))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// GetPresignedDownloadURL generates a presigned GET URL for downloading a file from S3.
func (s *FileService) GetPresignedDownloadURL(ctx context.Context, s3Key string) (string, error) {
	req, err := s.presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s3Key),
	}, s3.WithPresignExpires(15*time.Minute))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// GetFileTransfer returns a file transfer and the receiver ID of its associated message.
func (s *FileService) GetFileTransfer(ctx context.Context, fileTransferID string) (*models.FileTransfer, string, error) {
	var ft models.FileTransfer
	var receiverID string
	err := s.db.QueryRow(ctx, `
		SELECT ft.id, ft.message_id, ft.encrypted_s3_url, ft.s3_key, ft.file_name, ft.file_size,
		       ft.content_type, ft.content_subtype, ft.caption, ft.width, ft.height, ft.duration, ft.thumbnail,
		       ft.bucket, ft.region, ft.downloaded, ft.deleted_from_s3, ft.created_at, m.receiver_id
		FROM file_transfers ft
		JOIN messages m ON m.id = ft.message_id
		WHERE ft.id = $1
	`, fileTransferID).Scan(&ft.ID, &ft.MessageID, &ft.EncryptedS3URL, &ft.S3Key, &ft.FileName,
		&ft.FileSize, &ft.ContentType, &ft.ContentSubtype, &ft.Caption, &ft.Width, &ft.Height, &ft.Duration, &ft.Thumbnail,
		&ft.Bucket, &ft.Region, &ft.Downloaded, &ft.DeletedFromS3, &ft.CreatedAt, &receiverID)
	if err != nil {
		return nil, "", err
	}
	return &ft, receiverID, nil
}

// StoreFileTransfer records a file transfer entry in the database.
func (s *FileService) StoreFileTransfer(ctx context.Context, messageID string, payload models.SendFilePayload) (*models.FileTransfer, error) {
	bucket := payload.Bucket
	if bucket == "" {
		bucket = s.bucket
	}
	region := payload.Region
	if region == "" {
		region = s.region
	}

	var ft models.FileTransfer
	err := s.db.QueryRow(ctx, `
		INSERT INTO file_transfers (message_id, encrypted_s3_url, s3_key, file_name, file_size, content_type, content_subtype, caption, width, height, duration, thumbnail, bucket, region)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id, message_id, encrypted_s3_url, s3_key, file_name, file_size, content_type, content_subtype, caption, width, height, duration, thumbnail, bucket, region, downloaded, deleted_from_s3, created_at
	`, messageID, payload.EncryptedS3URL, payload.S3Key, payload.FileName, payload.FileSize,
		payload.ContentType, payload.ContentSubtype, payload.Caption, payload.Width, payload.Height, payload.Duration, payload.Thumbnail,
		bucket, region,
	).Scan(&ft.ID, &ft.MessageID, &ft.EncryptedS3URL, &ft.S3Key, &ft.FileName, &ft.FileSize,
		&ft.ContentType, &ft.ContentSubtype, &ft.Caption, &ft.Width, &ft.Height, &ft.Duration, &ft.Thumbnail,
		&ft.Bucket, &ft.Region, &ft.Downloaded, &ft.DeletedFromS3, &ft.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &ft, nil
}

// MarkDownloadedAndDelete deletes the file object from S3 only.
// The database is left untouched so the frontend can still render local data.
// Both the main attachment and its thumbnail (if any) are removed — uploads
// with a thumbnail result in two S3 objects, and both must be cleaned up.
func (s *FileService) MarkDownloadedAndDelete(ctx context.Context, fileTransferID, requesterID string) error {
	var ft models.FileTransfer
	var receiverID string
	err := s.db.QueryRow(ctx, `
		SELECT ft.id, ft.s3_key, ft.thumbnail, ft.bucket, m.receiver_id
		FROM file_transfers ft
		JOIN messages m ON m.id = ft.message_id
		WHERE ft.id = $1
	`, fileTransferID).Scan(&ft.ID, &ft.S3Key, &ft.Thumbnail, &ft.Bucket, &receiverID)
	if err != nil {
		return errors.New("file transfer not found")
	}

	if receiverID != requesterID {
		return errors.New("unauthorized: only the receiver can trigger deletion")
	}

	bucket := ft.Bucket
	if bucket == "" {
		bucket = s.bucket
	}

	keys := []string{ft.S3Key}
	if ft.Thumbnail != "" && ft.Thumbnail != ft.S3Key {
		keys = append(keys, ft.Thumbnail)
	}

	for _, key := range keys {
		if _, delErr := s.s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		}); delErr != nil {
			log.Printf("Warning: failed to delete S3 object %s: %v", key, delErr)
		}
	}
	return nil
}
