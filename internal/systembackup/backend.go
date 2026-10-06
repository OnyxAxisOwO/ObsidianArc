package systembackup

import (
	"context"
	"errors"
	"fmt"
	"io"
)

const (
	StorageTypeS3     = "s3"
	StorageTypeWebDAV = "webdav"
)

// StorageBackend is the common interface implemented by all backup storage
// providers (S3-compatible, WebDAV).
type StorageBackend interface {
	PutObject(ctx context.Context, key string, body io.Reader, size int64, payloadHash string) error
	DeleteObject(ctx context.Context, key string) error
	ListObjects(ctx context.Context, prefix string, visit func(listedObject) error) error
}

func newBackend(ctx context.Context, cfg Config) (StorageBackend, error) {
	switch cfg.Type {
	case StorageTypeWebDAV:
		return NewWebDAVClient(cfg)
	case StorageTypeS3, "":
		return NewS3Client(cfg)
	default:
		return nil, fmt.Errorf("unsupported backup storage type: %s", cfg.Type)
	}
}

// ValidateConfig validates the storage configuration based on its chosen type.
func ValidateConfig(cfg Config) error {
	switch cfg.Type {
	case StorageTypeWebDAV:
		return validateWebDAVConfig(cfg)
	case StorageTypeS3, "":
		return validateS3Config(cfg)
	default:
		return invalidConfig(errors.New("unsupported storage type: " + cfg.Type).Error())
	}
}
