package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/swagger"
)

func TestBearerLintOpenAPIContract(t *testing.T) {
	data, err := swagger.ReadSpec()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	object := func(value any) map[string]any {
		t.Helper()
		m, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("expected schema object, got%T", value)
		}
		return m
	}
	op := object(object(object(document["paths"])["/lint"])["post"])
	if op["operationId"] != "lintScriptWarnings" ||
		!reflect.DeepEqual(
			op["security"],
			[]any{map[string]any{"bearer": []any{}}},
		) {
		t.Fatal("lint operation identity or authentication differs")
	}
	parameters, ok := op["parameters"].([]any)
	if !ok || len(parameters) != 1 {
		t.Fatal("unexpected request parameters")
	}
	parameter := object(parameters[0])
	request := object(parameter["schema"])
	properties := object(request["properties"])
	if parameter["in"] != "body" || parameter["required"] != true ||
		request["additionalProperties"] != false ||
		request["x-max-json-bytes"] != float64(maxLintBodyBytes) ||
		len(properties) != 2 ||
		!reflect.DeepEqual(request["required"], []any{"profile", "script"}) ||
		!reflect.DeepEqual(
			object(properties["profile"])["enum"],
			[]any{handler.BearerLintProfile},
		) ||
		object(properties["script"])["x-max-utf8-bytes"] != float64(
			handler.MaxBearerLintScriptBytes,
		) {
		t.Fatal("strict request contract differs")
	}
	responses := object(op["responses"])
	response := object(object(responses["200"])["schema"])
	responseProperties := object(response["properties"])
	diagnostics := object(responseProperties["diagnostics"])
	if response["additionalProperties"] != false ||
		response["x-max-json-bytes"] != float64(maxLintResponseBytes) ||
		len(responseProperties) != 3 ||
		diagnostics["type"] != "array" ||
		diagnostics["maxItems"] != float64(handler.MaxBearerLintDiagnostics) ||
		object(responseProperties["truncated"])["type"] != "boolean" {
		t.Fatal("complete response contract differs")
	}
	for _, status := range []string{"400", "401", "403", "408", "413", "415", "500", "503", "504"} {
		if responses[status] == nil {
			t.Fatalf("missing status%s", status)
		}
	}
}

type lintObservedBody struct {
	io.ReadCloser
	started chan<- struct{}
	once    sync.Once
}

func (b *lintObservedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { b.started <- struct{}{} })
	return b.ReadCloser.Read(p)
}

func TestBearerLintHTTPAdmissionAndCancellation(t *testing.T) {
	h := NewLintHandler()
	started := make(chan struct{}, 8)
	finished := make(chan struct{}, 8)
	srv := newLintAdmissionServer(t, h, started, finished)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	writers, results := openLintRequests(t, ctx, srv, started)
	req, err := http.NewRequestWithContext(
		ctx,
		"POST",
		srv.URL+"/api/v1/lint",
		strings.NewReader(lintTestBody("#!")),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 503 || len(h.slots) != 4 {
		t.Fatalf(
			"admission status%d slots%d",
			response.StatusCode,
			len(h.slots),
		)
	}
	// Keep the four admitted requests open. Neither rejected HTTP/1 body is
	// complete, so an implicit server drain would delay its response forever.
	for _, framing := range []struct {
		name, header, partial string
	}{
		{"length", "Content-Length: 100\r\n", "{"},
		{"chunked", "Transfer-Encoding: chunked\r\n", "1\r\n{\r\n"},
	} {
		assertLintBusyUpload(t, srv.Listener.Addr().String(), h,
			framing.name, framing.header, framing.partial)
	}
	finishLintOccupants(t, ctx, writers, results, finished)
	if len(h.slots) != 0 {
		t.Fatal("admission slots leaked")
	}
	assertLintHTTPCancellation(t, ctx, srv, h, started, finished)
}

func finishLintOccupants(
	t *testing.T, ctx context.Context, writers []*io.PipeWriter,
	results <-chan int, finished <-chan struct{},
) {
	t.Helper()
	for _, writer := range writers {
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for range 4 {
		select {
		case status := <-results:
			if status != 400 {
				t.Fatalf("partial request status%d", status)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	for range 7 {
		select {
		case <-finished:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func newLintAdmissionServer(
	t *testing.T, h *LintHandler, started, finished chan<- struct{},
) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(
		handler.InternalErrorMiddleware(
			RequestBodyLimit(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer func() { finished <- struct{}{} }()
					if r.Header.Get("X-Observe-Body") == "yes" {
						r.Body = &lintObservedBody{
							ReadCloser: r.Body,
							started:    started,
						}
					}
					h.Analyze(w, r)
				}),
			),
		),
	)
	t.Cleanup(srv.Close)
	return srv
}

func openLintRequests(
	t *testing.T,
	ctx context.Context,
	srv *httptest.Server,
	started <-chan struct{},
) ([]*io.PipeWriter, <-chan int) {
	t.Helper()
	writers := make([]*io.PipeWriter, 0, 4)
	t.Cleanup(func() {
		for _, w := range writers {
			_ = w.Close()
		}
	})
	results := make(chan int, 4)
	for range 4 {
		reader, writer := io.Pipe()
		writers = append(writers, writer)
		req, err := http.NewRequestWithContext(
			ctx,
			"POST",
			srv.URL+"/api/v1/lint",
			reader,
		)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Observe-Body", "yes")
		go func() {
			response, err := srv.Client().Do(req)
			if err != nil {
				results <- 0
				return
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			results <- response.StatusCode
		}()
		if _, err := writer.Write([]byte("{")); err != nil {
			t.Fatal(err)
		}
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	return writers, results
}

func assertLintBusyUpload(
	t *testing.T, address string, h *LintHandler, name, header, partial string,
) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(conn,
		"POST /api/v1/lint HTTP/1.1\r\nHost: localhost\r\n"+
			"Content-Type: application/json\r\n%s\r\n%s", header, partial)
	if err != nil {
		t.Fatal(err)
	}
	busy, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("%s busy response waited for body: %v", name, err)
	}
	defer busy.Body.Close()
	body, err := io.ReadAll(io.LimitReader(busy.Body, 32_769))
	if err != nil {
		t.Fatal(err)
	}
	var failure map[string]string
	if busy.StatusCode != 503 || !busy.Close || len(h.slots) != 4 ||
		busy.Header.Get("Cache-Control") != "no-store" ||
		json.Unmarshal(body, &failure) != nil ||
		failure["error"] != "internal server error" {
		t.Fatalf("%s invalid busy response: %d %s", name, busy.StatusCode, body)
	}
}

func assertLintHTTPCancellation(
	t *testing.T, ctx context.Context, srv *httptest.Server, h *LintHandler,
	started, finished <-chan struct{},
) {
	t.Helper()
	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	requestCtx, stop := context.WithCancel(ctx)
	defer stop()
	req, err := http.NewRequestWithContext(
		requestCtx,
		"POST",
		srv.URL+"/api/v1/lint",
		reader,
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	requestDone := make(chan error, 1)
	req.Header.Set("X-Observe-Body", "yes")
	go func() {
		response, err := srv.Client().Do(req)
		if response != nil {
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	if _, err := writer.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	stop()
	_ = writer.CloseWithError(context.Canceled)
	select {
	case err := <-requestDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error%v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if len(h.slots) != 0 {
		t.Fatal("canceled request retained a slot")
	}
}

type lintPartialWriter struct{ http.ResponseWriter }

func (w lintPartialWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p[:len(p)/2])
	if err != nil {
		return n, err
	}
	return n, io.ErrShortWrite
}

func TestBearerLintHTTPReadDeadlineAndPartialDelivery(t *testing.T) {
	h := NewLintHandler()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(h.Analyze))
	srv.Config.ReadTimeout = 100 * time.Millisecond
	srv.Config.WriteTimeout = time.Second
	srv.Start()
	t.Cleanup(srv.Close)
	conn, err := net.DialTimeout(
		"tcp",
		srv.Listener.Addr().String(),
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprint(conn, "POST /api/v1/lint HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{"); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 408 || !json.Valid(body) {
		t.Fatalf(
			"read timeout: status%d body%s err%v",
			response.StatusCode,
			body,
			err,
		)
	}
	partial := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) { h.Analyze(lintPartialWriter{w}, r) },
		),
	)
	t.Cleanup(partial.Close)
	req, err := http.NewRequestWithContext(
		t.Context(),
		"POST",
		partial.URL,
		strings.NewReader(lintTestBody("#!")),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err = partial.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 || !errors.Is(err, io.ErrUnexpectedEOF) ||
		json.Valid(body) {
		t.Fatalf(
			"partial response accepted: status%d body%s err%v",
			response.StatusCode,
			body,
			err,
		)
	}
}

func lintTestBody(script string) string {
	data, _ := json.Marshal(map[string]string{
		"profile": handler.BearerLintProfile, "script": script,
	})
	return string(data)
}

func TestBearerLintStrictJSONAndMedia(t *testing.T) {
	valid := lintTestBody("#!\nif then")
	cases := []struct {
		body, media string
		status      int
	}{
		{valid, "application/json", 200},
		{valid, "application/json; charset=UTF-8", 200},
		{valid, "application/json; charset=\"\"", 415},
		{valid, "application/json; charset=latin1", 415},
		{valid, "text/plain", 415}, {valid, "", 415},
		{valid, "application/json; charset=utf-8; charset=latin1", 415},
		{`null`, "application/json", 400},
		{`[]`, "application/json", 400},
		{`{}`, "application/json", 400},
		{
			`{"profile":"builtin-shell-warnings-v1","script":null}`,
			"application/json",
			400,
		},
		{
			`{"profile":"builtin-shell-warnings-v1","script":true}`,
			"application/json",
			400,
		},
		{
			`{"profile":"builtin-shell-warnings-v1","script":[]}`,
			"application/json",
			400,
		},
		{
			`{"Profile":"builtin-shell-warnings-v1","script":""}`,
			"application/json",
			400,
		},
		{`{"profile":"other","script":""}`, "application/json", 400},
		{
			`{"profile":"builtin-shell-warnings-v1","script":"","path":"/private"}`,
			"application/json",
			400,
		},
		{
			`{"profile":"builtin-shell-warnings-v1","script":"","script":"x"}`,
			"application/json",
			400,
		},
		{
			`{"profile":"builtin-shell-warnings-v1","script":"","scr\u0069pt":"x"}`,
			"application/json",
			400,
		},
		{valid + `{}`, "application/json", 400},
		{
			`{"profile":"builtin-shell-warnings-v1","script":"\ud800"}`,
			"application/json",
			400,
		},
		{
			`{"profile":"builtin-shell-warnings-v1","script":"\udc00"}`,
			"application/json",
			400,
		},
		{
			`{"profile":"builtin-shell-warnings-v1","script":"\ud800\u0061"}`,
			"application/json",
			400,
		},
		{
			`{"profile":"builtin-shell-warnings-v1","script":"\ud83d\ude00"}`,
			"application/json",
			200,
		},
		{
			`{"profile":"builtin-shell-warnings-v1","script":"\\ud800"}`,
			"application/json",
			200,
		},
		{lintTestBody("�"), "application/json", 200},
		{lintTestBody("\x00"), "application/json", 400},
		{strings.Replace(valid, "if then", "\xff", 1), "application/json", 400},
	}
	for n, tc := range cases {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			r := httptest.NewRequest(
				"POST",
				"/api/v1/lint",
				strings.NewReader(tc.body),
			)
			r.Header.Set("Content-Type", tc.media)
			w := httptest.NewRecorder()
			handler.InternalErrorMiddleware(http.HandlerFunc(NewLintHandler().Analyze)).
				ServeHTTP(w, r)
			if w.Code != tc.status ||
				w.Header().Get("Cache-Control") != "no-store" ||
				!json.Valid(w.Body.Bytes()) ||
				w.Body.Len() > maxLintResponseBytes {
				t.Fatalf(
					"status %d, want %d, body %s",
					w.Code,
					tc.status,
					w.Body.String(),
				)
			}
		})
	}
}

func TestBearerLintSizeBoundariesAndResponse(t *testing.T) {
	assertLintDecodedSizeBoundaries(t)
	assertLintEncodedSizeBoundaries(t)
	assertLintWarningResponseBounds(t)
	limits := config.Defaults().Limits.HTTP
	limits.JSONBytes = 64
	r := httptest.NewRequest(
		"POST", "/api/v1/lint",
		strings.NewReader(lintTestBody(strings.Repeat("x", 100))),
	)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.WithRequestBodyLimits(limits)(
		http.HandlerFunc(NewLintHandler().Analyze),
	).ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatalf("configured smaller ceiling: %d", w.Code)
	}
}

func assertLintDecodedSizeBoundaries(t *testing.T) {
	t.Helper()
	for _, size := range []int{65_535, 65_536, 65_537} {
		for _, unknownLength := range []bool{false, true} {
			r := httptest.NewRequest(
				"POST",
				"/api/v1/lint",
				strings.NewReader(lintTestBody(strings.Repeat("x", size))),
			)
			if unknownLength {
				r.ContentLength = -1
			}
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			NewLintHandler().Analyze(w, r)
			want := 200
			if size > 65_536 {
				want = 413
			}
			if w.Code != want {
				t.Fatalf("decoded %d/%v status %d", size, unknownLength, w.Code)
			}
		}
	}
}

func assertLintEncodedSizeBoundaries(t *testing.T) {
	t.Helper()
	for _, size := range []int{maxLintBodyBytes - 1, maxLintBodyBytes, maxLintBodyBytes + 1} {
		for _, unknownLength := range []bool{false, true} {
			base := lintTestBody("#!")
			r := httptest.NewRequest(
				"POST",
				"/api/v1/lint",
				strings.NewReader(base+strings.Repeat(" ", size-len(base))),
			)
			if unknownLength {
				r.ContentLength = -1
			}
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			NewLintHandler().Analyze(w, r)
			want := 200
			if size > maxLintBodyBytes {
				want = 413
			}
			if w.Code != want {
				t.Fatalf("encoded %d/%v status %d", size, unknownLength, w.Code)
			}
		}
	}
}

func assertLintWarningResponseBounds(t *testing.T) {
	t.Helper()
	for _, count := range []int{0, 99, 100, 101} {
		r := httptest.NewRequest(
			"POST",
			"/api/v1/lint",
			strings.NewReader(
				lintTestBody("#!\n"+strings.Repeat("$PRIVATE ", count)),
			),
		)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		NewLintHandler().Analyze(w, r)
		var result lintResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil ||
			result.Diagnostics == nil ||
			len(result.Diagnostics) != min(count, 100) ||
			result.Truncated != (count > 100) ||
			w.Header().Get("Content-Length") != fmt.Sprint(w.Body.Len()) ||
			strings.Contains(w.Body.String(), "PRIVATE") {
			t.Fatalf("response count%d: %d %s", count, w.Code, w.Body.String())
		}
	}
}

type lintUnreadableBody struct{}

func (lintUnreadableBody) Read(
	[]byte,
) (int, error) {
	panic("busy/canceled lint read its body")
}
func (lintUnreadableBody) Close() error { return nil }

func TestBearerLintAdmissionCancellationAndFailure(t *testing.T) {
	h := NewLintHandler()
	for range cap(h.slots) {
		h.slots <- struct{}{}
	}
	r := httptest.NewRequest("POST", "/api/v1/lint", nil)
	r.Body = lintUnreadableBody{}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.InternalErrorMiddleware(http.HandlerFunc(h.Analyze)).ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), "busy/canceled") {
		t.Fatalf("admission %d: %s", w.Code, w.Body.String())
	}
	for range cap(h.slots) {
		<-h.slots
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r = r.WithContext(ctx)
	w = httptest.NewRecorder()
	handler.InternalErrorMiddleware(http.HandlerFunc(h.Analyze)).ServeHTTP(w, r)
	if w.Code != 408 {
		t.Fatalf("cancellation status%d", w.Code)
	}
	deadline, stop := context.WithDeadline(
		t.Context(),
		time.Now().Add(-time.Second),
	)
	defer stop()
	_, _, err := handler.LintBearerWarnings(deadline, "#!")
	w = httptest.NewRecorder()
	handler.InternalErrorMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeLintAnalysisError(w, r, err) })).
		ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/lint", nil))
	if w.Code != 504 {
		t.Fatalf("deadline status%d", w.Code)
	}
	for range 8 {
		r := httptest.NewRequest(
			"POST",
			"/api/v1/lint",
			strings.NewReader("invalid"),
		)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.Analyze(w, r)
		if w.Code != 400 || len(h.slots) != 0 {
			t.Fatalf("slot leaked: status%d slots%d", w.Code, len(h.slots))
		}
	}
}
