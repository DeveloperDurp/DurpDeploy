package main

import (
	"log"
	"log/slog"
	"net/http"
	"os"

	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
)

func main() {
	slog.SetDefault(
		slog.New(
			slog.NewJSONHandler(
				os.Stdout,
				&slog.HandlerOptions{Level: slog.LevelInfo},
			),
		),
	)

	dsn := "durpdeploy.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

	db, err := migrate.Run(dsn)
	if err != nil {
		log.Fatalf("migration failed: %v", err)
	}
	defer db.Close()
	slog.Info("database ready")

	repo := repository.New(db)
	broker := runner.NewLogBroker()
	rnr := runner.New(repo, broker)
	r := server.NewRouter(repo, rnr)
	slog.Info("server starting", "addr", ":8080")
	if err := http.ListenAndServe(":8080", r); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
