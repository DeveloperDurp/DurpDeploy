package httpstream

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
)

var errStreamAccessRevoked = errors.New("stream access revoked")

type logStream struct {
	repo         *repository.Repository
	broker       *runner.LogBroker
	deploymentID int64
	after        int64
	format       string
	preview      bool
}

// StreamTextLogs shares durable replay, completion and authorization with the
// structured stream while retaining the historical text/NDJSON wire shapes.
func StreamTextLogs(
	w http.ResponseWriter,
	r *http.Request,
	repo *repository.Repository,
	broker *runner.LogBroker,
	deploymentID int64,
	ndjson bool,
) {
	after, err := LogCursor(r)
	if err != nil {
		auth.RenderRequestError(
			w,
			r,
			http.StatusBadRequest,
			"Invalid log cursor",
		)
		return
	}
	format := "sse"
	if ndjson {
		format = "ndjson"
	}
	streamLogs(
		w,
		r,
		logStream{
			repo:         repo,
			broker:       broker,
			deploymentID: deploymentID,
			after:        after,
			format:       format,
		},
	)
}

func writeStreamLog(
	w io.Writer,
	log db.DeploymentLog,
	format string,
	live bool,
) error {
	switch format {
	case "structured":
		data, err := json.Marshal(StepLogFromRow(log))
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(
			w,
			"id: %d\nevent: log\ndata: %s\n\n",
			log.ID,
			data,
		)
		return err
	case "ndjson":
		step := ""
		if !live && log.StepName.Valid {
			step = log.StepName.String
		}
		return json.NewEncoder(w).Encode(struct {
			Line string `json:"line"`
			Step string `json:"step"`
		}{Line: log.Line, Step: step})
	default:
		line := strings.ReplaceAll(
			strings.ReplaceAll(log.Line, "\r\n", "\n"),
			"\r",
			"\n",
		)
		frame := "data: " + strings.ReplaceAll(line, "\n", "\ndata: ") + "\n\n"
		if live {
			frame = fmt.Sprintf("id: %d\n", log.ID) + frame
		}
		_, err := io.WriteString(w, frame)
		return err
	}
}
