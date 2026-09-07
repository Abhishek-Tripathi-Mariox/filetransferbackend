package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/filetransfer/backend/internal/middleware"
	"github.com/filetransfer/backend/internal/models"
	"github.com/filetransfer/backend/internal/services"
)

type MultipartHandler struct {
	fileSvc *services.FileService
}

func NewMultipartHandler(fileSvc *services.FileService) *MultipartHandler {
	return &MultipartHandler{fileSvc: fileSvc}
}

// POST /api/files/multipart/initiate
func (h *MultipartHandler) Initiate(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())
	var req models.MultipartInitiateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	resp, status, err := h.fileSvc.InitiateMultipart(r.Context(), userID, req)
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, status, resp)
}

// POST /api/files/multipart/complete
func (h *MultipartHandler) Complete(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())
	var req models.MultipartCompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	resp, status, err := h.fileSvc.CompleteMultipart(r.Context(), userID, req)
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, status, resp)
}

// POST /api/files/multipart/abort
func (h *MultipartHandler) Abort(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())
	var req models.MultipartAbortRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	status, err := h.fileSvc.AbortMultipart(r.Context(), userID, req)
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, status, map[string]string{"status": "aborted"})
}

// GET /api/files/multipart/{uploadId}/parts
func (h *MultipartHandler) ListParts(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())
	uploadID := r.PathValue("uploadId")
	if uploadID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "upload_id is required"})
		return
	}
	resp, status, err := h.fileSvc.ListUploadedParts(r.Context(), userID, uploadID)
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, status, resp)
}

// POST /api/files/multipart/download
func (h *MultipartHandler) Download(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())
	var req models.MultipartDownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.ObjectKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "object_key is required"})
		return
	}
	resp, status, err := h.fileSvc.PresignMultipartDownload(r.Context(), userID, req)
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, status, resp)
}

// DELETE /api/files/{fileId}
func (h *MultipartHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r.Context())
	fileID := r.PathValue("fileId")
	if fileID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "fileId is required"})
		return
	}
	status, err := h.fileSvc.DeleteSftFile(r.Context(), userID, fileID)
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, status, map[string]string{"status": "deleted"})
}
