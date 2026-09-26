package handler

import (
	"database/sql"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var runbookScheduleParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

// ParseAndValidateCron accepts server-local, satisfiable five-field schedules.
func ParseAndValidateCron(expr string) (cron.Schedule, error) {
	if strings.HasPrefix(expr, "TZ=") || strings.HasPrefix(expr, "CRON_TZ=") {
		return nil, sql.ErrNoRows
	}
	schedule, err := runbookScheduleParser.Parse(expr)
	if err != nil {
		return nil, err
	}
	if schedule.Next(time.Now()).IsZero() {
		return nil, sql.ErrNoRows
	}
	return schedule, nil
}
