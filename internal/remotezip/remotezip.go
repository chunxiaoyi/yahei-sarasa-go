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
)

const (
	defaultBlockSize = int64(2 << 20) // 2 MiB HTTP range blocks
	defaultMaxBlocks = 12             // ~24 MiB cache
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

	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	req.Header.Set("User-Agent", "yahei-sarasa-semibold-go")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("remotezip: server ignored byte range %d-%d: %s", start, end, resp.Status)
	}
	wantLen := end - start + 1
	data, err := io.ReadAll(io.LimitReader(resp.Body, wantLen+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != wantLen {
		return nil, fmt.Errorf("remotezip: short range %d-%d: got %d bytes want %d", start, end, len(data), wantLen)
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
func ExtractSelected(ctx context.Context, client *http.Client, url string, size int64, dst string, wanted []string) (int64, error) {
	ra, err := NewReaderAt(ctx, client, url, size)
	if err != nil {
		return 0, err
	}
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return ra.BytesFetched(), fmt.Errorf("remotezip: open central directory: %w", err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return ra.BytesFetched(), err
	}

	need := make(map[string]bool, len(wanted))
	for _, n := range wanted {
		need[n] = true
	}
	for _, zf := range zr.File {
		base := filepath.Base(filepath.FromSlash(zf.Name))
		if !need[base] || zf.FileInfo().IsDir() {
			continue
		}
		fmt.Printf("========> Extracting %-28s %6.1f MiB\n", base, float64(zf.UncompressedSize64)/(1<<20))
		if err := extractOne(zf, filepath.Join(dst, base)); err != nil {
			return ra.BytesFetched(), err
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
		return ra.BytesFetched(), fmt.Errorf("remotezip: archive missing requested files: %v", missing)
	}
	return ra.BytesFetched(), nil
}

func extractOne(zf *zip.File, path string) error {
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	pw := &progressWriter{name: filepath.Base(path), total: int64(zf.UncompressedSize64), lastAt: time.Now()}
	if _, err := io.Copy(io.MultiWriter(out, pw), rc); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	fmt.Println()
	ok = true
	return nil
}

type progressWriter struct {
	name        string
	total, done int64
	last        int
	lastAt      time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	percent := 0
	if p.total > 0 {
		percent = int(p.done * 100 / p.total)
	}
	if percent >= p.last+10 || time.Since(p.lastAt) > 2*time.Second || p.done == p.total {
		fmt.Printf("\r           %-28s %3d%%", p.name, percent)
		p.last, p.lastAt = percent, time.Now()
	}
	return len(b), nil
}

// ProbeSize can be used for explicit URLs. It prefers HEAD, then a 0-0 range.
func ProbeSize(ctx context.Context, client *http.Client, url string) (int64, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	req.Header.Set("User-Agent", "yahei-sarasa-semibold-go")
	if resp, err := client.Do(req); err == nil {
		_ = resp.Body.Close()
		if resp.ContentLength > 0 {
			return resp.ContentLength, nil
		}
	}
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("User-Agent", "yahei-sarasa-semibold-go")
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
