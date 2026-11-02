package app

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// maxPackageEntryBytes bounds the title part of one entry name inside the zip.
// It is a byte budget applied on rune boundaries, so a long Chinese title is
// truncated without splitting a character.
const maxPackageEntryBytes = 120

// PackageFile is a project package built in memory: the zip bytes, the name
// the browser should save it as, and how many documents it contains.
type PackageFile struct {
	Name    string
	Data    []byte
	Entries int
}

// ProjectPackage builds the download-on-demand package of one project: the
// current requirement version followed by every independent spec's current
// version, each as a separate markdown file.
//
// Nothing is persisted: the zip is assembled from the objects the project
// already points at, so downloading it does not add a material or move any
// pointer. An empty project yields a valid zip with zero entries (200), not a
// 404 — the project exists, it just has nothing to package yet.
func (s *ProjectService) ProjectPackage(ctx context.Context, projectID string) (PackageFile, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return PackageFile{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "id", Reason: "is required"},
		}}
	}
	p, err := store.GetProjectByProjectID(ctx, s.store.Read(), projectID)
	if err != nil {
		return PackageFile{}, specLookupError(err, "project", projectID)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries := 0

	requirement, err := s.packageRequirementEntry(ctx, p)
	if err != nil {
		return PackageFile{}, err
	}
	if requirement != nil {
		if err := writeZipEntry(zw, requirement); err != nil {
			return PackageFile{}, err
		}
		entries++
	}

	docs, err := store.ListSpecDocuments(ctx, s.store.Read(), p.ID)
	if err != nil {
		return PackageFile{}, err
	}
	for _, doc := range docs {
		entry, err := s.packageSpecEntry(ctx, doc)
		if err != nil {
			return PackageFile{}, err
		}
		if err := writeZipEntry(zw, entry); err != nil {
			return PackageFile{}, err
		}
		entries++
	}

	if err := zw.Close(); err != nil {
		return PackageFile{}, fmt.Errorf("close package %s: %w", projectID, err)
	}
	return PackageFile{Name: projectID + "-资料包.zip", Data: buf.Bytes(), Entries: entries}, nil
}

// packageEntry is one markdown file inside the zip.
type packageEntry struct {
	name string
	body []byte
}

// packageRequirementEntry loads the current requirement version. It returns
// nil when the project has no requirement yet.
func (s *ProjectService) packageRequirementEntry(ctx context.Context, p store.Project) (*packageEntry, error) {
	if p.RequirementMaterialID == nil {
		return nil, nil
	}
	row, err := store.GetMaterialByID(ctx, s.store.Read(), *p.RequirementMaterialID)
	if err != nil {
		return nil, err
	}
	mapped, err := materialFromRow(row)
	if err != nil {
		return nil, err
	}
	body, err := s.materials.Read(row)
	if err != nil {
		return nil, err
	}
	base := fmt.Sprintf("需求-v%d-%s", mapped.Version, mapped.Title)
	return &packageEntry{name: zipEntryName(base, "需求"), body: body}, nil
}

// packageSpecEntry loads one independent spec's current version.
func (s *ProjectService) packageSpecEntry(ctx context.Context, doc store.SpecDocument) (*packageEntry, error) {
	row, err := store.GetMaterialByID(ctx, s.store.Read(), doc.MaterialID)
	if err != nil {
		return nil, err
	}
	mapped, err := materialFromRow(row)
	if err != nil {
		return nil, err
	}
	body, err := s.materials.Read(row)
	if err != nil {
		return nil, err
	}
	label := specLabel(doc)
	base := fmt.Sprintf("方案-%s-v%d-%s", label, mapped.Version, mapped.Title)
	return &packageEntry{name: zipEntryName(base, "方案-"+label), body: body}, nil
}

// specLabel renders the short spec number used in file names: the part after
// the last dash of the business id ("APP-1-S0002" → "S0002"), or the padded
// sequence number when the id does not follow that shape.
func specLabel(doc store.SpecDocument) string {
	if i := strings.LastIndex(doc.SpecID, "-"); i >= 0 && i+1 < len(doc.SpecID) {
		if label := strings.TrimSpace(doc.SpecID[i+1:]); label != "" {
			return label
		}
	}
	return fmt.Sprintf("S%04d", doc.SeqNo)
}

// zipEntryName turns "需求-v1-<title>" into a safe "<base>.md" entry name.
//
// A zip entry is a path: a "/" in a title would create a directory, so path
// separators and the other characters Windows rejects are replaced before the
// name is trimmed, length-capped and suffixed. The cleaned name keeps UTF-8,
// which is how Chinese titles stay readable in the archive.
func zipEntryName(raw, fallback string) string {
	base := sanitizeZipBase(raw, fallback)
	return base + ".md"
}

func sanitizeZipBase(raw, fallback string) string {
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteByte('-')
		case r < 0x20 || r == 0x7f:
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	base := strings.Trim(b.String(), ". ")
	base = truncateRunes(base, maxPackageEntryBytes)
	if base == "" {
		return fallback
	}
	return base
}

// truncateRunes caps s at max bytes without splitting a UTF-8 character.
func truncateRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	used := 0
	end := 0
	for i, r := range s {
		size := utf8.RuneLen(r)
		if used+size > max {
			break
		}
		used += size
		end = i + size
	}
	return strings.Trim(s[:end], ". ")
}

// writeZipEntry appends one file. Method is Deflate and Modified stays zero so
// the archive bytes are a pure function of the inputs.
func writeZipEntry(zw *zip.Writer, entry *packageEntry) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: entry.name, Method: zip.Deflate})
	if err != nil {
		return fmt.Errorf("create zip entry %s: %w", entry.name, err)
	}
	if _, err := w.Write(entry.body); err != nil {
		return fmt.Errorf("write zip entry %s: %w", entry.name, err)
	}
	return nil
}
