package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
	"time"
)

// Validate rejects unsafe arithmetic and combinations before consumers allocate
// buffers, convert counts to int, add sentinel bytes, or create containers.
func (l Limits) Validate() error {
	if err := visitFields(reflect.ValueOf(l), "limits", validateNumber); err != nil {
		return err
	}
	if err := l.validateRelationships(); err != nil {
		return err
	}
	return l.validateCapacity()
}

func validateNumber(value reflect.Value, path, _ string) error {
	if value.Kind() != reflect.Int64 {
		return nil
	}
	n := value.Int()
	minimum := int64(1)
	if path == "limits.execution.max_retries" {
		minimum = 0
	}
	if n < minimum || n >= int64(^uint(0)>>1) {
		return fmt.Errorf("%s is outside the supported range", path)
	}
	if strings.HasSuffix(path, "_seconds") &&
		n > math.MaxInt64/int64(time.Second) {
		return fmt.Errorf("%s exceeds the duration range", path)
	}
	return nil
}

func (l Limits) validateRelationships() error {
	if l.Execution.DefaultTimeoutSeconds > l.Execution.MaxTimeoutSeconds {
		return errors.New("default timeout exceeds maximum timeout")
	}
	if l.Transfer.PerAgent > l.Transfer.Global {
		return errors.New("per-agent transfers exceed global transfers")
	}
	if l.Artifact.StagingNodes < l.Artifact.Files {
		return errors.New("artifact staging nodes must cover the file count")
	}
	if l.Staging.ArchiveBytes < 1024 ||
		l.Staging.PayloadBytes > l.Staging.ArchiveBytes-1024 {
		return errors.New("staging archive must allow payload and TAR framing")
	}
	if l.HTTP.PasswordFormBytes > l.HTTP.FormBytes {
		return errors.New("password form limit exceeds general form limit")
	}
	if l.HTTP.RepositoryFormBytes > l.HTTP.FormBytes {
		return errors.New("repository form limit exceeds general form limit")
	}
	if l.HTTP.ReadHeaderTimeoutSeconds > l.HTTP.ReadTimeoutSeconds {
		return errors.New("HTTP header timeout exceeds read timeout")
	}
	// Three text fields can each require six JSON bytes per input byte.
	// Leave room for the envelope and row metadata, even on the first row.
	if l.Logs.LineBytes < 128 || l.Logs.PageBytes < 2048 ||
		l.Logs.LineBytes > (l.Logs.PageBytes-2048)/18 {
		return errors.New("log page must hold one escaped preview and metadata")
	}
	return nil
}

func (l Limits) validateCapacity() error {
	maxInt := int64(^uint(0) >> 1)
	if math.IsNaN(l.Container.HelperCPUs) ||
		math.IsInf(l.Container.HelperCPUs, 0) ||
		l.Container.HelperCPUs < 1e-9 ||
		l.Container.HelperCPUs >= float64(maxInt)/1e9 {
		return errors.New(
			"container helper CPUs are outside the supported range",
		)
	}
	if l.Artifact.DownloadBytes > maxInt-l.Container.TmpfsBytes {
		return errors.New("artifact temporary filesystem size overflows")
	}
	page := int64(os.Getpagesize())
	for _, stage := range []struct{ bytes, entries int64 }{
		{l.Staging.PayloadBytes, l.Staging.Entries},
		{l.Artifact.ExtractedBytes, l.Artifact.StagingNodes},
	} {
		if stage.entries > (maxInt-stage.bytes)/page ||
			stage.entries > (maxInt-2)/2 {
			return errors.New("staging temporary filesystem capacity overflows")
		}
		capacity := stage.bytes + stage.entries*page
		if capacity > maxInt-l.Container.HelperMemoryOverheadBytes {
			return errors.New("staging helper memory capacity overflows")
		}
	}
	return nil
}
