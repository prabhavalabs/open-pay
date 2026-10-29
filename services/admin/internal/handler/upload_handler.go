package handler

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/openlankapay/openlankapay/pkg/auth"
	"github.com/openlankapay/openlankapay/pkg/storage"
)

// AdminUploadHandler handles file uploads for admin assets (logos, etc).
type AdminUploadHandler struct {
	minioClient *minio.Client
	bucketName  string
}

// NewAdminUploadHandler creates an upload handler backed by the S3-compatible
// store in cfg, creating the bucket (private) when it is missing.
func NewAdminUploadHandler(cfg storage.Config) (*AdminUploadHandler, error) {
	client, err := storage.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	if err := storage.EnsureBucket(context.Background(), client, cfg); err != nil {
		return nil, err
	}

	return &AdminUploadHandler{
		minioClient: client,
		bucketName:  cfg.Bucket,
	}, nil
}

// Upload handles POST /v1/admin/uploads — upload admin assets.
func (h *AdminUploadHandler) Upload(w http.ResponseWriter, r *http.Request) {
	_, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authentication")
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10MB max for logos
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "file too large or invalid form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "MISSING_FILE", "file field is required")
		return
	}
	defer func() { _ = file.Close() }()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowedExts := map[string]string{
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".svg":  "image/svg+xml",
		".webp": "image/webp",
		".ico":  "image/x-icon",
		".pdf":  "application/pdf",
	}
	contentType, allowed := allowedExts[ext]
	if !allowed {
		writeError(w, http.StatusBadRequest, "INVALID_FILE_TYPE", "only PNG, JPG, SVG, WebP, ICO, PDF files are allowed")
		return
	}

	category := r.FormValue("category")
	if category == "" {
		category = "branding"
	}

	objectKey := fmt.Sprintf("admin/%s/%s%s", category, uuid.New().String(), ext)

	_, err = h.minioClient.PutObject(r.Context(), h.bucketName, objectKey, file, header.Size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "UPLOAD_FAILED", "failed to upload file")
		return
	}

	// Return a gateway-relative URL to avoid cross-origin issues with direct MinIO access
	fileURL := fmt.Sprintf("/v1/assets/%s", objectKey)

	writeJSON(w, http.StatusOK, envelope{
		"data": map[string]string{
			"url":      fileURL,
			"key":      objectKey,
			"filename": header.Filename,
		},
	})
}

// ServeAsset handles GET /v1/assets/* — serves uploaded files from the object store.
func (h *AdminUploadHandler) ServeAsset(w http.ResponseWriter, r *http.Request) {
	objectKey := chi.URLParam(r, "*")
	if objectKey == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "asset path is required")
		return
	}

	obj, err := h.minioClient.GetObject(r.Context(), h.bucketName, objectKey, minio.GetObjectOptions{})
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "asset not found")
		return
	}
	defer func() { _ = obj.Close() }()

	info, err := obj.Stat()
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "asset not found")
		return
	}

	w.Header().Set("Content-Type", info.ContentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, info.Key, info.LastModified, obj)
}
