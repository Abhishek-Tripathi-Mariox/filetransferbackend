package main

import (
	"log"
	"net/http"

	"github.com/filetransfer/backend/internal/config"
	"github.com/filetransfer/backend/internal/db"
	"github.com/filetransfer/backend/internal/handlers"
	"github.com/filetransfer/backend/internal/middleware"
	"github.com/filetransfer/backend/internal/services"
	"github.com/filetransfer/backend/internal/ws"
)

func main() {
	cfg := config.Load()

	// Database
	pool := db.Connect(cfg.DatabaseURL)
	defer pool.Close()
	db.RunMigrations(pool)

	// Services
	authSvc := services.NewAuthService(pool, cfg.JWTSecret)
	keySvc := services.NewKeyService(pool)
	msgSvc := services.NewMessageService(pool)
	fileSvc := services.NewFileService(pool, cfg.AWSRegion, cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, cfg.S3Bucket)

	// WebSocket hub
	hub := ws.NewHub()
	wsHandler := ws.NewHandler(hub, authSvc, msgSvc, fileSvc)

	// HTTP handlers
	authHandler := handlers.NewAuthHandler(authSvc)
	keyHandler := handlers.NewKeyHandler(keySvc)
	fileHandler := handlers.NewFileHandler(fileSvc)
	multipartHandler := handlers.NewMultipartHandler(fileSvc)
	msgHandler := handlers.NewMessageHandler(msgSvc, fileSvc)
	statusHandler := handlers.NewStatusHandler(wsHandler)

	// Auth middleware
	authMW := middleware.AuthMiddleware(authSvc)

	mux := http.NewServeMux()

	// Public routes
	mux.HandleFunc("POST /api/auth/register", authHandler.Register)
	mux.HandleFunc("POST /api/auth/login", authHandler.Login)

	// WebSocket (auth via query param)
	mux.HandleFunc("/ws", wsHandler.ServeWS)

	// Protected routes
	protected := http.NewServeMux()
	protected.HandleFunc("POST /api/keys/upload", keyHandler.UploadKeys)
	protected.HandleFunc("GET /api/keys/bundle/{userID}", keyHandler.GetKeyBundle)
	protected.HandleFunc("GET /api/keys/count", keyHandler.GetKeyCounts)
	protected.HandleFunc("POST /api/files/{fileTransferID}/downloaded", fileHandler.MarkDownloaded)
	protected.HandleFunc("POST /api/files/{fileTransferID}/download", fileHandler.GetPresignedDownloadURL)
	protected.HandleFunc("POST /api/files/presign", fileHandler.GetPresignedUploadURL)
	protected.HandleFunc("POST /api/files/presign-download", fileHandler.PresignDownloadByKey)

	// Secure File Transfer (multipart) — order matters: more-specific paths first
	protected.HandleFunc("POST /api/files/multipart/initiate", multipartHandler.Initiate)
	protected.HandleFunc("POST /api/files/multipart/complete", multipartHandler.Complete)
	protected.HandleFunc("POST /api/files/multipart/abort", multipartHandler.Abort)
	protected.HandleFunc("POST /api/files/multipart/download", multipartHandler.Download)
	protected.HandleFunc("GET /api/files/multipart/{uploadId}/parts", multipartHandler.ListParts)
	protected.HandleFunc("DELETE /api/files/{fileId}", multipartHandler.Delete)
	protected.HandleFunc("GET /api/messages/unread", msgHandler.GetUnreadCounts)
	protected.HandleFunc("GET /api/messages/{roomID}", msgHandler.GetMessages)
	protected.HandleFunc("GET /api/rooms", msgHandler.GetRooms)
	protected.HandleFunc("POST /api/rooms", msgHandler.CreateRoom)
	protected.HandleFunc("GET /api/users", msgHandler.GetUsers)
	protected.HandleFunc("POST /api/users/online", statusHandler.GetOnlineStatus)

	mux.Handle("/api/", authMW(protected))

	// CORS wrapper
	handler := corsMiddleware(mux)

	log.Printf("Server starting on :%s", cfg.ServerPort)
	if err := http.ListenAndServe(":"+cfg.ServerPort, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
