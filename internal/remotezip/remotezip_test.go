package remotezip

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestExtractSelectedUsesRanges(t *testing.T) {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for name, body := range map[string]string{
		"SarasaUiSC-Light.ttf":    strings.Repeat("L", 200000),
		"SarasaUiSC-SemiBold.ttf": strings.Repeat("S", 200000),
		"unused.ttf":              strings.Repeat("X", 2_000_000),
	} {
		w, _ := zw.Create(name)
		_, _ = io.WriteString(w, body)
	}
	_ = zw.Close()
	data := b.Bytes()
	var rangeRequests int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rng := r.Header.Get("Range")
		if rng == "" {
			http.Error(w, "range required", http.StatusBadRequest)
			return
		}
		rangeRequests++
		var start, end int
		if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); err != nil {
			http.Error(w, "bad range", 400)
			return
		}
		if start < 0 || end >= len(data) || start > end {
			http.Error(w, "bad range", 416)
			return
		}
		w.Header().Set("Content-Range", "bytes "+strconv.Itoa(start)+"-"+strconv.Itoa(end)+"/"+strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[start : end+1])
	}))
	defer ts.Close()
	dir := t.TempDir()
	fetched, err := ExtractSelected(context.Background(), ts.Client(), ts.URL, int64(len(data)), dir, []string{"SarasaUiSC-SemiBold.ttf"})
	if err != nil {
		t.Fatal(err)
	}
	if rangeRequests == 0 {
		t.Fatal("no range requests")
	}
	if fetched <= 0 || fetched > int64(len(data)) {
		t.Fatalf("fetched=%d zip=%d", fetched, len(data))
	}
	got, err := os.ReadFile(filepath.Join(dir, "SarasaUiSC-SemiBold.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 200000 || got[0] != 'S' {
		t.Fatal("wrong extracted file")
	}
	if _, err := os.Stat(filepath.Join(dir, "unused.ttf")); !os.IsNotExist(err) {
		t.Fatal("unused entry was extracted")
	}
}
