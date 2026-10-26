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
	// ErrTooLarge reports a body above the configured material_max_bytes. The
	// app layer maps it to HTTP 413 too_large; the concrete error is
	// *TooLargeError so the response can report the limit and the size seen.
	ErrTooLarge = errors.New("material: content exceeds material_max_bytes")
	// ErrEmpty reports a zero-length body.
	ErrEmpty = errors.New("material: content is empty")
)

// TooLargeError reports a body that exceeded material_max_bytes. Size is the
// number of bytes actually seen before the write was aborted.
type TooLargeError struct {
	Size  int64
	Limit int64
}

// Error implements error.
func (e *TooLargeError) Error() string {
	return fmt.Sprintf("%s: %d bytes > %d", ErrTooLarge.Error(), e.Size, e.Limit)
}

// Unwrap reports ErrTooLarge so errors.Is keeps working for callers that only
// care about the class of failure.
func (e *TooLargeError) Unwrap() error { return ErrTooLarge }

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

// Read returns the whole body of a material version.
//
// The HTTP download path uses Open instead, so a large object is streamed to
// the client rather than buffered in memory; Read stays for the small bodies
// embedded in project responses.
func (w *Writer) Read(m store.Material) ([]byte, error) {
	full, err := w.objectPath(m)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("material: read object %s: %w", m.Path, err)
	}
	return data, nil
}

// Open returns a read seeker over the stored object. The caller streams it and
// must close it; http.ServeContent accepts the *os.File directly, which also
// gives HEAD and Range support for free.
func (w *Writer) Open(m store.Material) (*os.File, error) {
	full, err := w.objectPath(m)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, fmt.Errorf("material: open object %s: %w", m.Path, err)
	}
	return f, nil
}

// objectPath resolves a stored relative path to an absolute object file path.
//
// The path is written by this package, but a corrupted (or hand-edited) row
// must not be able to read outside data_dir/objects: the cleaned path is
// checked for containment instead of trusting the column.
func (w *Writer) objectPath(m store.Material) (string, error) {
	if strings.TrimSpace(m.Path) == "" {
		return "", fmt.Errorf("material: material %s has no object path", m.MaterialID)
	}
	clean := filepath.Clean(filepath.FromSlash(m.Path))
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("material: object path %q escapes the data directory", m.Path)
	}
	full := filepath.Join(w.dataDir, clean)
	rel, err := filepath.Rel(w.objectsDir(), full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("material: object path %q escapes the objects directory", m.Path)
	}
	return full, nil
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
		// The limit is enforced while copying, so an oversized body never
		// reaches the objects directory and its scratch file is dropped here.
		_ = f.Close()
		_ = os.Remove(path)
		return "", "", 0, &TooLargeError{Size: written, Limit: w.maxBytes}
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
