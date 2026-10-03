package remotezip

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/progress"
)

const (
	defaultBlockSize = int64(2 << 20) // 2 MiB HTTP range blocks
	defaultMaxBlocks = 12             // ~24 MiB cache
	rangeAttempts    = 4              // per block, with backoff
	userAgent        = "yahei-sarasa-go"
)

type cacheBlock struct {
	data []byte
	used uint64
}

// ReaderAt presents a remote HTTP object as io.ReaderAt using bounded block
// caching. archive/zip can therefore read only the central directory and the
// compressed ranges belonging to selected entries.
type ReaderAt struct {
	ctx       context.Context
	client    *http.Client
	url       string
	size      int64
	blockSize int64
	maxBlocks int

	mu      sync.Mutex
	cache   map[int64]*cacheBlock
	clock   uint64
	fetched int64
}

func NewReaderAt(ctx context.Context, client *http.Client, url string, size int64) (*ReaderAt, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if url == "" {
		return nil, errors.New("remotezip: empty URL")
	}
	if size <= 0 {
		return nil, errors.New("remotezip: asset size must be known")
	}
	return &ReaderAt{ctx: ctx, client: client, url: url, size: size, blockSize: defaultBlockSize, maxBlocks: defaultMaxBlocks, cache: make(map[int64]*cacheBlock)}, nil
}

func (r *ReaderAt) Size() int64         { return r.size }
func (r *ReaderAt) BytesFetched() int64 { r.mu.Lock(); defer r.mu.Unlock(); return r.fetched }

func (r *ReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("remotezip: negative offset")
	}
	if off >= r.size {
		return 0, io.EOF
	}
	want := len(p)
	if int64(want) > r.size-off {
		want = int(r.size - off)
	}
	read := 0
	for read < want {
		pos := off + int64(read)
		blockIndex := pos / r.blockSize
		block, err := r.getBlock(blockIndex)
		if err != nil {
			return read, err
		}
		within := int(pos - blockIndex*r.blockSize)
		n := copy(p[read:want], block[within:])
		if n == 0 {
			return read, io.ErrUnexpectedEOF
		}
		read += n
	}
	if read < len(p) {
		return read, io.EOF
	}
	return read, nil
}

func (r *ReaderAt) getBlock(index int64) ([]byte, error) {
	r.mu.Lock()
	if b, ok := r.cache[index]; ok {
		r.clock++
		b.used = r.clock
		data := b.data
		r.mu.Unlock()
		return data, nil
	}
	r.mu.Unlock()

	start := index * r.blockSize
	if start >= r.size {
		return nil, io.EOF
	}
	end := start + r.blockSize - 1
	if end >= r.size {
		end = r.size - 1
	}

	data, err := r.fetchRange(start, end)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.clock++
	r.cache[index] = &cacheBlock{data: data, used: r.clock}
	r.fetched += int64(len(data))
	if len(r.cache) > r.maxBlocks {
		r.evictOneLocked(index)
	}
	r.mu.Unlock()
	return data, nil
}

// fetchRange GETs bytes [start,end] with bounded retries. Transient network
// failures and 5xx/429 answers are retried with backoff; a server that ignores
// Range is a hard error because silently reading a full 700 MiB body is what
// this package exists to avoid.
func (r *ReaderAt) fetchRange(start, end int64) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < rangeAttempts; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt*attempt) * 700 * time.Millisecond
			select {
			case <-r.ctx.Done():
				return nil, r.ctx.Err()
			case <-time.After(delay):
			}
		}
		data, retry, err := r.fetchRangeOnce(start, end)
		if err == nil {
			return data, nil
		}
		lastErr = err
		if !retry || r.ctx.Err() != nil {
			break
		}
	}
	if r.ctx.Err() != nil {
		return nil, r.ctx.Err()
	}
	return nil, lastErr
}

func (r *ReaderAt) fetchRangeOnce(start, end int64) (data []byte, retry bool, err error) {
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	req.Header.Set("User-Agent", userAgent)
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("remotezip: range %d-%d: %s", start, end, resp.Status)
	}
	if resp.StatusCode != http.StatusPartialContent {
		return nil, false, fmt.Errorf("remotezip: server ignored byte range %d-%d: %s", start, end, resp.Status)
	}
	wantLen := end - start + 1
	data, err = io.ReadAll(io.LimitReader(resp.Body, wantLen+1))
	if err != nil {
		return nil, true, err
	}
	if int64(len(data)) != wantLen {
		return nil, true, fmt.Errorf("remotezip: short range %d-%d: got %d bytes want %d", start, end, len(data), wantLen)
	}
	return data, false, nil
}

func (r *ReaderAt) evictOneLocked(keep int64) {
	var victim int64
	var oldest uint64 = ^uint64(0)
	found := false
	for idx, b := range r.cache {
		if idx == keep {
			continue
		}
		if b.used < oldest {
			victim, oldest, found = idx, b.used, true
		}
	}
	if found {
		delete(r.cache, victim)
	}
}

// ExtractSelected fetches only ZIP ranges required for requested basenames.
func ExtractSelected(ctx context.Context, client *http.Client, url string, size int64, dst string, wanted []string, sink progress.Sink) (int64, error) {
	ra, err := NewReaderAt(ctx, client, url, size)
	if err != nil {
		return 0, err
	}
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return ra.BytesFetched(), fmt.Errorf("remotezip: open central directory: %w", err)
	}
	err = extractFrom(zr.File, dst, wanted, sink)
	return ra.BytesFetched(), err
}

// ExtractLocal extracts the requested basenames from a ZIP on disk (for a
// Sarasa package the user downloaded with a browser).
func ExtractLocal(zipPath, dst string, wanted []string, sink progress.Sink) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	return extractFrom(zr.File, dst, wanted, sink)
}

func extractFrom(files []*zip.File, dst string, wanted []string, sink progress.Sink) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	need := make(map[string]bool, len(wanted))
	for _, n := range wanted {
		need[n] = true
	}
	for _, zf := range files {
		base := filepath.Base(filepath.FromSlash(zf.Name))
		if !need[base] || zf.FileInfo().IsDir() {
			continue
		}
		sink.Infof("正在解出 %s（%.1f MiB）", base, float64(zf.UncompressedSize64)/(1<<20))
		if err := extractOne(zf, filepath.Join(dst, base), sink); err != nil {
			return err
		}
		delete(need, base)
		if len(need) == 0 {
			break
		}
	}
	if len(need) != 0 {
		missing := make([]string, 0, len(need))
		for n := range need {
			missing = append(missing, n)
		}
		sort.Strings(missing)
		return fmt.Errorf("remotezip: archive missing requested files: %v", missing)
	}
	return nil
}

// extractOne writes to a temporary sibling and renames on success, so an
// interrupted download never leaves a truncated font in the cache. The zip
// reader verifies CRC-32 at EOF, so a renamed file is a verified copy.
func extractOne(zf *zip.File, path string, sink progress.Sink) error {
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	tmp := path + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	pw := &progressWriter{name: filepath.Base(path), total: int64(zf.UncompressedSize64), sink: sink}
	if _, err := io.Copy(io.MultiWriter(out, pw), rc); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	pw.finish()
	ok = true
	return nil
}

type progressWriter struct {
	name        string
	total, done int64
	lastAt      time.Time
	sink        progress.Sink
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if time.Since(p.lastAt) > 200*time.Millisecond {
		p.sink.Progress(p.name, p.done, p.total)
		p.lastAt = time.Now()
	}
	return len(b), nil
}

func (p *progressWriter) finish() { p.sink.Progress(p.name, p.total, p.total) }

// ProbeSize can be used for explicit URLs. It prefers HEAD, then a 0-0 range.
func ProbeSize(ctx context.Context, client *http.Client, url string) (int64, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	req.Header.Set("User-Agent", userAgent)
	if resp, err := client.Do(req); err == nil {
		_ = resp.Body.Close()
		if resp.ContentLength > 0 {
			return resp.ContentLength, nil
		}
	}
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	cr := resp.Header.Get("Content-Range")
	parts := strings.Split(cr, "/")
	if len(parts) != 2 {
		return 0, fmt.Errorf("remotezip: no object size in Content-Range %q", cr)
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("remotezip: invalid object size %q", parts[1])
	}
	return n, nil
}
