package transitclosing

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func TestSpacesReadBackAndACL(t *testing.T) {
	var stored []byte
	var acl, content, disposition string
	corrupt := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			var err error
			stored, err = io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			acl = r.Header.Get("X-Amz-Acl")
			content = r.Header.Get("Content-Type")
			disposition = r.Header.Get("Content-Disposition")
			w.Header().Set("ETag", `"test"`)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Last-Modified", "Mon, 05 Oct 2026 00:00:00 GMT")
		w.Header().Set("ETag", `"test"`)
		if corrupt {
			_, _ = w.Write([]byte("corrupt"))
		} else {
			_, _ = w.Write(stored)
		}
	}))
	defer server.Close()
	client, err := minio.New(strings.TrimPrefix(server.URL, "https://"), &minio.Options{Creds: credentials.NewStaticV4("access", "secret", ""), Secure: true, Region: "us-east-1", Transport: server.Client().Transport})
	if err != nil {
		t.Fatal(err)
	}
	store := &Spaces{client: client, bucket: "test", publicBase: server.URL + "/test"}
	file := filepath.Join(t.TempDir(), "report.csv")
	if err = os.WriteFile(file, []byte("month,trips\n2026-08,10\n"), 0600); err != nil {
		t.Fatal(err)
	}
	obj, err := store.UploadVerified(context.Background(), "release/report.csv", file, true)
	if err != nil {
		t.Fatal(err)
	}
	if acl != "public-read" || content != "text/csv; charset=utf-8" || disposition != "attachment; filename=report.csv" || obj.Bytes != int64(len("month,trips\n2026-08,10\n")) {
		t.Fatalf("metadata: %s %s %s %+v", acl, content, disposition, obj)
	}
	if _, err = store.UploadVerified(context.Background(), "raw/archive.csv.gz", file, false); err != nil {
		t.Fatal(err)
	}
	if acl != "private" {
		t.Fatal("raw archive public", acl)
	}
	if _, err = store.UploadVerified(context.Background(), "archive/transit_reports_v1.zip", file, true); err != nil {
		t.Fatal(err)
	}
	if content != "application/zip" || acl != "public-read" || disposition != "attachment; filename=transit_reports_v1.zip" {
		t.Fatalf("ZIP metadata: %s %s %s", content, acl, disposition)
	}
	corrupt = true
	if _, err = store.UploadVerified(context.Background(), "raw/archive.csv.gz", file, false); err == nil {
		t.Fatal("accepted corrupt remote data")
	}
}
