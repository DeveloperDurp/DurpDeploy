package artifact

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
)

// metadataReader bounds parser reads, including footer searches and re-reads.
// The quota is disabled after parsing; payload reads keep cancellation checks.
type metadataReader struct {
	ctx       context.Context // NOSONAR: archive/zip requires a ReaderAt without context parameters.
	reader    io.ReaderAt
	remaining int64
	failure   error
}

func (r *metadataReader) ReadAt(buffer []byte, offset int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		r.failure = err
		return 0, err
	}
	if r.failure != nil {
		return 0, r.failure
	}
	if r.remaining >= 0 && int64(len(buffer)) > r.remaining {
		r.failure = ErrMetadataLimit
		return 0, r.failure
	}
	count, err := r.reader.ReadAt(buffer, offset)
	if r.remaining >= 0 {
		r.remaining -= int64(count)
	}
	return count, err
}

func openZIP(
	ctx context.Context,
	filename string,
) (archive *zip.Reader, file *os.File, err error) {
	file, err = os.Open(filename)
	if err != nil {
		return nil, nil, ErrInvalid
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, file.Close())
			file = nil
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, file, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxDownload {
		return nil, file, ErrInvalid
	}
	reader := &metadataReader{ctx: ctx, reader: file, remaining: MaxMetadata}
	archive, err = zip.NewReader(reader, info.Size())
	// zip may convert a reader error into ErrFormat while locating its footer.
	if reader.failure != nil {
		return nil, file, reader.failure
	}
	if err != nil {
		return nil, file, ErrInvalid
	}
	reader.remaining = -1
	return archive, file, nil
}
