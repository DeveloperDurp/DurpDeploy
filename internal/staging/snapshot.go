// Package staging validates and merges deployment-scoped file snapshots.
package staging

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"durpdeploy/internal/config"
)

const (
	MaxFiles               = 20000
	MaxBytes         int64 = 512 << 20
	MaxArchive       int64 = MaxBytes + (32 << 20)
	Variable               = "DURPDEPLOY_STAGE_DIR"
	CapabilityHeader       = "X-DurpDeploy-Staging"
	Capability             = "staging/1"
)

var ErrInvalid = errors.New("invalid staging archive")
var ErrConflict = errors.New("conflicting staging changes")

type Entry struct {
	Directory bool
	Size      int64
	SHA256    string
	offset    int64
	file      *os.File
}

func (e Entry) same(other Entry) bool {
	return e.Directory == other.Directory && e.Size == other.Size &&
		e.SHA256 == other.SHA256
}

type Snapshot struct {
	Entries map[string]Entry
}

// Read leaves file ownership with the caller. Entries refer to immutable bytes.
func Read(ctx context.Context, file *os.File) (Snapshot, error) {
	return ReadWithLimits(ctx, file, config.Defaults().Limits.Staging)
}

// ReadWithLimits counts file content and distinct entries, including parents.
func ReadWithLimits(
	ctx context.Context, file *os.File, limits config.StagingLimits,
) (Snapshot, error) {
	info, err := file.Stat()
	if err != nil {
		return Snapshot{}, err
	}
	if info.Size() < 1024 || info.Size() > limits.ArchiveBytes {
		return Snapshot{}, ErrInvalid
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Snapshot{}, err
	}
	return readSnapshotEntries(ctx, file, limits)
}

func readSnapshotEntries(
	ctx context.Context, file *os.File, limits config.StagingLimits,
) (Snapshot, error) {
	count := &boundedReader{ctx: ctx, reader: file}
	reader := tar.NewReader(count)
	result := Snapshot{Entries: make(map[string]Entry)}
	seen := make(map[string]bool)
	var total int64
	for {
		// Bound PAX/GNU metadata allocation inside archive/tar.Next.
		count.remaining = 16 << 20
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Snapshot{}, fmt.Errorf("read staging header: %w", ErrInvalid)
		}
		name, err := snapshotEntryName(header, seen)
		if err != nil {
			return Snapshot{}, err
		}
		if name == "" {
			continue
		}
		entry, err := readSnapshotEntry(
			reader, count, file, header, limits.PayloadBytes-total,
		)
		if err != nil {
			return Snapshot{}, err
		}
		total += header.Size
		if err := result.addEntry(name, entry, limits.Entries); err != nil {
			return Snapshot{}, err
		}
	}
	if err := checkSnapshotTrailer(count); err != nil {
		return Snapshot{}, err
	}
	return result, nil
}

func snapshotEntryName(
	header *tar.Header,
	seen map[string]bool,
) (string, error) {
	// Sparse TAR readers synthesize bytes that have no source-file offset.
	for key := range header.PAXRecords {
		if strings.HasPrefix(key, "GNU.sparse.") {
			return "", ErrInvalid
		}
	}
	name := strings.TrimSuffix(strings.TrimPrefix(header.Name, "./"), "/")
	if (name == "." || name == "") && header.Typeflag == tar.TypeDir &&
		header.Size == 0 {
		return "", nil
	}
	if name == "." || !fs.ValidPath(name) ||
		strings.ContainsAny(name, "\\\x00") || len(name) > 4096 || seen[name] {
		return "", ErrInvalid
	}
	seen[name] = true
	return name, nil
}

func readSnapshotEntry(
	reader *tar.Reader,
	count *boundedReader,
	file *os.File,
	header *tar.Header,
	remaining int64,
) (Entry, error) {
	entry := Entry{
		Directory: header.Typeflag == tar.TypeDir,
		Size:      header.Size, offset: count.offset, file: file,
	}
	//nolint:staticcheck // Accept legacy regular-file tar headers.
	if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA &&
		!entry.Directory || header.Size < 0 || header.Size > remaining ||
		entry.Directory && header.Size != 0 {
		return Entry{}, ErrInvalid
	}
	count.remaining = header.Size
	hash := sha256.New()
	if _, err := io.CopyN(hash, reader, header.Size); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			err = errors.Join(ErrInvalid, err)
		}
		return Entry{}, fmt.Errorf("read staging content: %w", err)
	}
	if !entry.Directory {
		entry.SHA256 = hex.EncodeToString(hash.Sum(nil))
	}
	return entry, nil
}

func (s Snapshot) addEntry(name string, entry Entry, limit int64) error {
	if prior, exists := s.Entries[name]; exists &&
		(!prior.Directory || !entry.Directory) {
		return ErrInvalid
	}
	s.Entries[name] = entry
	for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
		if prior, exists := s.Entries[parent]; exists && !prior.Directory {
			return ErrInvalid
		}
		if _, exists := s.Entries[parent]; !exists {
			s.Entries[parent] = Entry{Directory: true}
		}
	}
	if int64(len(s.Entries)) > limit {
		return ErrInvalid
	}
	return nil
}

func checkSnapshotTrailer(count *boundedReader) error {
	// A TAR EOF must not conceal a second archive or unbounded trailing bytes.
	count.remaining = 16 << 20
	trailer, err := io.ReadAll(io.LimitReader(count, (16<<20)+1))
	if err != nil || len(trailer) > 16<<20 {
		return ErrInvalid
	}
	for _, value := range trailer {
		if value != 0 {
			return ErrInvalid
		}
	}
	return nil
}

// boundedReader adapts one operation's cancellation to io.Reader.Read, which
// cannot accept a context. Each instance is local to a synchronous read or copy.
type boundedReader struct {
	ctx       context.Context
	reader    io.Reader
	offset    int64
	remaining int64
}

func (r *boundedReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.remaining <= 0 {
		return 0, ErrInvalid
	}
	if int64(len(data)) > r.remaining {
		data = data[:r.remaining]
	}
	n, err := r.reader.Read(data)
	r.offset += int64(n)
	r.remaining -= int64(n)
	return n, err
}
