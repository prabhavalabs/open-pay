package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/openlankapay/openlankapay/pkg/auth"
	"github.com/openlankapay/openlankapay/pkg/storage"
	"github.com/openlankapay/openlankapay/services/merchant/internal/domain"
)

// DocumentRepository persists uploaded document metadata.
type DocumentRepository interface {
	Create(ctx context.Context, doc *domain.Document) error
	ListByMerchant(ctx context.Context, merchantID uuid.UUID) ([]*domain.Document, error)
	DeleteByKey(ctx context.Context, merchantID uuid.UUID, objectKey string) error
}

// FileUploadHandler handles file upload operations.
type FileUploadHandler struct {
	minioClient *minio.Client
	bucketName  string
	docs        DocumentRepository
}

// NewFileUploadHandler creates a FileUploadHandler backed by the S3-compatible
// store in cfg, creating the bucket (private) when it is missing.
func NewFileUploadHandler(cfg storage.Config, docs DocumentRepository) (*FileUploadHandler, error) {
	client, err := storage.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	if err := storage.EnsureBucket(context.Background(), client, cfg); err != nil {
		return nil, err
	}

	return &FileUploadHandler{
		minioClient: client,
		bucketName:  cfg.Bucket,
		docs:        docs,
	}, nil
}

// MinioClient returns the MinIO client used by this handler.
func (h *FileUploadHandler) MinioClient() *minio.Client { return h.minioClient }

// BucketName returns the bucket name used by this handler.
func (h *FileUploadHandler) BucketName() string { return h.bucketName }

// RegisterUploadRoutes adds upload routes to the router.
func RegisterUploadRoutes(r chi.Router, h *FileUploadHandler, jwtSecret string) {
	r.Group(func(r chi.Router) {
		r.Use(auth.JWTMiddleware(jwtSecret))
		r.Post("/v1/uploads", h.UploadFile)
		r.Delete("/v1/uploads", h.DeleteFile)
	})
}

// UploadFile handles POST /v1/uploads - multipart file upload.
func (h *FileUploadHandler) UploadFile(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authentication")
		return
	}

	// 32 MB max
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "file too large or invalid multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "MISSING_FILE", "file field is required")
		return
	}
	defer func() { _ = file.Close() }()

	// Validate file type
	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowedExts := map[string]string{
		".pdf":  "application/pdf",
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
	}
	contentType, allowed := allowedExts[ext]
	if !allowed {
		writeError(w, http.StatusBadRequest, "INVALID_FILE_TYPE", "only PDF, PNG, JPG files are allowed")
		return
	}

	// Get document category from form
	category := r.FormValue("category")
	if category == "" {
		category = "general"
	}

	// Generate unique object key
	objectKey := fmt.Sprintf("%s/%s/%s%s", claims.MerchantID.String(), category, uuid.New().String(), ext)

	// Upload to MinIO
	_, err = h.minioClient.PutObject(r.Context(), h.bucketName, objectKey, file, header.Size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "UPLOAD_FAILED", "failed to upload file")
		return
	}

	// Store document metadata in DB
	if h.docs != nil {
		doc := &domain.Document{
			ID:          uuid.New(),
			MerchantID:  claims.MerchantID,
			Category:    category,
			Filename:    header.Filename,
			ObjectKey:   objectKey,
			ContentType: contentType,
			FileSize:    header.Size,
		}
		if err := h.docs.Create(r.Context(), doc); err != nil {
			// Log but don't fail — file is already uploaded
			fmt.Printf("warning: failed to store document metadata: %v\n", err)
		}
	}

	// The bucket is private, so return the gateway path that serves the
	// object rather than a store URL the browser cannot reach.
	fileURL := "/v1/assets/" + objectKey

	writeJSON(w, http.StatusOK, envelope{
		"data": map[string]string{
			"url":      fileURL,
			"key":      objectKey,
			"filename": header.Filename,
			"category": category,
		},
	})
}

// DeleteFile handles DELETE /v1/uploads - remove a file.
func (h *FileUploadHandler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	_, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authentication")
		return
	}

	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "key is required")
		return
	}

	if err := h.minioClient.RemoveObject(r.Context(), h.bucketName, body.Key, minio.RemoveObjectOptions{}); err != nil {
		writeError(w, http.StatusInternalServerError, "DELETE_FAILED", "failed to delete file")
		return
	}

	writeJSON(w, http.StatusOK, envelope{"data": map[string]string{"status": "deleted"}})
}
