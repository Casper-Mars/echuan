package material

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/Casper-Mars/echuan/internal/store"
)

// newTestEnv opens a migrated database and a writer rooted in the same
// temporary directory.
func newTestEnv(t *testing.T, maxBytes int64) (*Writer, *store.Store, string) {
	t.Helper()

	dir := t.TempDir()
	st, err := store.Open(store.Config{Path: filepath.Join(dir, "state.db")})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	dataDir := filepath.Join(dir, "data")
	writer, err := NewWriter(dataDir, maxBytes)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	return writer, st, dataDir
}

// write runs one Write inside its own transaction.
func write(t *testing.T, w *Writer, st *store.Store, in WriteInput) (Stored, error) {
	t.Helper()
	var (
		stored Stored
		err    error
	)
	txErr := st.Tx(context.Background(), func(tx *sql.Tx) error {
		stored, err = w.Write(context.Background(), tx, in)
		return err
	})
	if txErr != nil {
		return Stored{}, txErr
	}
	return stored, nil
}

func countObjectFiles(t *testing.T, dataDir string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(filepath.Join(dataDir, objectsDirName), func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk objects: %v", err)
	}
	return count
}

func TestWriteStoresObjectAndRow(t *testing.T) {
	writer, st, dataDir := newTestEnv(t, 1<<20)
	body := []byte("# 登录页改版\n\n支持 emoji 🚀\n")

	stored, err := write(t, writer, st, WriteInput{
		Kind:       1,
		Title:      "登录页改版",
		Body:       body,
		UploadedBy: "agent:cto",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	sum := sha256.Sum256(body)
	wantSum := hex.EncodeToString(sum[:])
	if stored.Material.SHA256 != wantSum {
		t.Errorf("sha256 = %s, want %s", stored.Material.SHA256, wantSum)
	}
	if stored.Material.Size != int64(len(body)) {
		t.Errorf("size = %d, want %d", stored.Material.Size, len(body))
	}
	if stored.Material.Version != 1 {
		t.Errorf("version = %d, want 1", stored.Material.Version)
	}
	if !stored.NewObject {
		t.Error("NewObject = false, want true for the first write")
	}

	// objects/<sha[:2]>/<sha> must exist with exactly the request body.
	objectPath := filepath.Join(dataDir, objectsDirName, wantSum[:2], wantSum)
	onDisk, err := os.ReadFile(objectPath)
	if err != nil {
		t.Fatalf("read object: %v", err)
	}
	if string(onDisk) != string(body) {
		t.Errorf("object content = %q, want %q", onDisk, body)
	}
	if stored.Material.Path != objectsDirName+"/"+wantSum[:2]+"/"+wantSum {
		t.Errorf("path = %q, want the relative object path", stored.Material.Path)
	}

	// The row is visible to readers once the transaction committed.
	row, err := store.GetMaterialByMaterialID(context.Background(), st.Read(), stored.Material.MaterialID)
	if err != nil {
		t.Fatalf("GetMaterialByMaterialID: %v", err)
	}
	if row.SHA256 != wantSum || row.Kind != 1 || row.UploadedBy != "agent:cto" {
		t.Errorf("row = %+v, want the written values", row)
	}

	// No scratch file survives a successful write.
	entries, err := os.ReadDir(filepath.Join(dataDir, tmpDirName))
	if err != nil {
		t.Fatalf("read tmp: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("tmp has %d leftovers, want 0", len(entries))
	}
}

func TestWriteDeduplicatesContent(t *testing.T) {
	writer, st, dataDir := newTestEnv(t, 1<<20)
	body := []byte("同一个正文")

	first, err := write(t, writer, st, WriteInput{Kind: 1, Title: "v1", Body: body, UploadedBy: "human:liu"})
	if err != nil {
		t.Fatalf("first Write: %v", err)
	}
	second, err := write(t, writer, st, WriteInput{Kind: 1, Title: "v2", Body: body, UploadedBy: "human:liu"})
	if err != nil {
		t.Fatalf("second Write: %v", err)
	}

	if !first.NewObject {
		t.Error("first NewObject = false, want true")
	}
	if second.NewObject {
		t.Error("second NewObject = true, want false: the object is content-addressed")
	}
	if got := countObjectFiles(t, dataDir); got != 1 {
		t.Errorf("object files = %d, want 1", got)
	}
	count, err := store.CountMaterialsBySHA(context.Background(), st.Read(), first.Material.SHA256)
	if err != nil {
		t.Fatalf("CountMaterialsBySHA: %v", err)
	}
	if count != 2 {
		t.Errorf("materials rows = %d, want 2 (one per submission)", count)
	}

	// Removing the second write must not delete the shared object.
	if err := writer.RemoveNewObject(second); err != nil {
		t.Fatalf("RemoveNewObject(second): %v", err)
	}
	if _, err := writer.Read(first.Material); err != nil {
		t.Fatalf("Read after RemoveNewObject(second): %v", err)
	}
}

func TestWriteVersionChain(t *testing.T) {
	writer, st, _ := newTestEnv(t, 1<<20)
	ctx := context.Background()

	first, err := write(t, writer, st, WriteInput{Kind: 1, Title: "v1", Body: []byte("第一版")})
	if err != nil {
		t.Fatalf("first Write: %v", err)
	}
	second, err := write(t, writer, st, WriteInput{
		Kind:               1,
		Title:              "v1",
		Body:               []byte("第二版"),
		PreviousMaterialID: &first.Material.ID,
	})
	if err != nil {
		t.Fatalf("second Write: %v", err)
	}

	if second.Material.Version != 2 {
		t.Errorf("version = %d, want 2", second.Material.Version)
	}
	if second.Material.PreviousMaterialID == nil || *second.Material.PreviousMaterialID != first.Material.ID {
		t.Errorf("previous_material_id = %v, want %d", second.Material.PreviousMaterialID, first.Material.ID)
	}

	// The old version stays readable after the chain moved on.
	oldRow, err := store.GetMaterialByID(ctx, st.Read(), first.Material.ID)
	if err != nil {
		t.Fatalf("GetMaterialByID(v1): %v", err)
	}
	data, err := writer.Read(oldRow)
	if err != nil {
		t.Fatalf("Read(v1): %v", err)
	}
	if string(data) != "第一版" {
		t.Errorf("v1 content = %q, want %q", data, "第一版")
	}
}

func TestWriteRejectsTooLarge(t *testing.T) {
	writer, st, dataDir := newTestEnv(t, 4)

	_, err := write(t, writer, st, WriteInput{Kind: 1, Title: "big", Body: []byte("12345")})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}

	// Nothing survives: no object, no row, no scratch file.
	if got := countObjectFiles(t, dataDir); got != 0 {
		t.Errorf("object files = %d, want 0", got)
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, tmpDirName))
	if err != nil {
		t.Fatalf("read tmp: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("tmp has %d leftovers, want 0", len(entries))
	}
	var count int64
	if err := st.Read().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM materials").Scan(&count); err != nil {
		t.Fatalf("count materials: %v", err)
	}
	if count != 0 {
		t.Errorf("materials rows = %d, want 0", count)
	}
}

func TestWriteAcceptsExactlyMaxBytes(t *testing.T) {
	writer, st, _ := newTestEnv(t, 5)
	if _, err := write(t, writer, st, WriteInput{Kind: 1, Title: "max", Body: []byte("12345")}); err != nil {
		t.Fatalf("Write at exactly max bytes: %v", err)
	}
}

func TestWriteRejectsEmptyBody(t *testing.T) {
	writer, st, _ := newTestEnv(t, 1<<20)
	if _, err := write(t, writer, st, WriteInput{Kind: 1, Title: "empty"}); !errors.Is(err, ErrEmpty) {
		t.Fatalf("err = %v, want ErrEmpty", err)
	}
}

func TestWriteRequiresTransaction(t *testing.T) {
	writer, _, _ := newTestEnv(t, 1<<20)
	if _, err := writer.Write(context.Background(), nil, WriteInput{Kind: 1, Body: []byte("x")}); err == nil {
		t.Fatal("Write without a transaction succeeded, want error")
	}
}

func TestRemoveNewObjectDeletesOnlyOwnObject(t *testing.T) {
	writer, st, dataDir := newTestEnv(t, 1<<20)

	stored, err := write(t, writer, st, WriteInput{Kind: 1, Title: "t", Body: []byte("删除我")})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.RemoveNewObject(stored); err != nil {
		t.Fatalf("RemoveNewObject: %v", err)
	}
	if got := countObjectFiles(t, dataDir); got != 0 {
		t.Errorf("object files = %d, want 0", got)
	}
	if _, err := writer.Read(stored.Material); err == nil {
		t.Error("Read after removal succeeded, want error")
	}
}

func TestReadRejectsEscapingPath(t *testing.T) {
	writer, _, _ := newTestEnv(t, 1<<20)
	_, err := writer.Read(store.Material{MaterialID: "x", Path: "../../etc/passwd"})
	if err == nil {
		t.Fatal("Read of an escaping path succeeded, want error")
	}
}
