package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"

	"github.com/jackc/pgx/v5"
	"ops-platform/internal/bootstrap"
)

func runBootstrapDatabase(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl bootstrap database-logins", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("secrets-file", "", "repository-external private database LOGIN configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *input == "" {
		return errors.New("database-logins requires --secrets-file")
	}
	info, err := os.Stat(*input)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !outsideGitTree(*input) {
		return errors.New("database LOGIN input must be a private repository-external file")
	}
	raw, err := readBoundedFile(*input, 64<<10)
	if err != nil {
		return errors.New("database LOGIN input unavailable")
	}
	var config bootstrap.DatabaseLogins
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || config.Validate() != nil {
		return errors.New("explicit migration/API/Worker LOGIN configuration required")
	}
	dsn := os.Getenv("OPS_BOOTSTRAP_DATABASE_URL")
	if dsn == "" {
		return errors.New("separate OPS_BOOTSTRAP_DATABASE_URL required")
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return errors.New("bootstrap database unavailable")
	}
	defer conn.Close(ctx)
	if err := bootstrap.ProvisionDatabaseLogins(ctx, conn, config); err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"migrationLogin": config.Migration.Name, "apiLogin": config.API.Name, "workerLogin": config.Worker.Name, "duties": "separate authenticated LOGIN, NOINHERIT/NOBYPASSRLS; only the named duty granted"})
}
