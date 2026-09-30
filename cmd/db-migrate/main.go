package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func main() {
	directory := flag.String("dir", "migrations", "directory containing forward-only SQL migrations")
	target := flag.Int64("to", 0, "migrate through this version; zero migrates to the latest version")
	flag.Parse()

	dsn := os.Getenv("SP03_MIGRATION_DATABASE_URL")
	if dsn == "" {
		fatal(errors.New("SP03_MIGRATION_DATABASE_URL is required"))
	}
	if err := migrate(context.Background(), dsn, *directory, *target); err != nil {
		fatal(err)
	}
}

func migrate(ctx context.Context, dsn, directory string, target int64) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open migration database: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to migration database: %w", err)
	}
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("configure PostgreSQL migrations: %w", err)
	}
	if target > 0 {
		if err := goose.UpToContext(ctx, db, directory, target); err != nil {
			return fmt.Errorf("migrate through version %d: %w", target, err)
		}
		return nil
	}
	if err := goose.UpContext(ctx, db, directory); err != nil {
		return fmt.Errorf("migrate to latest forward version: %w", err)
	}
	return nil
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
