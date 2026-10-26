package handler

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

type textExportWriter struct {
	*httptest.ResponseRecorder
	write func([]byte) error
}

func (w *textExportWriter) Write(p []byte) (int, error) {
	if err := w.write(p); err != nil {
		return 0, err
	}
	return w.ResponseRecorder.Write(p)
}

func TestTextLogExportCancellation(t *testing.T) {
	repo := setupTestRepo(t)
	seedStreamTestDeployment(t, repo)
	for index := range 300 {
		if _, err := repo.Queries.CreateDeploymentLog(t.Context(),
			db.CreateDeploymentLogParams{DeploymentID: 1,
				Line: fmt.Sprintf("line-%03d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	lines := 0
	w := &textExportWriter{ResponseRecorder: httptest.NewRecorder(),
		write: func(p []byte) error {
			if strings.HasPrefix(string(p), "[") {
				lines++
				cancel()
			}
			return nil
		}}
	NewLogHandler(nil, repo).exportDeploymentLogs(w,
		httptest.NewRequest("GET", "/", nil).WithContext(ctx), 1)
	if lines == 0 || lines >= 300 || w.Code != 200 ||
		strings.HasSuffix(w.Body.String(), "\n\n") ||
		repo.DB.Stats().InUse != 0 {
		t.Fatalf(
			"cancelled export did not stop within its batch: lines=%d",
			lines,
		)
	}
}

func TestTextLogExportWriteFailure(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			repo := setupTestRepo(t)
			seedStreamTestDeployment(t, repo)
			if _, err := repo.Queries.CreateDeploymentLog(t.Context(),
				db.CreateDeploymentLogParams{DeploymentID: 1, Line: "log"}); err != nil {
				t.Fatal(err)
			}
			writes := 0
			w := &textExportWriter{ResponseRecorder: httptest.NewRecorder(),
				write: func([]byte) error {
					writes++
					if writes == failAt {
						return io.ErrClosedPipe
					}
					return nil
				}}
			NewLogHandler(nil, repo).exportDeploymentLogs(w,
				httptest.NewRequest("GET", "/", nil), 1)
			if writes != failAt || repo.DB.Stats().InUse != 0 {
				t.Fatal(
					"export continued after a failed write or retained a connection",
				)
			}
		})
	}
}
