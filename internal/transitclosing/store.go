package transitclosing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"thundercitizen/internal/config"
)

type Object struct {
	Key    string `json:"key"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type Store interface {
	UploadVerified(context.Context, string, string, bool) (Object, error)
	PublicURL(string) string
}
type Spaces struct {
	client             *minio.Client
	bucket, publicBase string
}

// StoreFromConfig is disabled until a bucket is configured. Credentials use
// the existing secret-file support; raw objects always retain private ACLs.
func StoreFromConfig() (Store, error) {
	bucket := config.Secret("TRANSIT_ARCHIVE_BUCKET", "")
	if bucket == "" {
		return nil, nil
	}
	endpoint := config.Secret("TRANSIT_ARCHIVE_ENDPOINT", "tor1.digitaloceanspaces.com")
	access, secret := config.Secret("AWS_ACCESS_KEY_ID", ""), config.Secret("AWS_SECRET_ACCESS_KEY", "")
	if access == "" || secret == "" {
		return nil, fmt.Errorf("TRANSIT_ARCHIVE_BUCKET requires AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY")
	}
	c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: true})
	if err != nil {
		return nil, err
	}
	return &Spaces{client: c, bucket: bucket, publicBase: "https://" + bucket + "." + endpoint}, nil
}
func (s *Spaces) PublicURL(prefix string) string {
	return s.publicBase + "/" + strings.Trim(prefix, "/")
}
func (s *Spaces) UploadVerified(ctx context.Context, key, file string, public bool) (Object, error) {
	f, err := os.Open(file)
	if err != nil {
		return Object{}, err
	}
	defer f.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, f)
	if err != nil {
		return Object{}, err
	}
	expected := hex.EncodeToString(hash.Sum(nil))
	acl, content := "private", "application/octet-stream"
	if public {
		acl = "public-read"
		content = "text/csv; charset=utf-8"
		if strings.HasSuffix(key, ".zip") {
			content = "application/zip"
		}
	}
	opts := minio.PutObjectOptions{ContentType: content, UserMetadata: map[string]string{"x-amz-acl": acl, "sha256": expected}}
	if public {
		opts.ContentDisposition = "attachment; filename=" + path.Base(key)
		opts.CacheControl = "public, max-age=31536000, immutable"
	}
	if _, err = s.client.FPutObject(ctx, s.bucket, key, file, opts); err != nil {
		return Object{}, err
	}
	remote, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return Object{}, err
	}
	defer remote.Close()
	hash.Reset()
	got, err := io.Copy(hash, remote)
	if err != nil {
		return Object{}, err
	}
	if got != size || hex.EncodeToString(hash.Sum(nil)) != expected {
		return Object{}, fmt.Errorf("uploaded object failed read-back verification: %s", key)
	}
	return Object{Key: key, SHA256: expected, Bytes: size}, nil
}
