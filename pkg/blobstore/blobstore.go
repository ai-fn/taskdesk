// Package blobstore is a thin S3-compatible object-store client used for
// presigned, direct-to-client uploads/downloads. It works against Cloudflare R2
// (prod) and MinIO (dev) since both speak the S3 API. The service never streams
// bytes through itself: it hands the client a presigned URL and the client
// transfers directly to/from the store.
package blobstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Store is the subset of object-store operations the todo service needs.
type Store interface {
	// PresignPut returns a URL the client can PUT to for up to ttl. The signed
	// request pins Content-Type and Content-Length, so the store rejects any other size.
	PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (string, error)
	// PresignGet returns a short-lived download URL that forces a download (Content-Disposition:
	// attachment) and serves only allowlisted content types, anything else as octet-stream.
	PresignGet(ctx context.Context, key, filename, contentType string, ttl time.Duration) (string, error)
	// Head returns the stored object's size, or an error if it does not exist.
	Head(ctx context.Context, key string) (int64, error)
	// Delete removes the object (no error if already absent).
	Delete(ctx context.Context, key string) error
	// InitiateMultipart starts an S3 multipart upload and returns the upload ID.
	// The upload ID must be stored in the DB; it is passed to PresignUploadPart and
	// CompleteMultipart.
	InitiateMultipart(ctx context.Context, key, contentType string) (uploadID string, err error)
	// PresignUploadPart returns a presigned URL the client can PUT a single part to.
	// partNumber is 1-based as required by S3.
	PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32, ttl time.Duration) (string, error)
	// CompleteMultipart finalises a multipart upload. parts must be sorted by
	// PartNumber (ascending) as required by S3.
	CompleteMultipart(ctx context.Context, key, uploadID string, parts []CompletedPart) error
	// AbortMultipart cancels an incomplete multipart upload and frees staged parts.
	AbortMultipart(ctx context.Context, key, uploadID string) error
	// Get downloads the object and returns its byte stream.
	// The caller must close the returned ReadCloser after use.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

// CompletedPart carries the part number and ETag returned by the client after
// a successful part PUT, needed to finalise a multipart upload.
type CompletedPart struct {
	PartNumber int32
	ETag       string
}

// Config configures an S3Store.
type Config struct {
	Endpoint string `env:"BLOB_ENDPOINT" default:"http://minio.data.svc.cluster.local:9000"`
	// PublicEndpoint is the host presigned URLs point at; it must be reachable by the browser
	// (the in-cluster Endpoint is not). Empty = same as Endpoint.
	PublicEndpoint string `env:"BLOB_PUBLIC_ENDPOINT" default:""`
	Region         string `env:"BLOB_REGION"          default:"auto"`
	Bucket         string `env:"BLOB_BUCKET"          default:"attachments"`
	AccessKey      string `env:"BLOB_ACCESS_KEY,required"`
	SecretKey      string `env:"BLOB_SECRET_KEY,required"`
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
	newClient := func(endpoint string) *s3.Client {
		return s3.New(s3.Options{
			BaseEndpoint: aws.String(endpoint),
			Region:       cfg.Region,
			UsePathStyle: true,
			Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		})
	}
	// A presigned signature covers the host, so URLs must be signed against the public endpoint.
	public := cfg.PublicEndpoint
	if public == "" {
		public = cfg.Endpoint
	}
	return &S3Store{client: newClient(cfg.Endpoint), presign: s3.NewPresignClient(newClient(public)), bucket: cfg.Bucket}
}

// EnsureBucket creates the bucket if missing and applies a CORS policy that lets the browser PUT/GET
// directly and read the ETag of each multipart part. Intended for dev/MinIO; prod buckets are provisioned.
func (s *S3Store) EnsureBucket(ctx context.Context, origins []string) error {
	_, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.bucket)})
	var owned *types.BucketAlreadyOwnedByYou
	if err != nil && !errors.As(err, &owned) {
		return fmt.Errorf("create bucket: %w", err)
	}
	_, err = s.client.PutBucketCors(ctx, &s3.PutBucketCorsInput{
		Bucket: aws.String(s.bucket),
		CORSConfiguration: &types.CORSConfiguration{CORSRules: []types.CORSRule{{
			AllowedOrigins: origins,
			AllowedMethods: []string{"GET", "PUT"},
			AllowedHeaders: []string{"*"},
			ExposeHeaders:  []string{"ETag"},
			MaxAgeSeconds:  aws.Int32(3600),
		}}},
	})
	if err != nil {
		return fmt.Errorf("put bucket cors: %w", err)
	}
	return nil
}

func (s *S3Store) PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(size),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign put: %w", err)
	}
	return req.URL, nil
}

func (s *S3Store) PresignGet(ctx context.Context, key, filename, contentType string, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     aws.String(s.bucket),
		Key:                        aws.String(key),
		ResponseContentDisposition: aws.String(contentDisposition(filename)),
		ResponseContentType:        aws.String(SafeDownloadType(contentType)),
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
	return aws.ToInt64(out.ContentLength), nil
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

func (s *S3Store) InitiateMultipart(ctx context.Context, key, contentType string) (string, error) {
	out, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("initiate multipart: %w", err)
	}
	return aws.ToString(out.UploadId), nil
}

func (s *S3Store) PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(s.bucket),
		Key:        aws.String(key),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(partNumber),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign upload part: %w", err)
	}
	return req.URL, nil
}

func (s *S3Store) CompleteMultipart(ctx context.Context, key, uploadID string, parts []CompletedPart) error {
	completed := make([]types.CompletedPart, len(parts))
	for i, p := range parts {
		completed[i] = types.CompletedPart{
			PartNumber: aws.Int32(p.PartNumber),
			ETag:       aws.String(p.ETag),
		}
	}
	_, err := s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(s.bucket),
		Key:             aws.String(key),
		UploadId:        aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: completed},
	})
	if err != nil {
		return fmt.Errorf("complete multipart: %w", err)
	}
	return nil
}

func (s *S3Store) AbortMultipart(ctx context.Context, key, uploadID string) error {
	_, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	var gone *types.NoSuchUpload
	if err != nil && !errors.As(err, &gone) { // already completed or aborted
		return fmt.Errorf("abort multipart: %w", err)
	}
	return nil
}

func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("get object %s: %w", key, err)
	}
	return out.Body, nil
}

// Key builds a residency-scoped, content-addressed-safe object key. The filename
// is slugified: spaces and slashes in the filename are replaced with '-';
// uniqueness comes from attID.
func Key(region, userID, taskID, attID, filename string) string {
	safe := strings.NewReplacer(" ", "-", "/", "-").Replace(strings.TrimSpace(filename))
	return fmt.Sprintf("%s/%s/%s/%s/%s", region, userID, taskID, attID, safe)
}

// safeTypes are served as-is; everything else (html, svg, xml, js, unknown) becomes octet-stream so a
// stored file can never render as active content on the storage origin.
var safeTypes = map[string]bool{
	"application/pdf": true, "application/zip": true, "application/json": true,
	"text/plain": true, "text/csv": true,
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/avif": true,
	"audio/mpeg": true, "audio/ogg": true, "audio/wav": true, "audio/mp4": true,
	"video/mp4": true, "video/webm": true, "video/quicktime": true,
}

// SafeDownloadType returns the media type to serve a stored file with.
func SafeDownloadType(contentType string) string {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil || !safeTypes[mt] {
		return "application/octet-stream"
	}
	return mt
}

// contentDisposition builds an attachment header with control characters stripped and non-ASCII
// names RFC 2231-encoded.
func contentDisposition(filename string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, filename)
	if clean == "" {
		clean = "download"
	}
	if d := mime.FormatMediaType("attachment", map[string]string{"filename": clean}); d != "" {
		return d
	}
	return "attachment"
}
