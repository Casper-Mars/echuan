package material

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/store"
)

var (
	// ErrTooLarge reports a body above the configured material_max_bytes.
	// Task-05 maps it to HTTP 413 too_large; until then the app layer turns it
	// into a validation error.
	ErrTooLarge = errors.New("material: content exceeds material_max_bytes")
	// ErrEmpty reports a zero-length body.
	ErrEmpty = errors.New("material: content is empty")
)

// On-disk layout names.
const (
	objectsDirName = "objects"
	tmpDirName     = "tmp"

	dirMode  = 0o755
	fileMode = 0o644
)

// Writer stores material bodies under one data directory.
//
// It holds no per-request state: a Writer is safe for concurrent use as long
// as each call gets its own transaction (the store serialises writers anyway).
type Writer struct {
	dataDir  string
	maxBytes int64
	now      func() time.Time
}

// NewWriter builds a Writer for dataDir and prepares the objects/tmp
// directories. maxBytes is the material_max_bytes limit.
func NewWriter(dataDir string, maxBytes int64) (*Writer, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("material: data directory is empty")
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("material: max bytes must be positive, got %d", maxBytes)
	}

	w := &Writer{
		dataDir:  filepath.Clean(dataDir),
		maxBytes: maxBytes,
		now:      func() time.Time { return time.Now().UTC() },
	}
	for _, dir := range []string{w.objectsDir(), w.tmpDir()} {
		if err := os.MkdirAll(dir, dirMode); err != nil {
			return nil, fmt.Errorf("material: create %s: %w", dir, err)
		}
	}
	return w, nil
}

// MaxBytes returns the configured per-material limit.
func (w *Writer) MaxBytes() int64 { return w.maxBytes }

// WriteInput describes one material version to store.
//
// PreviousMaterialID is the physical id of the current chain head; version is
// derived from it (head + 1) inside the transaction, so a caller cannot
// accidentally write a broken chain.
type WriteInput struct {
	Kind               int
	Title              string
	Body               []byte
	UploadedBy         string
	PreviousMaterialID *int64
}

// Stored is the result of Write. NewObject reports whether this write created
// the object file; discard it only in that case, otherwise the object belongs
// to an earlier version.
type Stored struct {
	Material  store.Material
	NewObject bool
}

// Write stores one material version: temp file → hash → fsync → rename into
// objects/<sha[:2]>/<sha> → materials row inside tx.
func (w *Writer) Write(ctx context.Context, tx *sql.Tx, in WriteInput) (Stored, error) {
	if tx == nil {
		return Stored{}, errors.New("material: Write requires a transaction")
	}
	if in.Kind <= 0 {
		return Stored{}, fmt.Errorf("material: invalid kind %d", in.Kind)
	}
	if len(in.Body) == 0 {
		return Stored{}, ErrEmpty
	}

	tmpPath, sum, size, err := w.writeTemp(in.Body)
	if err != nil {
		return Stored{}, err
	}

	relPath := filepath.ToSlash(filepath.Join(objectsDirName, sum[:2], sum))
	target := filepath.Join(w.dataDir, filepath.FromSlash(relPath))

	// Content addressing: an existing object is reused as-is.
	newObject := true
	switch _, statErr := os.Stat(target); {
	case statErr == nil:
		newObject = false
		if err := os.Remove(tmpPath); err != nil {
			return Stored{}, fmt.Errorf("material: drop temporary file: %w", err)
		}
	case errors.Is(statErr, os.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(target), dirMode); err != nil {
			_ = os.Remove(tmpPath)
			return Stored{}, fmt.Errorf("material: create object directory: %w", err)
		}
		if err := os.Rename(tmpPath, target); err != nil {
			_ = os.Remove(tmpPath)
			return Stored{}, fmt.Errorf("material: publish object: %w", err)
		}
		if err := syncDir(filepath.Dir(target)); err != nil {
			// The object is already durable enough for this version; a failed
			// directory fsync must not lose the row that points at it.
			_ = err
		}
	default:
		_ = os.Remove(tmpPath)
		return Stored{}, fmt.Errorf("material: stat object: %w", statErr)
	}

	version := 1
	if in.PreviousMaterialID != nil {
		head, err := store.GetMaterialByID(ctx, tx, *in.PreviousMaterialID)
		if err != nil {
			w.discard(target, newObject)
			return Stored{}, fmt.Errorf("material: read chain head %d: %w", *in.PreviousMaterialID, err)
		}
		version = head.Version + 1
	}

	now := w.now().UTC().Truncate(time.Millisecond)
	ts := now.Format(time.RFC3339Nano)
	materialID, err := newMaterialID(now)
	if err != nil {
		w.discard(target, newObject)
		return Stored{}, err
	}

	row := store.NewMaterial{
		MaterialID:         materialID,
		Kind:               in.Kind,
		Version:            version,
		Title:              in.Title,
		SHA256:             sum,
		Size:               size,
		Path:               relPath,
		UploadedBy:         in.UploadedBy,
		PreviousMaterialID: in.PreviousMaterialID,
		CreateTime:         ts,
		UpdateTime:         ts,
	}
	id, err := store.InsertMaterial(ctx, tx, row)
	if err != nil {
		w.discard(target, newObject)
		return Stored{}, err
	}

	return Stored{
		Material: store.Material{
			ID:                 id,
			MaterialID:         row.MaterialID,
			Kind:               row.Kind,
			Version:            row.Version,
			Title:              row.Title,
			SHA256:             row.SHA256,
			Size:               row.Size,
			Path:               row.Path,
			UploadedBy:         row.UploadedBy,
			PreviousMaterialID: row.PreviousMaterialID,
			CreateTime:         row.CreateTime,
			UpdateTime:         row.UpdateTime,
		},
		NewObject: newObject,
	}, nil
}

// Read returns the body of a material version.
func (w *Writer) Read(m store.Material) ([]byte, error) {
	if strings.TrimSpace(m.Path) == "" {
		return nil, fmt.Errorf("material: material %s has no object path", m.MaterialID)
	}
	full := filepath.Join(w.dataDir, filepath.FromSlash(m.Path))
	// The path is written by this package, but a defensive containment check
	// keeps a corrupted row from reading outside data_dir.
	if !strings.HasPrefix(full, w.dataDir+string(os.PathSeparator)) {
		return nil, fmt.Errorf("material: object path %q escapes the data directory", m.Path)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("material: read object %s: %w", m.Path, err)
	}
	return data, nil
}

// RemoveNewObject deletes the object file created by the given write. It is a
// no-op when the write reused an existing object, because that file belongs to
// earlier versions.
func (w *Writer) RemoveNewObject(s Stored) error {
	if !s.NewObject || s.Material.SHA256 == "" {
		return nil
	}
	target := filepath.Join(w.dataDir, filepath.FromSlash(s.Material.Path))
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("material: remove object %s: %w", s.Material.Path, err)
	}
	return nil
}

// discard removes target unless this write reused an existing object.
func (w *Writer) discard(target string, newObject bool) {
	if !newObject {
		return
	}
	_ = os.Remove(target)
}

// writeTemp streams body into a scratch file while hashing it, then fsyncs it.
// The caller either renames the file into objects/ or deletes it.
func (w *Writer) writeTemp(body []byte) (path, sum string, size int64, err error) {
	name, err := randomName()
	if err != nil {
		return "", "", 0, err
	}
	path = filepath.Join(w.tmpDir(), name)

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return "", "", 0, fmt.Errorf("material: create temporary file: %w", err)
	}

	hash := sha256.New()
	// Read one byte past the limit so "exactly max" is accepted and "max+1" is
	// reported as too large without buffering the whole body again.
	written, copyErr := io.Copy(io.MultiWriter(f, hash), io.LimitReader(bytes.NewReader(body), w.maxBytes+1))

	switch {
	case copyErr != nil:
		_ = f.Close()
		_ = os.Remove(path)
		return "", "", 0, fmt.Errorf("material: write temporary file: %w", copyErr)
	case written > w.maxBytes:
		_ = f.Close()
		_ = os.Remove(path)
		return "", "", 0, fmt.Errorf("%w: %d bytes > %d", ErrTooLarge, written, w.maxBytes)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", "", 0, fmt.Errorf("material: fsync temporary file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", "", 0, fmt.Errorf("material: close temporary file: %w", err)
	}

	return path, hex.EncodeToString(hash.Sum(nil)), written, nil
}

func (w *Writer) objectsDir() string { return filepath.Join(w.dataDir, objectsDirName) }
func (w *Writer) tmpDir() string     { return filepath.Join(w.dataDir, tmpDirName) }

// randomName returns a 128-bit random file name for the scratch file.
func randomName() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("material: random name: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// materialIDEncoding renders the ULID-shaped business id: 48-bit millisecond
// timestamp followed by 80 random bits, base32 (no padding) → 26 characters.
// The timestamp prefix keeps ids roughly sortable by creation time.
var materialIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func newMaterialID(now time.Time) (string, error) {
	var raw [16]byte
	ms := uint64(now.UnixMilli())
	raw[0] = byte(ms >> 40)
	raw[1] = byte(ms >> 32)
	raw[2] = byte(ms >> 24)
	raw[3] = byte(ms >> 16)
	raw[4] = byte(ms >> 8)
	raw[5] = byte(ms)
	if _, err := rand.Read(raw[6:]); err != nil {
		return "", fmt.Errorf("material: generate material id: %w", err)
	}
	return materialIDEncoding.EncodeToString(raw[:]), nil
}

// syncDir fsyncs a directory so the rename itself is durable.
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
