// Package blobstore is a thin S3-compatible object-store client used for
// presigned, direct-to-client uploads/downloads. It works against Cloudflare R2
// (prod) and MinIO (dev) since both speak the S3 API. The service never streams
// bytes through itself: it hands the client a presigned URL and the client
// transfers directly to/from the store.
package blobstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Store is the subset of object-store operations the todo service needs.
type Store interface {
	// PresignPut returns a URL the client can PUT to for up to ttl. The signed
	// request pins Content-Type. Size enforcement is deferred to the confirm
	// step, which calls Head to verify the stored byte count.
	PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error)
	// PresignGet returns a short-lived download URL.
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	// Head returns the stored object's size, or an error if it does not exist.
	Head(ctx context.Context, key string) (int64, error)
	// Delete removes the object (no error if already absent).
	Delete(ctx context.Context, key string) error
}

// Config configures an S3Store.
type Config struct {
	Endpoint  string `env:"BLOB_ENDPOINT"   default:"http://minio.data.svc.cluster.local:9000"`
	Region    string `env:"BLOB_REGION"     default:"auto"`
	Bucket    string `env:"BLOB_BUCKET"     default:"attachments"`
	AccessKey string `env:"BLOB_ACCESS_KEY" default:"minioadmin"`
	SecretKey string `env:"BLOB_SECRET_KEY" default:"minioadmin"`
}

// S3Store implements Store against any S3-compatible endpoint.
type S3Store struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

var _ Store = (*S3Store)(nil)

// New builds an S3Store. UsePathStyle is required for MinIO and harmless for R2.
func New(cfg Config) *S3Store {
	client := s3.New(s3.Options{
		EndpointResolver: s3.EndpointResolverFromURL(cfg.Endpoint),
		Region:           cfg.Region,
		UsePathStyle:     true,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.AccessKey, cfg.SecretKey, ""),
	})
	return &S3Store{client: client, presign: s3.NewPresignClient(client), bucket: cfg.Bucket}
}

func (s *S3Store) PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign put: %w", err)
	}
	return req.URL, nil
}

func (s *S3Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign get: %w", err)
	}
	return req.URL, nil
}

func (s *S3Store) Head(ctx context.Context, key string) (int64, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return 0, fmt.Errorf("head object: %w", err)
	}
	return out.ContentLength, nil
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

// Key builds a residency-scoped, content-addressed-safe object key. The filename
// is slugified: spaces and slashes in the filename are replaced with '-';
// uniqueness comes from attID.
func Key(region, userID, taskID, attID, filename string) string {
	safe := strings.NewReplacer(" ", "-", "/", "-").Replace(strings.TrimSpace(filename))
	return fmt.Sprintf("%s/%s/%s/%s/%s", region, userID, taskID, attID, safe)
}
