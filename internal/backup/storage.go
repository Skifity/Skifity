// Package backup takes and restores backups of databases and volumes.
package backup

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// Storage is the configured S3-compatible destination.
type Storage struct {
	client *minio.Client
	bucket string
	// endpoint and region are kept for error messages.
	endpoint string
	region   string
}

// LoadStorage reads the storage settings and connects.
//
// It returns a catalogued problem rather than a bare error when nothing is
// configured, because "backups are not set up" is a normal state with a clear
// next step, not a failure.
func LoadStorage(ctx context.Context, db *store.DB, keyring *crypto.Keyring) (*Storage, error) {
	read := func(key string) (string, error) {
		value, encrypted, err := db.GetSetting(ctx, key)
		if err != nil {
			return "", err
		}
		if value == "" || !encrypted {
			return value, nil
		}
		plaintext, err := keyring.Open(value, settings.Context(key))
		if err != nil {
			return "", fmt.Errorf("read the setting %s: %w", key, err)
		}
		return string(plaintext), nil
	}

	endpoint, err := read(settings.KeyS3Endpoint)
	if err != nil {
		return nil, err
	}
	bucket, err := read(settings.KeyS3Bucket)
	if err != nil {
		return nil, err
	}
	accessKey, err := read(settings.KeyS3AccessKey)
	if err != nil {
		return nil, err
	}
	secretKey, err := read(settings.KeyS3SecretKey)
	if err != nil {
		return nil, err
	}
	region, err := read(settings.KeyS3Region)
	if err != nil {
		return nil, err
	}
	pathStyle, err := read(settings.KeyS3PathStyle)
	if err != nil {
		return nil, err
	}

	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		return nil, errdoc.StorageNotConfigured()
	}

	host, secure, err := splitEndpoint(endpoint)
	if err != nil {
		return nil, errdoc.NotConfigured("Backup storage", "Settings, then Storage").
			WithCause("The endpoint %q could not be read: %s", endpoint, err.Error())
	}

	client, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
		Region: region,
		// Most self-hosted S3 services, MinIO included, only speak path style.
		BucketLookup: bucketLookup(pathStyle),
	})
	if err != nil {
		return nil, fmt.Errorf("connect to the storage service: %w", err)
	}

	return &Storage{client: client, bucket: bucket, endpoint: endpoint, region: region}, nil
}

func bucketLookup(pathStyle string) minio.BucketLookupType {
	switch strings.ToLower(pathStyle) {
	case "true", "1", "yes", "on":
		return minio.BucketLookupPath
	case "false", "0", "no", "off":
		return minio.BucketLookupDNS
	default:
		// Auto works for AWS and for most others; an operator who needs path
		// style can say so explicitly.
		return minio.BucketLookupAuto
	}
}

// splitEndpoint turns a URL into the host and whether it is TLS.
func splitEndpoint(endpoint string) (host string, secure bool, err error) {
	if !strings.Contains(endpoint, "://") {
		// A bare host means TLS: refusing to guess plaintext is the safer
		// default, and MinIO over http needs the scheme spelled out.
		return endpoint, true, nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", false, err
	}
	if parsed.Host == "" {
		return "", false, fmt.Errorf("no host in %q", endpoint)
	}
	return parsed.Host, parsed.Scheme != "http", nil
}

// Verify checks that the bucket exists and can be written to.
//
// Writing and deleting a marker object is the only way to know: a bucket can
// exist and still reject writes, and finding that out at three in the morning
// when the scheduled backup runs is too late.
func (s *Storage) Verify(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return errdoc.New("backup.storage_unreachable", "The backup storage could not be reached").
			WithCause("Connecting to %s failed: %s", s.endpoint, err.Error()).
			WithImpact("Backups cannot run.").
			WithFix("Check the endpoint, the access key and that this server can reach the storage service.").
			Retry()
	}
	if !exists {
		return errdoc.New("backup.bucket_missing", "That bucket does not exist").
			WithCause("The bucket %q was not found at %s.", s.bucket, s.endpoint).
			WithImpact("Backups cannot run.").
			WithFix("Create the bucket in your storage provider, then try again. Skifity does not create buckets: doing so would need broader permissions than writing backups.")
	}

	marker := "skifity/.write-test"
	content := strings.NewReader("skifity write test")
	if _, err := s.client.PutObject(ctx, s.bucket, marker, content, int64(content.Len()),
		minio.PutObjectOptions{ContentType: "text/plain"}); err != nil {
		return errdoc.New("backup.storage_read_only", "The backup storage cannot be written to").
			WithCause("Writing a test object to %s failed: %s", s.bucket, err.Error()).
			WithImpact("Backups would fail.").
			WithFix("Give the access key permission to put objects in this bucket.")
	}
	if err := s.client.RemoveObject(ctx, s.bucket, marker, minio.RemoveObjectOptions{}); err != nil {
		// Being able to write but not delete still allows backups; retention
		// will not work, which is worth knowing but not worth refusing over.
		return errdoc.New("backup.storage_no_delete", "Old backups cannot be removed").
			WithCause("The access key can write to %s but cannot delete from it.", s.bucket).
			WithImpact("Backups will work, but old ones will never be cleaned up and the bucket will grow forever.").
			WithFix("Give the access key permission to delete objects, or set the retention policy in your storage provider instead.").
			WithSeverity(errdoc.SeverityWarning)
	}
	return nil
}

// presignExpiry is how long an upload or download URL stays valid. Long enough
// for a large dump, short enough that a leaked URL is not a lasting problem.
const presignExpiry = 6 * time.Hour

// PresignPut returns a URL a backup job can upload to.
//
// This is what keeps the storage credentials inside the panel: the namespace
// running the backup only ever sees a URL that expires (ADR-0012).
func (s *Storage) PresignPut(ctx context.Context, key string) (string, error) {
	presigned, err := s.client.PresignedPutObject(ctx, s.bucket, key, presignExpiry)
	if err != nil {
		return "", fmt.Errorf("prepare the upload: %w", err)
	}
	return presigned.String(), nil
}

// PresignGet returns a URL a restore job can download from.
func (s *Storage) PresignGet(ctx context.Context, key string) (string, error) {
	presigned, err := s.client.PresignedGetObject(ctx, s.bucket, key, presignExpiry, nil)
	if err != nil {
		return "", fmt.Errorf("prepare the download: %w", err)
	}
	return presigned.String(), nil
}

// Stat returns an object's size, which is how a finished backup's size is
// recorded without the job reporting it.
func (s *Storage) Stat(ctx context.Context, key string) (int64, error) {
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return 0, fmt.Errorf("read the backup's details: %w", err)
	}
	return info.Size, nil
}

// Remove deletes a backup object during retention.
func (s *Storage) Remove(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("delete the backup %s: %w", key, err)
	}
	return nil
}

// Bucket is the configured bucket name, for display.
func (s *Storage) Bucket() string { return s.bucket }

// ObjectKey builds the path a backup is stored at.
//
// The shape is deliberately readable in a bucket listing: an operator should be
// able to find a backup without the panel.
func ObjectKey(targetType, targetID, name string, at time.Time) string {
	return fmt.Sprintf("skifity/%s/%s/%s-%s.gz",
		targetType, targetID, at.UTC().Format("20060102-150405"), sanitise(name))
}

func sanitise(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == ' ' || r == '_' || r == '.':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "backup"
	}
	return out
}
