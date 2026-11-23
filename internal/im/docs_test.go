package im

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeDocStore struct {
	urls  map[string]string
	saves int
	mu    sync.Mutex
}

func newFakeDocStore() *fakeDocStore { return &fakeDocStore{urls: map[string]string{}} }

func (f *fakeDocStore) CachedDoc(_ context.Context, materialID string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	url, ok := f.urls[materialID]
	return url, ok, nil
}

func (f *fakeDocStore) SaveDoc(_ context.Context, materialID, _ string, url string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls[materialID] = url
	f.saves++
	return nil
}

func (f *fakeDocStore) savesCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saves
}

type fakeMaterialReader struct {
	reads int
	mu    sync.Mutex
	body  []byte
}

func (f *fakeMaterialReader) MaterialMarkdown(context.Context, string) (string, []byte, error) {
	f.mu.Lock()
	f.reads++
	f.mu.Unlock()
	return "material", f.body, nil
}

type fakeImporter struct {
	imports int
	err     error
	url     string
	mu      sync.Mutex
}

func (f *fakeImporter) ImportMarkdown(context.Context, string, []byte) (string, error) {
	f.mu.Lock()
	f.imports++
	f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	if f.url != "" {
		return f.url, nil
	}
	return "https://example.feishu.cn/docx/abc", nil
}

func (f *fakeImporter) importsCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.imports
}

func newTestDocs(store DocStore, materials MaterialReader, importer DocImporter, logger *slog.Logger) *Docs {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return NewDocs(store, materials, importer, logger)
}

func logBuffer() (*bytes.Buffer, *slog.Logger) {
	buf := &bytes.Buffer{}
	return buf, slog.New(slog.NewTextHandler(buf, nil))
}

func TestEnsureImportsOnce(t *testing.T) {
	store := newFakeDocStore()
	materials := &fakeMaterialReader{body: []byte("# hi")}
	importer := &fakeImporter{}
	buf, logger := logBuffer()
	docs := newTestDocs(store, materials, importer, logger)

	first, err := docs.Ensure(context.Background(), "m001", "需求")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	second, err := docs.Ensure(context.Background(), "m001", "需求")
	if err != nil {
		t.Fatalf("Ensure second: %v", err)
	}
	if first != second {
		t.Errorf("urls differ: %q vs %q", first, second)
	}
	if importer.importsCount() != 1 {
		t.Errorf("imports = %d, want 1 (cached)", importer.importsCount())
	}
	if store.savesCount() != 1 {
		t.Errorf("saves = %d, want 1", store.savesCount())
	}
	if got := strings.Count(buf.String(), "im doc imported"); got != 1 {
		t.Errorf("`im doc imported` logged %d times, want 1\nlog: %s", got, buf.String())
	}
}

func TestEnsureSerialisesConcurrentImports(t *testing.T) {
	store := newFakeDocStore()
	materials := &fakeMaterialReader{body: []byte("# hi")}
	importer := &fakeImporter{}
	docs := newTestDocs(store, materials, importer, nil)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := docs.Ensure(context.Background(), "m001", "需求"); err != nil {
				t.Errorf("Ensure: %v", err)
			}
		}()
	}
	wg.Wait()

	if importer.importsCount() != 1 {
		t.Errorf("imports = %d, want 1 (single flight)", importer.importsCount())
	}
}

func TestEnsureRejectsEmptyMaterial(t *testing.T) {
	docs := newTestDocs(newFakeDocStore(), &fakeMaterialReader{body: nil}, &fakeImporter{}, nil)
	if _, err := docs.Ensure(context.Background(), "m001", "需求"); err == nil {
		t.Fatal("Ensure on empty material = nil error")
	}
}

func TestEnsureReturnsImporterFailure(t *testing.T) {
	store := newFakeDocStore()
	importer := &fakeImporter{err: errors.New("feishu: tenant token code=99991663 msg=invalid token")}
	docs := newTestDocs(store, &fakeMaterialReader{body: []byte("# hi")}, importer, nil)

	if _, err := docs.Ensure(context.Background(), "m001", "需求"); err == nil {
		t.Fatal("Ensure on failing importer = nil error")
	}
	if store.savesCount() != 0 {
		t.Errorf("saves = %d, want 0 (nothing to cache)", store.savesCount())
	}
	if url := docs.Cached(context.Background(), "m001"); url != "" {
		t.Errorf("Cached = %q after failed import, want empty", url)
	}
}

func TestCachedMissReturnsEmpty(t *testing.T) {
	docs := newTestDocs(newFakeDocStore(), &fakeMaterialReader{}, &fakeImporter{}, nil)
	if url := docs.Cached(context.Background(), "nope"); url != "" {
		t.Errorf("Cached = %q, want empty", url)
	}
}

// AC3: an empty folder token disables the feature instead of failing per card.
func TestNewDocServiceWithoutFolderToken(t *testing.T) {
	buf, logger := logBuffer()
	docs := NewDocService(Config{AppID: "cli_x", AppSecret: "s"}, newFakeDocStore(),
		&fakeMaterialReader{body: []byte("# hi")}, logger)
	if docs != nil {
		t.Fatalf("NewDocService without folder token = %v, want nil (feature off)", docs)
	}
	if !strings.Contains(buf.String(), "im doc folder token is empty") {
		t.Errorf("log missing `im doc folder token is empty`:\n%s", buf.String())
	}
}

func TestNewDocServiceWithFolderToken(t *testing.T) {
	docs := NewDocService(Config{AppID: "cli_x", AppSecret: "s", DocFolderToken: "fld_1"},
		newFakeDocStore(), &fakeMaterialReader{body: []byte("# hi")}, nil)
	if docs == nil {
		t.Fatal("NewDocService with folder token = nil, want the service")
	}
	// No network: the service is built without touching the tenant.
	if url := docs.Cached(context.Background(), "m001"); url != "" {
		t.Errorf("Cached = %q, want empty before any import", url)
	}
}
