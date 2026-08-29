package runner

import (
	"bytes"
	"strings"
)

type redactingWriter struct {
	scrubber *Scrubber
	writeLog func(string) error
	buffer   bytes.Buffer
}

func newRedactingWriter(
	scrubber *Scrubber,
	writeLog func(string) error,
) *redactingWriter {
	return &redactingWriter{scrubber: scrubber, writeLog: writeLog}
}

func (w *redactingWriter) Write(data []byte) (int, error) {
	w.buffer.Write(data)
	lastNewline := bytes.LastIndexByte(w.buffer.Bytes(), '\n')
	if lastNewline == -1 {
		return len(data), nil
	}
	if err := w.write(w.buffer.String()[:lastNewline+1]); err != nil {
		return 0, err
	}
	w.buffer.Next(lastNewline + 1)
	return len(data), nil
}

func (w *redactingWriter) flush() error {
	if w.buffer.Len() == 0 {
		return nil
	}
	if err := w.write(w.buffer.String()); err != nil {
		return err
	}
	w.buffer.Reset()
	return nil
}

func (w *redactingWriter) write(text string) error {
	if w.writeLog == nil {
		return nil
	}
	for _, line := range strings.Split(
		strings.TrimSuffix(w.scrubber.Scrub(text), "\n"),
		"\n",
	) {
		if err := w.writeLog(line); err != nil {
			return err
		}
	}
	return nil
}
