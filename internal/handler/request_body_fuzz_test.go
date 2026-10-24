package handler_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"testing/iotest"

	"durpdeploy/internal/handler"
)

func FuzzReadJSON(f *testing.F) {
	for _, seed := range []string{
		`{}`, `{"name":"synthetic"}`, `{"name":"é\u0000"}`,
		``, `null`, `[]`, `{"extra":true}`, `{"name":1}`,
		`{} {}`, `{} junk`, `{"name":`, "{\xff}",
	} {
		f.Add([]byte(seed), false)
		f.Add([]byte(seed), true)
	}
	f.Fuzz(fuzzReadJSON)
}

func fuzzReadJSON(t *testing.T, raw []byte, chunked bool) {
	if len(raw) > 64<<10 {
		t.Skip()
	}
	var input struct {
		Name string `json:"name"`
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/", bytes.NewReader(raw))
	if chunked {
		request.Body = io.NopCloser(iotest.OneByteReader(request.Body))
		request.ContentLength = -1
	}
	accepted := handler.ReadJSON(recorder, request, &input)
	var baseline struct {
		Name string `json:"name"`
	}
	other := httptest.NewRecorder()
	plain := httptest.NewRequest("POST", "/", bytes.NewReader(raw))
	if accepted != handler.ReadJSON(other, plain, &baseline) ||
		input != baseline || recorder.Body.String() != other.Body.String() {
		t.Fatal("chunking changed JSON decoding or public error")
	}
	if accepted && (!json.Valid(raw) ||
		len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{') {
		t.Fatal("accepted a non-object or incomplete JSON document")
	}
	if !accepted && (recorder.Code != 400 ||
		recorder.Body.String() != "{\"error\":\"Invalid JSON body\"}\n") {
		t.Fatal("parser exposed an unstable rejection")
	}
	empty := httptest.NewRecorder()
	request = httptest.NewRequest("POST", "/", bytes.NewReader(raw))
	var object map[string]json.RawMessage
	wantEmpty := len(raw) == 0 ||
		(json.Unmarshal(raw, &object) == nil && object != nil &&
			len(object) == 0)
	if handler.ReadEmptyJSON(empty, request) != wantEmpty {
		t.Fatal("empty-body control accepted fields or invalid JSON")
	}
	fuzzBoundedBody(t, raw, chunked)
}

func fuzzBoundedBody(t *testing.T, raw []byte, chunked bool) {
	t.Helper()
	const limit = 256
	reader := bytes.NewReader(raw)
	request := httptest.NewRequest("POST", "/", reader)
	if chunked {
		request.ContentLength = -1
	}
	data, err := handler.ReadRequestBody(
		httptest.NewRecorder(), request, limit,
	)
	if len(raw)-reader.Len() > limit+1 {
		t.Fatal("body reader exceeded its encoded-byte budget")
	}
	if len(raw) <= limit {
		if err != nil || !bytes.Equal(data, raw) {
			t.Fatal("bounded body changed a valid payload")
		}
	} else if err == nil {
		t.Fatal("accepted an oversized body")
	}
}
