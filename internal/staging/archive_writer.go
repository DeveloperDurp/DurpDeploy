package staging

import "io"

// LimitWriter rejects writes that exceed the remaining archive byte budget.
// It also bounds TAR padding and metadata written during canonicalization.
func LimitWriter(target io.Writer, limit int64) io.Writer {
	return &archiveWriter{target: target, remaining: limit}
}

type archiveWriter struct {
	target    io.Writer
	remaining int64
}

func (w *archiveWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, ErrInvalid
	}
	n, err := w.target.Write(data)
	w.remaining -= int64(n)
	return n, err
}
