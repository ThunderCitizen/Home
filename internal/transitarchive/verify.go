package transitarchive

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func verify(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m.Version != 1 || len(m.Tables) == 0 {
		return nil, fmt.Errorf("unsupported or empty manifest")
	}
	if _, err = time.Parse("2006-01", m.Month); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, t := range m.Tables {
		if (t.Schema != "transit" && t.Schema != "gtfs") || t.Name == "" || t.File != t.Schema+"__"+t.Name+".csv.gz" || filepath.Base(t.File) != t.File || seen[t.File] || len(t.Columns) == 0 || t.Rows < 0 {
			return nil, fmt.Errorf("invalid table entry %q", t.File)
		}
		seen[t.File] = true
		if err = verifyTable(dir, t); err != nil {
			return nil, err
		}
	}
	return &m, nil
}
func verifyTable(dir string, t Table) error {
	f, err := os.Open(filepath.Join(dir, t.File))
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	gz, err := gzip.NewReader(io.TeeReader(f, h))
	if err != nil {
		return err
	}
	defer gz.Close()
	r := csv.NewReader(gz)
	r.FieldsPerRecord = len(t.Columns)
	var count int64
	for {
		_, err = r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%s: %w", t.File, err)
		}
		count++
	}
	// Consume any buffered/trailing compressed bytes into the checksum as well.
	if _, err = io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != t.SHA256 || count != t.Rows {
		return fmt.Errorf("checksum or row count mismatch: %s", t.File)
	}
	return nil
}
