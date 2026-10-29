package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/testutil"
)

func TestGotifyDestinationBinding(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(
			t, newDeploymentCreationEngine(t, name),
		)
		for _, scope := range []string{"project", "global"} {
			t.Run(scope, func(t *testing.T) {
				for _, test := range []struct {
					name, before, after, replacement, want string
					clear                                  int64
				}{
					{"unchanged", "https://example.com/App", "https://example.com/App", "", "old", 0},
					{"host", "https://example.com/App", "https://other.example/App", "", "", 0},
					{"case", "https://example.com/App", "https://example.com/app", "", "", 0},
					{"port", "https://example.com/App", "https://example.com:443/App", "", "", 0},
					{"slash", "https://example.com/App", "https://example.com/App/", "", "", 0},
					{"escaping", "https://example.com/App", "https://example.com/%41pp", "", "", 0},
					{"query", "https://example.com/App", "https://example.com/App?tenant=b", "", "", 0},
					{"remove", "https://example.com/App", "", "", "", 0},
					{"restore", "", "https://example.com/App", "", "", 0},
					{"disabled", "", "", "", "old", 0},
					{"replace", "https://example.com/App", "https://other.example/App", "new", "new", 0},
					{"raw replacement", "https://example.com/App", "https://other.example/App", " ", " ", 0},
					{"ignorable replacement", "https://example.com/App", "https://other.example/App", "\u200b", "\u200b", 0},
					{"clear", "https://example.com/App", "https://example.com/App", "", "", 1},
					{"clear wins", "https://example.com/App", "https://other.example/App", "new", "", 1},
				} {
					t.Run(test.name, func(t *testing.T) {
						initial := db.UpdateGlobalNotificationsParams{
							GotifyUrl: nullable(
								test.before,
							), GotifyToken: nullable("old"),
							SlackWebhookUrl: nullable(
								"https://example.com/slack",
							),
							DiscordWebhookUrl: nullable(
								"https://example.com/discord",
							),
							NotifyEmails: nullable("saved@example.com"),
						}
						if err := updateGotifySettings(
							t.Context(),
							repo,
							scope,
							initial,
						); err != nil {
							t.Fatal(err)
						}
						update := db.UpdateGlobalNotificationsParams{
							GotifyUrl: nullable(test.after),
							GotifyToken: sql.NullString{
								String: test.replacement,
								Valid:  true,
							},
							ClearGotifyToken: test.clear,
							NotifyEmails:     nullable("edited@example.com"),
						}
						if err := updateGotifySettings(
							t.Context(),
							repo,
							scope,
							update,
						); err != nil {
							t.Fatal(err)
						}
						stored := readGotifySettings(t, repo, scope)
						if stored.GotifyUrl.String != test.after ||
							stored.GotifyToken.String != test.want ||
							stored.SlackWebhookUrl != initial.SlackWebhookUrl ||
							stored.DiscordWebhookUrl != initial.DiscordWebhookUrl ||
							stored.NotifyEmails != update.NotifyEmails {
							t.Fatal(
								"destination binding changed settings incorrectly",
							)
						}
					})
				}
			})
		}
	})
}

func TestGotifyDestinationBindingNullValues(t *testing.T) {
	// SQL NULL and an empty destination both disable the channel.
	for _, current := range []sql.NullString{{}, {String: "", Valid: true}} {
		for _, destination := range []sql.NullString{
			{}, {String: "", Valid: true}, {String: "ignored", Valid: false},
		} {
			if gotifyTokenClearFlag(
				0,
				current,
				destination,
				sql.NullString{},
			) != 0 {
				t.Fatal("equivalent disabled destination cleared a token")
			}
		}
	}
	if gotifyTokenClearFlag(0, nullable("https://a.example"),
		nullable("https://b.example"), sql.NullString{String: "ignored"}) != 1 {
		t.Fatal("invalid replacement retained a token at a changed destination")
	}
}

func TestGotifyDestinationBindingConcurrentWrites(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		first, second := openDeploymentCreationEngine(
			t, newDeploymentCreationEngine(t, name),
		)
		for _, scope := range []string{"project", "global"} {
			t.Run(scope, func(t *testing.T) {
				testGotifyConcurrentWrite(t, first, second, scope)
			})
		}
	})
}

func testGotifyConcurrentWrite(
	t *testing.T, first, second *Repository, scope string,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	original := db.UpdateGlobalNotificationsParams{
		GotifyUrl: nullable(
			"https://a.example",
		), GotifyToken: nullable("a"),
	}
	if err := updateGotifySettings(
		ctx,
		first,
		scope,
		original,
	); err != nil {
		t.Fatal(err)
	}
	tx, err := first.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Rollback(t, tx)
	q := first.Queries.WithTx(tx)
	if scope == "project" {
		_, err = q.LockProject(ctx, 1)
	} else {
		_, err = q.LockGlobalNotifications(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		original.GotifyToken = sql.NullString{}
		done <- updateGotifySettings(ctx, second, scope, original)
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("writer escaped held notification lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	// Commit B while the other writer requests A without a token.
	changed := db.UpdateGlobalNotificationsParams{
		GotifyUrl: nullable(
			"https://b.example",
		), GotifyToken: nullable("b"),
	}
	if scope == "project" {
		err = q.UpdateProjectNotifications(
			ctx,
			projectGotifyParams(changed),
		)
	} else {
		_, err = q.UpdateGlobalNotifications(ctx, changed)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	waitForGotifyWrite(t, ctx, done)
	stored := readGotifySettings(t, first, scope)
	if stored.GotifyUrl.String != "https://a.example" ||
		stored.GotifyToken.String != "" {
		t.Fatal(
			"concurrent writer transferred a different destination's token",
		)
	}
}

func waitForGotifyWrite(t *testing.T, ctx context.Context, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("notification writer did not finish")
	}
}

func TestGotifyDestinationBindingFailedWrite(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(
			t, newDeploymentCreationEngine(t, name),
		)
		for _, scope := range []string{"project", "global"} {
			t.Run(scope, func(t *testing.T) {
				initial := db.UpdateGlobalNotificationsParams{
					GotifyUrl: nullable(
						"https://a.example",
					), GotifyToken: nullable("a"),
					NotifyEmails: nullable("saved@example.com"),
				}
				if err := updateGotifySettings(
					t.Context(),
					repo,
					scope,
					initial,
				); err != nil {
					t.Fatal(err)
				}
				rejectGotifyEmailWrite(t, repo, scope, name)
				before := readGotifySettings(t, repo, scope)
				update := db.UpdateGlobalNotificationsParams{
					GotifyUrl:    nullable("https://b.example"),
					NotifyEmails: nullable("reject@example.com"),
				}
				if err := updateGotifySettings(
					t.Context(),
					repo,
					scope,
					update,
				); err == nil {
					t.Fatal("settings update ignored the storage failure")
				}
				if readGotifySettings(t, repo, scope) != before {
					t.Fatal("failed update changed saved settings")
				}
			})
		}
		if err := repo.UpdateProjectNotifications(
			t.Context(),
			db.UpdateProjectNotificationsParams{
				ID: -1,
			},
		); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("missing project error: %v", err)
		}
		if _, err := repo.DB.ExecContext(
			t.Context(),
			"DELETE FROM global_notifications",
		); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.UpdateGlobalNotifications(
			t.Context(),
			db.UpdateGlobalNotificationsParams{},
		); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("missing global settings error: %v", err)
		}
	})
}

func rejectGotifyEmailWrite(
	t *testing.T, repo *Repository, scope, engine string,
) {
	t.Helper()
	table := "projects"
	if scope == "global" {
		table = "global_notifications"
	}
	constraint := fmt.Sprintf(
		`ALTER TABLE %s ADD CONSTRAINT reject_%s
CHECK (notify_emails IS NULL OR notify_emails <> 'reject@example.com')`,
		table, table,
	)
	if engine == "SQLite" {
		constraint = fmt.Sprintf(
			`CREATE TRIGGER reject_%s AFTER UPDATE ON %s
WHEN NEW.notify_emails = 'reject@example.com'
BEGIN SELECT RAISE(ABORT, 'forced failure'); END`,
			table, table,
		)
	}
	if _, err := repo.DB.ExecContext(t.Context(), constraint); err != nil {
		t.Fatal(err)
	}
}

func updateGotifySettings(
	ctx context.Context, repo *Repository, scope string,
	params db.UpdateGlobalNotificationsParams,
) error {
	if scope == "project" {
		return repo.UpdateProjectNotifications(ctx, projectGotifyParams(params))
	}
	_, err := repo.UpdateGlobalNotifications(ctx, params)
	return err
}

func projectGotifyParams(
	p db.UpdateGlobalNotificationsParams,
) db.UpdateProjectNotificationsParams {
	return db.UpdateProjectNotificationsParams{
		ID:                1,
		GotifyUrl:         p.GotifyUrl,
		GotifyToken:       p.GotifyToken,
		ClearGotifyToken:  p.ClearGotifyToken,
		NotifyEmails:      p.NotifyEmails,
		SlackWebhookUrl:   p.SlackWebhookUrl,
		DiscordWebhookUrl: p.DiscordWebhookUrl,
	}
}

func readGotifySettings(
	t *testing.T, repo *Repository, scope string,
) db.UpdateGlobalNotificationsParams {
	t.Helper()
	if scope == "project" {
		p, err := repo.Queries.GetProject(t.Context(), 1)
		if err != nil {
			t.Fatal(err)
		}
		return db.UpdateGlobalNotificationsParams{
			GotifyUrl: p.GotifyUrl, GotifyToken: p.GotifyToken,
			NotifyEmails: p.NotifyEmails, SlackWebhookUrl: p.SlackWebhookUrl,
			DiscordWebhookUrl: p.DiscordWebhookUrl,
		}
	}
	p, err := repo.Queries.GetGlobalNotifications(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return db.UpdateGlobalNotificationsParams{
		GotifyUrl: p.GotifyUrl, GotifyToken: p.GotifyToken,
		NotifyEmails: p.NotifyEmails, SlackWebhookUrl: p.SlackWebhookUrl,
		DiscordWebhookUrl: p.DiscordWebhookUrl,
	}
}
