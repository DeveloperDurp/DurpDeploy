package repository

import (
	"context"
	"errors"
	"time"
)

const (
	sqliteBusyCode              = 5
	deploymentCreationBusyTries = 5
)

type sqliteErrorCoder interface {
	Code() int
}

func retryDeploymentCreation(
	ctx context.Context,
	err error,
	attempt int,
) bool {
	var sqliteErr sqliteErrorCoder
	if attempt+1 >= deploymentCreationBusyTries ||
		!errors.As(err, &sqliteErr) ||
		sqliteErr.Code()&0xff != sqliteBusyCode {
		return false
	}
	timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
