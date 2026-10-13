// transitarchive makes verified evidence copies; it never removes raw data.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"thundercitizen/internal/config"
	"thundercitizen/internal/transitarchive"
)

func main() {
	action := flag.String("action", "verify", "export, verify, upload, or restore (never evicts data)")
	dir := flag.String("dir", "", "archive directory; export requires a new directory")
	month := flag.String("month", "", "completed month YYYY-MM")
	grace := flag.Duration("grace", 48*time.Hour, "minimum delay after month-end 04:00 local service boundary")
	schema := flag.String("schema", "", "new transit_archive_* schema for restore")
	endpoint := flag.String("endpoint", "tor1.digitaloceanspaces.com", "Spaces endpoint")
	bucket := flag.String("bucket", "", "private archive bucket for upload")
	prefix := flag.String("prefix", "transit-archives", "object prefix")
	flag.Parse()
	if *dir == "" {
		check(fmt.Errorf("-dir is required"))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	switch *action {
	case "verify":
		m, err := transitarchive.Verify(*dir)
		check(err)
		fmt.Printf("Verified %s: %d tables\n", m.Month, len(m.Tables))
	case "upload":
		check(upload(ctx, *dir, *endpoint, *bucket, *prefix))
	case "export", "restore":
		url := os.Getenv("DATABASE_URL")
		if url == "" {
			check(fmt.Errorf("DATABASE_URL is required"))
		}
		conn, err := pgx.Connect(ctx, url)
		check(err)
		defer conn.Close(ctx)
		if *action == "export" {
			_, err = transitarchive.Export(ctx, conn, *dir, *month, *grace)
			check(err)
			_, err = transitarchive.Verify(*dir)
			check(err)
			fmt.Println("Verified snapshot exported. Raw data remains in production; this is not a closed month.")
		} else {
			check(transitarchive.Restore(ctx, conn, *dir, *schema))
			fmt.Println("Restored into isolated analysis schema", *schema)
		}
	default:
		check(fmt.Errorf("unknown action %q", *action))
	}
}
func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func upload(ctx context.Context, dir, endpoint, bucket, prefix string) error {
	if bucket == "" {
		return fmt.Errorf("-bucket is required; use a private archive bucket")
	}
	m, err := transitarchive.Verify(dir)
	if err != nil {
		return err
	}
	access, secret := config.Secret("AWS_ACCESS_KEY_ID", ""), config.Secret("AWS_SECRET_ACCESS_KEY", "")
	if access == "" || secret == "" {
		return fmt.Errorf("Spaces credentials are required")
	}
	c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: true})
	if err != nil {
		return err
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(manifest)
	root := strings.Trim(prefix, "/") + "/" + m.Month + "/" + hex.EncodeToString(sum[:]) + "/"
	names := make([]string, 0, len(m.Tables)+1)
	for _, t := range m.Tables {
		names = append(names, t.File)
	}
	names = append(names, "manifest.json") // Publish manifest only after all payloads verify.
	for _, name := range names {
		path := filepath.Join(dir, name)
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		h := sha256.New()
		_, e = io.Copy(h, f)
		f.Close()
		if e != nil {
			return e
		}
		expected := hex.EncodeToString(h.Sum(nil))
		_, err = c.FPutObject(ctx, bucket, root+name, path, minio.PutObjectOptions{ContentType: "application/octet-stream", UserMetadata: map[string]string{"x-amz-acl": "private", "sha256": expected}})
		if err != nil {
			return err
		}
		remote, e := c.GetObject(ctx, bucket, root+name, minio.GetObjectOptions{})
		if e != nil {
			return e
		}
		h.Reset()
		_, e = io.Copy(h, remote)
		remote.Close()
		if e != nil {
			return e
		}
		if hex.EncodeToString(h.Sum(nil)) != expected {
			return fmt.Errorf("remote checksum mismatch: %s", name)
		}
	}
	fmt.Printf("Uploaded and read-back verified s3://%s/%s (raw production data retained)\n", bucket, root)
	return nil
}
