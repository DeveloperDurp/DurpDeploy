package oidc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

var (
	ErrIdentityAmbiguous = errors.New("ambiguous OIDC email identity")
	ErrIdentityConflict  = errors.New("OIDC identity conflict")
	ErrIdentityInvalid   = errors.New("invalid OIDC identity")
	errIdentityRetry     = errors.New("retry OIDC identity resolution")
)

const identityResolveAttempts = 3

// ResolveIdentity resolves a ClaimIdentity returned by ParseClaims to one local user.
func ResolveIdentity(
	ctx context.Context,
	repo *repository.Repository,
	identity ClaimIdentity,
) (db.User, error) {
	switch identity.Role {
	case RoleAdmin, RoleDeployer, RoleViewer:
	default:
		return db.User{}, ErrIdentityInvalid
	}

	var retryErr error
	for range identityResolveAttempts {
		resolved, err := resolveIdentity(ctx, repo, identity)
		if !errors.Is(err, errIdentityRetry) {
			return resolved, err
		}
		retryErr = err
	}
	return db.User{}, fmt.Errorf(
		"resolve OIDC identity after retry: %w",
		retryErr,
	)
}

func resolveIdentity(
	ctx context.Context,
	repo *repository.Repository,
	identity ClaimIdentity,
) (db.User, error) {
	var resolved db.User
	if err := repo.WithTx(ctx, func(queries *db.Queries) error {
		identityRow, err := queries.GetOIDCIdentity(
			ctx,
			db.GetOIDCIdentityParams{
				Issuer: identity.Issuer, Subject: identity.Subject,
			},
		)
		var user db.User
		switch {
		case err == nil:
			user, err = queries.GetUserByID(ctx, identityRow.UserID)
			if err != nil {
				return fmt.Errorf("get identity user: %w", err)
			}
		case errors.Is(err, sql.ErrNoRows):
			userID, found, findErr := findUserIDByEmail(
				ctx,
				queries,
				identity.Email,
			)
			if findErr != nil {
				return findErr
			}
			if found {
				user, err = queries.GetUserByID(ctx, userID)
				if err != nil {
					return fmt.Errorf("get matched email user: %w", err)
				}
			} else {
				user, err = queries.CreateUser(ctx, db.CreateUserParams{
					Email: identity.Email, PasswordHash: "", Name: identity.Name,
					Role: string(identity.Role),
				})
				if err != nil {
					if !isUniqueConstraint(err) {
						return fmt.Errorf("create JIT user: %w", err)
					}
					userID, found, findErr = findUserIDByEmail(
						ctx,
						queries,
						identity.Email,
					)
					if findErr != nil {
						return findErr
					}
					if !found {
						return fmt.Errorf("create JIT user: %w", err)
					}
					user, findErr = queries.GetUserByID(ctx, userID)
					if findErr != nil {
						return fmt.Errorf("get raced JIT user: %w", findErr)
					}
				}
			}

			_, err = queries.CreateOIDCIdentity(
				ctx,
				db.CreateOIDCIdentityParams{
					Issuer: identity.Issuer, Subject: identity.Subject, UserID: user.ID,
				},
			)
			if err != nil {
				if !isUniqueConstraint(err) {
					return fmt.Errorf("create OIDC identity: %w", err)
				}
				existing, readErr := queries.GetOIDCIdentity(
					ctx,
					db.GetOIDCIdentityParams{
						Issuer: identity.Issuer, Subject: identity.Subject,
					},
				)
				switch {
				case readErr == nil && existing.UserID == user.ID:
				case readErr == nil:
					return ErrIdentityConflict
				case errors.Is(readErr, sql.ErrNoRows):
					return ErrIdentityConflict
				default:
					return fmt.Errorf("reread OIDC identity: %w", readErr)
				}
			}
		default:
			return fmt.Errorf("get OIDC identity: %w", err)
		}

		matchedUserID, found, err := findUserIDByEmail(
			ctx,
			queries,
			identity.Email,
		)
		if err != nil {
			return err
		}
		if found && matchedUserID != user.ID {
			return ErrIdentityConflict
		}

		roleChanged := user.Role != string(identity.Role)
		resolved, err = queries.UpdateOIDCUser(ctx, db.UpdateOIDCUserParams{
			Email: identity.Email, Name: identity.Name, Role: string(identity.Role),
			ID: user.ID, ExpectedRole: user.Role,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return errIdentityRetry
		}
		if err != nil {
			if isUniqueConstraint(err) {
				racedUserID, racedFound, findErr := findUserIDByEmail(
					ctx,
					queries,
					identity.Email,
				)
				if findErr != nil {
					return findErr
				}
				if racedFound && racedUserID != user.ID {
					return ErrIdentityConflict
				}
			}
			return fmt.Errorf("synchronize OIDC user claims: %w", err)
		}
		if roleChanged {
			if err := queries.DeleteSessionsByUser(ctx, user.ID); err != nil {
				return fmt.Errorf("invalidate browser sessions: %w", err)
			}
		}
		return nil
	}); err != nil {
		return db.User{}, fmt.Errorf("resolve OIDC identity: %w", err)
	}
	return resolved, nil
}

func findUserIDByEmail(
	ctx context.Context,
	queries *db.Queries,
	email string,
) (int64, bool, error) {
	// ponytail: O(n) small-team email lookup; add a case-folded unique index/query if user counts make callback latency matter.
	users, err := queries.ListUsers(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("list users for email link: %w", err)
	}
	var userID int64
	for _, user := range users {
		if !strings.EqualFold(user.Email, email) {
			continue
		}
		if userID != 0 {
			return 0, false, ErrIdentityAmbiguous
		}
		userID = user.ID
	}
	return userID, userID != 0, nil
}

func isUniqueConstraint(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") ||
		strings.Contains(message, "duplicate")
}
