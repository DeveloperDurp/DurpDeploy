package artifact

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func FuzzReviewJSONSyntax(f *testing.F) {
	for _, seed := range []string{
		`{"a":[null,true,false,-12.3e+2,"\uD800"]}`, `0`, `[]`, `1e-`,
		`"\uFFFF"`, `{"a":01}`, `{} {}`, `"\q"`, `{"a":[1,]}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		s := newReviewJSON(iotest.OneByteReader(bytes.NewReader(raw)))
		err := s.skip()
		if err == nil {
			err = s.finish()
		}
		if (err == nil) != json.Valid(raw) {
			t.Fatalf("validation differs: %q err=%v", raw, err)
		}
	})
}

func TestReviewJSONLargeIgnoredTokens(t *testing.T) {
	const padding = 2 << 20
	for _, pair := range [][2]string{
		{`{"`, `":null,"format_version":"1.2"}`},
		{`{"metadata":1`, `,"format_version":"1.2"}`},
		{`{"format_version":"1.2`, `"}`},
	} {
		// A long unused member name, number, or version must remain valid.
		fill := byte('x')
		if strings.Contains(pair[0], "metadata") {
			fill = '1'
		}
		raw := io.MultiReader(
			strings.NewReader(pair[0]),
			bytes.NewReader(
				bytes.Repeat([]byte{fill}, padding),
			),
			strings.NewReader(pair[1]),
		)
		if _, err := ParseGateReviewReader(raw, "terraform"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGateBundleStreamsExactReviewBytes(t *testing.T) {
	review := " \n" + `{"format_\u0076ersion":"1.2","prior_state":"hidden","resource_changes":[{"change":{"actions":["create"]}}]}` + "\t\n"
	var source bytes.Buffer
	writer := tar.NewWriter(&source)
	for _, entry := range [][2]string{{"review.json", review}, {"tfplan", "exact-plan"}} {
		if err := writer.WriteHeader(&tar.Header{Name: entry[0], Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(entry[1]))}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(writer, entry[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var target bytes.Buffer
	checksum, counts, err := CopyGateBundle(
		context.Background(),
		iotest.OneByteReader(&source),
		&target,
		"tfplan",
		"review.json",
		"terraform",
	)
	hash := sha256.Sum256([]byte("exact-plan"))
	if err != nil || checksum != hex.EncodeToString(hash[:]) ||
		counts.Create != 1 {
		t.Fatalf("checksum=%s counts=%+v err=%v", checksum, counts, err)
	}
	reader := tar.NewReader(&target)
	if _, err := reader.Next(); err != nil {
		t.Fatal(err)
	}
	retained, err := io.ReadAll(reader)
	if err != nil || string(retained) != review {
		t.Fatalf("review bytes changed: err=%v", err)
	}
}

func TestReviewJSONPropagatesReadFailure(t *testing.T) {
	failure := errors.New("reader failed")
	for _, details := range []bool{false, true} {
		reader := io.MultiReader(
			strings.NewReader(`{"format_version":"1.2","prior_state":`),
			iotest.ErrReader(failure),
		)
		var err error
		if details {
			_, err = TerraformReviewReader(reader, nil)
		} else {
			_, err = ParseGateReviewReader(reader, "terraform")
		}
		if !errors.Is(err, failure) || !errors.Is(err, ErrGateConfig) {
			t.Fatalf("details=%v err=%v", details, err)
		}
	}
}
