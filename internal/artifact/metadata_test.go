package artifact

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateZIPBoundsCentralDirectoryParsing(t *testing.T) {
	// Given: a small advertised count cannot limit Go's eager entry parsing.
	filename := filepath.Join(t.TempDir(), "many-records.zip")
	writeManyDirectoryRecords(t, filename, 400000)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	// When
	err := ValidateZIP(t.Context(), filename)
	runtime.ReadMemStats(&after)
	// Then: reject during parsing, not after allocating every advertised entry.
	if !errors.Is(err, ErrMetadataLimit) {
		t.Fatalf("expected metadata limit, got %v", err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	if allocated > 256<<20 {
		t.Fatalf(
			"parser allocated %d bytes despite the metadata quota",
			allocated,
		)
	}
}

func writeManyDirectoryRecords(t *testing.T, filename string, records int) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	header := make([]byte, 46)
	binary.LittleEndian.PutUint32(header, 0x02014b50)
	chunk := make([]byte, 46*1000)
	for offset := 0; offset < len(chunk); offset += len(header) {
		copy(chunk[offset:], header)
	}
	for remaining := records; remaining > 0; {
		count := min(remaining, 1000)
		if _, err := file.Write(chunk[:count*46]); err != nil {
			t.Fatal(err)
		}
		remaining -= count
	}
	end := make([]byte, 22)
	binary.LittleEndian.PutUint32(end, 0x06054b50)
	// ZIP's classic EOCD entry count wraps at 65536; the parser reads past it.
	binary.LittleEndian.PutUint16(end[8:], uint16(records))
	binary.LittleEndian.PutUint16(end[10:], uint16(records))
	binary.LittleEndian.PutUint32(end[12:], uint32(records*46))
	if _, err := file.Write(end); err != nil {
		t.Fatal(err)
	}
}

func TestValidateZIPMetadataQuotaDoesNotLimitPayload(t *testing.T) {
	// Given: payload bytes exceed the metadata budget but have small headers.
	filename := filepath.Join(t.TempDir(), "large-payload.zip")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	writer := zip.NewWriter(file)
	entry, err := writer.CreateHeader(
		&zip.FileHeader{Name: "payload.bin", Method: zip.Store},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(entry, zeroReader{}, MaxMetadata+1); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	// When
	err = ValidateZIP(t.Context(), filename)
	// Then
	if err != nil {
		t.Fatalf("payload incorrectly charged to metadata quota: %v", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(
	buffer []byte,
) (int, error) {
	clear(buffer)
	return len(buffer), nil
}

func TestValidateZIPCancellationDuringMetadataParsing(t *testing.T) {
	// Given
	filename := filepath.Join(t.TempDir(), "cancelled.zip")
	if err := os.WriteFile(
		filename,
		zipFixture(t, "file", 0644),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// When
	err := ValidateZIP(ctx, filename)
	// Then
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost during parser initialization: %v", err)
	}
}

func TestMetadataReaderKeepsFailureAndCancellation(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(t.Context())
	reader := &metadataReader{
		ctx:       ctx,
		reader:    bytes.NewReader([]byte("data")),
		remaining: 1,
	}
	// When
	_, err := reader.ReadAt(make([]byte, 2), 0)
	_, nextErr := reader.ReadAt(make([]byte, 1), 0)
	cancel()
	_, cancelErr := reader.ReadAt(make([]byte, 1), 0)
	// Then
	if !errors.Is(err, ErrMetadataLimit) ||
		!errors.Is(nextErr, ErrMetadataLimit) {
		t.Fatalf("quota failure was lost: %v %v", err, nextErr)
	}
	if !errors.Is(cancelErr, context.Canceled) {
		t.Fatalf("cancellation was lost: %v", cancelErr)
	}
}
