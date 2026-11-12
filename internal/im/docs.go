package im

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// DocStore caches material → Feishu document mappings.
type DocStore interface {
	CachedDoc(ctx context.Context, materialID string) (string, bool, error)
	SaveDoc(ctx context.Context, materialID, docToken, url string, at time.Time) error
}

// MaterialReader returns the Markdown body of a material version.
type MaterialReader interface {
	MaterialMarkdown(ctx context.Context, materialID string) (name string, content []byte, err error)
}

// DocImporter turns Markdown into a Feishu document and returns its URL.
type DocImporter interface {
	ImportMarkdown(ctx context.Context, name string, content []byte) (string, error)
}

// Docs imports materials on demand and remembers the result.
//
// One material version maps to exactly one document, so a card always links to
// the version it was rendered from — the snapshot is free.
type Docs struct {
	store     DocStore
	materials MaterialReader
	importer  DocImporter
	logger    *slog.Logger
	now       func() time.Time

	mu       sync.Mutex
	inflight map[string]*sync.WaitGroup
}

// NewDocs builds the document service.
func NewDocs(store DocStore, materials MaterialReader, importer DocImporter, logger *slog.Logger) *Docs {
	if logger == nil {
		logger = slog.Default()
	}
	return &Docs{
		store: store, materials: materials, importer: importer,
		logger: logger, now: func() time.Time { return time.Now().UTC() },
		inflight: map[string]*sync.WaitGroup{},
	}
}

// Cached returns the document URL if the material was already imported.
func (d *Docs) Cached(ctx context.Context, materialID string) string {
	if d == nil || materialID == "" {
		return ""
	}
	url, ok, err := d.store.CachedDoc(ctx, materialID)
	if err != nil {
		d.logger.Warn("im doc cache lookup failed", "material", materialID, "error", err)
		return ""
	}
	if !ok {
		return ""
	}
	return url
}

// Ensure imports the material if needed and returns the document URL.
//
// Concurrent calls for the same material wait for the first one instead of
// importing twice: every import costs three API calls and counts against the
// daily quota.
func (d *Docs) Ensure(ctx context.Context, materialID, title string) (string, error) {
	if d == nil {
		return "", fmt.Errorf("im: docs disabled")
	}
	if url := d.Cached(ctx, materialID); url != "" {
		return url, nil
	}

	d.mu.Lock()
	if wg, ok := d.inflight[materialID]; ok {
		d.mu.Unlock()
		wg.Wait()
		if url := d.Cached(ctx, materialID); url != "" {
			return url, nil
		}
		return "", fmt.Errorf("im: concurrent import of %s failed", materialID)
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	d.inflight[materialID] = wg
	d.mu.Unlock()

	defer func() {
		wg.Done()
		d.mu.Lock()
		delete(d.inflight, materialID)
		d.mu.Unlock()
	}()

	name, content, err := d.materials.MaterialMarkdown(ctx, materialID)
	if err != nil {
		return "", fmt.Errorf("im: read material %s: %w", materialID, err)
	}
	if len(content) == 0 {
		return "", fmt.Errorf("im: material %s is empty", materialID)
	}
	// The server rejects names that do not match the uploaded file's extension.
	docName := sanitizeDocName(title, name)

	url, err := d.importer.ImportMarkdown(ctx, docName, content)
	if err != nil {
		return "", err
	}
	if err := d.store.SaveDoc(ctx, materialID, "", url, d.now()); err != nil {
		d.logger.Warn("im doc cache write failed", "material", materialID, "error", err)
	}
	d.logger.Info("im doc imported", "material", materialID, "url", url)
	return url, nil
}

// sanitizeDocName keeps the document name readable and non-empty.
func sanitizeDocName(title, fallback string) string {
	if title != "" {
		return title
	}
	if fallback != "" {
		return fallback
	}
	return "驿传材料"
}
