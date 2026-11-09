package repository

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	mssql "github.com/microsoft/go-mssqldb"
)

var (
	ErrEnvironmentReferenced = errors.New("environment is referenced")
	ErrDeploymentParent      = errors.New(
		"release or environment is no longer available; refresh and try again",
	)
	ErrVariableParent = errors.New(
		"project or environment is no longer available; refresh and try again",
	)
)

// isForeignKeyViolation excludes other constraints, including SQL Server CHECK
// failures and SQLite trigger failures that share the same numeric code.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	var msErr mssql.Error
	if errors.As(err, &msErr) {
		return msForeignKey(msErr)
	}
	var msPtr *mssql.Error
	if errors.As(err, &msPtr) && msPtr != nil {
		return msForeignKey(*msPtr)
	}
	var code interface{ Code() int }
	if errors.As(err, &code) {
		return code.Code() == 787 || (code.Code() == 1811 &&
			strings.Contains(err.Error(), "FOREIGN KEY constraint failed"))
	}
	return false
}

func msForeignKey(err mssql.Error) bool {
	message := strings.ToUpper(err.Message)
	return err.Number == 547 &&
		(strings.Contains(message, "FOREIGN KEY") ||
			strings.Contains(message, "REFERENCE CONSTRAINT"))
}
