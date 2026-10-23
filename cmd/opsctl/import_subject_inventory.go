package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/identity"
)

func runImportSubjectInventory(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl identity import-subject-inventory", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configFile := flags.String("input", "", "0600 repository-external JSON containing only databaseUrl for the migration-duty LOGIN")
	inventoryFile := flags.String("inventory", "", "0600 repository-external inventory already reviewed by its operator")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *configFile == "" || *inventoryFile == "" {
		return errors.New("subject inventory import requires --input and --inventory")
	}
	config, err := readPrivateExternalFile(*configFile, 64<<10)
	if err != nil {
		return errors.New("private migration connection unavailable")
	}
	var connection struct {
		DatabaseURL string `json:"databaseUrl"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(config)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&connection) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("private migration connection invalid")
	}
	parsed, err := url.Parse(connection.DatabaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" || parsed.User == nil || parsed.User.Username() == "" {
		return errors.New("dedicated migration PostgreSQL URL required")
	}
	raw, err := readPrivateExternalFile(*inventoryFile, 1<<20)
	if err != nil {
		return errors.New("private reviewed inventory unavailable")
	}
	if _, _, err := identity.ParseSubjectInventory(raw); err != nil {
		return errors.New("reviewed inventory invalid; no database connection was attempted")
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(bounded, connection.DatabaseURL)
	if err != nil {
		return errors.New("migration database unavailable")
	}
	defer pool.Close()
	repository, err := identity.NewRepository(pool)
	if err != nil {
		return errors.New("identity repository unavailable")
	}
	receipt, err := repository.ImportSubjectInventory(bounded, raw)
	if err != nil {
		return errors.New("reviewed inventory import refused; check dedicated migration duty, tenant, history and target identities")
	}
	return json.NewEncoder(stdout).Encode(receipt)
}
