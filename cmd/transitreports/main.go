// transitreports publishes a trailing-year ZIP of two public summary CSVs.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"io"
	"os"
	"path/filepath"
	"strings"
	"thundercitizen/internal/config"
	"thundercitizen/internal/transit"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dir := flag.String("dir", "", "new output directory")
	bucket := flag.String("bucket", "", "Spaces bucket; omit to export locally only")
	endpoint := flag.String("endpoint", "tor1.digitaloceanspaces.com", "Spaces endpoint")
	basePrefix := flag.String("prefix", "transit/reports", "object directory prefix")
	flag.Parse()
	if *dir == "" {
		return fmt.Errorf("-dir is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	if err := os.Mkdir(*dir, 0755); err != nil {
		return err
	}
	names := []string{transit.ReportBundleFilename()}
	f, err := os.Create(filepath.Join(*dir, names[0]))
	if err != nil {
		return err
	}
	err = transit.WriteReportBundle(ctx, conn, f, time.Now())
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if *bucket == "" {
		fmt.Println("Reports exported to", *dir)
		return nil
	}
	client, err := minio.New(*endpoint, &minio.Options{Creds: credentials.NewStaticV4(config.Secret("AWS_ACCESS_KEY_ID", ""), config.Secret("AWS_SECRET_ACCESS_KEY", ""), ""), Secure: true})
	if err != nil {
		return err
	}
	prefix := strings.Trim(*basePrefix, "/") + "/archive/" + time.Now().UTC().Format("20060102T150405.000000000Z") + "/"
	for _, name := range names {
		_, err := client.FPutObject(ctx, *bucket, prefix+name, filepath.Join(*dir, name), minio.PutObjectOptions{ContentType: "application/zip", ContentDisposition: "attachment; filename=" + name, CacheControl: "public, max-age=31536000, immutable", UserMetadata: map[string]string{"x-amz-acl": "public-read"}})
		if err != nil {
			return err
		}
		local, err := os.ReadFile(filepath.Join(*dir, name))
		if err != nil {
			return err
		}
		expected := sha256.Sum256(local)
		remote, err := client.GetObject(ctx, *bucket, prefix+name, minio.GetObjectOptions{})
		if err != nil {
			return err
		}
		hash := sha256.New()
		size, err := io.Copy(hash, remote)
		remote.Close()
		if err != nil {
			return err
		}
		if size != int64(len(local)) || !bytes.Equal(hash.Sum(nil), expected[:]) {
			return fmt.Errorf("uploaded report failed read-back verification: %s", name)
		}
	}
	fmt.Printf("Publish this configuration after the ZIP upload verifies:\nTRANSIT_REPORT_BASE_URL=https://%s.%s/%s\n", *bucket, *endpoint, prefix)
	return nil
}
