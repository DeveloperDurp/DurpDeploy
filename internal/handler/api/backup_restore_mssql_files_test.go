//go:build e2e && backuprestore && linux

package api_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// The fixture creates one data file and one log file. Reject other layouts.
func restoreMSSQLFiles(
	ctx context.Context,
	conn *sql.DB,
	archive string,
) (moves string, err error) {
	rows, err := conn.QueryContext(
		ctx,
		"RESTORE FILELISTONLY FROM DISK = N'"+archive+"'",
	)
	if err != nil {
		return "", fmt.Errorf("inspect backup files: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	columns, err := rows.Columns()
	if err != nil {
		return "", fmt.Errorf("read backup columns: %w", err)
	}
	if len(columns) < 3 || columns[0] != "LogicalName" || columns[2] != "Type" {
		return "", errors.New("unexpected backup file metadata")
	}
	values := make([]any, len(columns))
	destinations := make([]any, len(columns))
	for i := range destinations {
		destinations[i] = &values[i]
	}
	files := map[string]string{}
	for rows.Next() {
		if err := rows.Scan(destinations...); err != nil {
			return "", fmt.Errorf("read backup file: %w", err)
		}
		name, nameOK := values[0].(string)
		kind, kindOK := values[2].(string)
		if !nameOK || !kindOK || (kind != "D" && kind != "L") ||
			files[kind] != "" {
			return "", errors.New("unexpected backup file layout")
		}
		files[kind] = strings.ReplaceAll(name, "'", "''")
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("inspect backup layout: %w", err)
	}
	if len(files) != 2 {
		return "", errors.New("backup lacks the expected data and log files")
	}
	return fmt.Sprintf("MOVE N'%s' TO N'/var/opt/mssql/data/recovered.mdf', "+
		"MOVE N'%s' TO N'/var/opt/mssql/data/recovered_log.ldf'", files["D"], files["L"]), nil
}
