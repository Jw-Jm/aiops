package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"

	"ops-platform/internal/bundle"
	"ops-platform/internal/bundle/drivers"
)

func runBootstrapGraphLeases(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl bootstrap graph-leases", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profilePath := flags.String("profile", "", "immutable resolved installation Profile")
	valuesPath := flags.String("business-values", "", "explicit validated current business configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *profilePath == "" || *valuesPath == "" {
		return errors.New("Graph Lease bootstrap requires --profile and --business-values")
	}
	p, err := readResolvedProfile(*profilePath)
	if err != nil {
		return err
	}
	f, err := os.Open(*valuesPath)
	if err != nil {
		return errors.New("Graph Lease business configuration unavailable")
	}
	b, err := bundle.ReadBusinessValues(f)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	receipt, err := bundle.BootstrapGraphLeases(ctx, p, b, drivers.Run)
	if receipt.NamespaceUID != "" {
		if encodeErr := json.NewEncoder(stdout).Encode(receipt); encodeErr != nil {
			return encodeErr
		}
	}
	return err
}
