package im

import (
	"context"
	"io"
	"log/slog"
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
	mu      sync.Mutex
}

func (f *fakeImporter) ImportMarkdown(context.Context, string, []byte) (string, error) {
	f.mu.Lock()
	f.imports++
	f.mu.Unlock()
	return "https://example.feishu.cn/docx/abc", nil
}

func newTestDocs(store DocStore, materials MaterialReader, importer DocImporter) *Docs {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewDocs(store, materials, importer, logger)
}

func TestEnsureImportsOnce(t *testing.T) {
	store := newFakeDocStore()
	materials := &fakeMaterialReader{body: []byte("# hi")}
	importer := &fakeImporter{}
	docs := newTestDocs(store, materials, importer)

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
	if importer.imports != 1 {
		t.Errorf("imports = %d, want 1 (cached)", importer.imports)
	}
	if store.saves != 1 {
		t.Errorf("saves = %d, want 1", store.saves)
	}
}

func TestEnsureSerialisesConcurrentImports(t *testing.T) {
	store := newFakeDocStore()
	materials := &fakeMaterialReader{body: []byte("# hi")}
	importer := &fakeImporter{}
	docs := newTestDocs(store, materials, importer)

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

	if importer.imports != 1 {
		t.Errorf("imports = %d, want 1 (single flight)", importer.imports)
	}
}

func TestEnsureRejectsEmptyMaterial(t *testing.T) {
	docs := newTestDocs(newFakeDocStore(), &fakeMaterialReader{body: nil}, &fakeImporter{})
	if _, err := docs.Ensure(context.Background(), "m001", "需求"); err == nil {
		t.Fatal("Ensure on empty material = nil error")
	}
}

func TestCachedMissReturnsEmpty(t *testing.T) {
	docs := newTestDocs(newFakeDocStore(), &fakeMaterialReader{}, &fakeImporter{})
	if url := docs.Cached(context.Background(), "nope"); url != "" {
		t.Errorf("Cached = %q, want empty", url)
	}
}
