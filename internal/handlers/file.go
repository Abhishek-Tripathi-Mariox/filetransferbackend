package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/filetransfer/backend/internal/middleware"
	"github.com/filetransfer/backend/internal/services"
)

type FileHandler struct {
	fileSvc *services.FileService
}

func NewFileHandler(fileSvc *services.FileService) *FileHandler {
	return &FileHandler{fileSvc: fileSvc}
}

// GetPresignedUploadURL handles POST /api/files/presign
type PresignRequest struct {
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
}

func (h *FileHandler) GetPresignedUploadURL(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())

	var req PresignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.FileName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file_name is required"})
		return
	}

	s3Key := "uploads/" + userID + "/" + req.FileName

	presignedURL, err := h.fileSvc.GetPresignedUploadURL(r.Context(), s3Key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate upload URL"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"upload_url": presignedURL,
		"s3_key":     s3Key,
	})
}

// GetPresignedDownloadURL handles POST /api/files/{fileTransferID}/download
func (h *FileHandler) GetPresignedDownloadURL(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())
	fileTransferID := r.PathValue("fileTransferID")
	if fileTransferID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file_transfer_id is required"})
		return
	}

	ft, receiverID, err := h.fileSvc.GetFileTransfer(r.Context(), fileTransferID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file transfer not found"})
		return
	}

	if receiverID != userID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "unauthorized: only the receiver can download"})
		return
	}

	if ft.Downloaded {
		writeJSON(w, http.StatusGone, map[string]string{"error": "file already downloaded and deleted"})
		return
	}

	downloadURL, err := h.fileSvc.GetPresignedDownloadURL(r.Context(), ft.S3Key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate download URL"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"download_url": downloadURL,
		"file_name":    ft.FileName,
		"file_size":    ft.FileSize,
	})
}

// MarkDownloaded handles POST /api/files/{fileTransferID}/downloaded
// Frontend calls this AFTER the file has been successfully downloaded.
// This deletes the file from S3 and marks it in DB.
func (h *FileHandler) MarkDownloaded(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())
	fileTransferID := r.PathValue("fileTransferID")
	if fileTransferID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file_transfer_id is required"})
		return
	}

	if err := h.fileSvc.MarkDownloadedAndDelete(r.Context(), fileTransferID, userID); err != nil {
		status := http.StatusInternalServerError
		switch err.Error() {
		case "unauthorized: only the receiver can trigger deletion":
			status = http.StatusForbidden
		case "file transfer not found":
			status = http.StatusNotFound
		case "file already downloaded and deleted":
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "file deleted from S3"})
}
