package config

import (
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL        string
	JWTSecret          string
	AWSRegion          string
	AWSAccessKeyID     string
	AWSSecretAccessKey string
	S3Bucket           string
	ServerPort         string

	// Attachment limits, served to clients by GET /api/config (spec: remote config).
	MaxFileSizeBytes int64 // largest plaintext attachment
	MinFileSizeBytes int64 // empty files are rejected
	MaxFilesPerSend  int
	MaxFileNameBytes int
	ThumbnailMaxB    int // inline thumbnail byte cap
	ThumbnailMaxPx   int // inline thumbnail longest side
	FileTTLHours     int // how long an undownloaded attachment stays on S3
}

func Load() *Config {
	godotenv.Load()

	return &Config{
		DatabaseURL:        getEnv("DATABASE_URL", "postgres://user:password@localhost:5432/filetransfer?sslmode=disable"),
		JWTSecret:          getEnv("JWT_SECRET", "dev-secret-change-me"),
		AWSRegion:          getEnv("AWS_REGION", "ap-south-1"),
		AWSAccessKeyID:     getEnv("AWS_ACCESS_KEY_ID", ""),
		AWSSecretAccessKey: getEnv("AWS_SECRET_ACCESS_KEY", ""),
		S3Bucket:           getEnv("AWS_S3_BUCKET_NAME", "bank-ster-dev"),
		ServerPort:         getEnv("SERVER_PORT", "8080"),

		MaxFileSizeBytes: getEnvInt64("MAX_FILE_SIZE_BYTES", 2*1024*1024*1024),
		MinFileSizeBytes: getEnvInt64("MIN_FILE_SIZE_BYTES", 1),
		MaxFilesPerSend:  int(getEnvInt64("MAX_FILES_PER_SEND", 30)),
		MaxFileNameBytes: int(getEnvInt64("MAX_FILE_NAME_BYTES", 255)),
		ThumbnailMaxB:    int(getEnvInt64("THUMBNAIL_MAX_BYTES", 16*1024)),
		ThumbnailMaxPx:   int(getEnvInt64("THUMBNAIL_MAX_PX", 256)),
		FileTTLHours:     int(getEnvInt64("FILE_TTL_HOURS", 30*24)),
	}
}

func getEnvInt64(key string, fallback int64) int64 {
	if v, err := strconv.ParseInt(os.Getenv(key), 10, 64); err == nil && v > 0 {
		return v
	}
	return fallback
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
