package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// TestIMDocRoundTripAndDedupe covers the im_doc cache the doc importer relies
// on: one row per material version, first import wins.
func TestIMDocRoundTripAndDedupe(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()

	if _, err := GetIMDocURL(ctx, st.Read(), "m001"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetIMDocURL on missing row = %v, want sql.ErrNoRows", err)
	}

	if err := PutIMDoc(ctx, st.Write(), "m001", "", "https://example.feishu.cn/docx/abc", "2026-11-18T12:00:00Z"); err != nil {
		t.Fatalf("PutIMDoc: %v", err)
	}
	// A second import of the same material version must not overwrite the URL.
	if err := PutIMDoc(ctx, st.Write(), "m001", "", "https://example.feishu.cn/docx/other", "2026-11-18T12:01:00Z"); err != nil {
		t.Fatalf("PutIMDoc second: %v", err)
	}

	url, err := GetIMDocURL(ctx, st.Read(), "m001")
	if err != nil {
		t.Fatalf("GetIMDocURL: %v", err)
	}
	if url != "https://example.feishu.cn/docx/abc" {
		t.Errorf("url = %q, want the first imported document", url)
	}
}
