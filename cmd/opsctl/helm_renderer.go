package main

import (
	"errors"
	"flag"
	"io"

	"ops-platform/internal/bundle"
)

func runHelmRenderer(args []string, input io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl helm-render-owned", flag.ContinueOnError)
	flags.SetOutput(stderr)
	release := flags.String("release", "", "planned Helm release")
	component := flags.String("component", "", "planned upstream component")
	image := flags.String("image", "", "verified OCI image reference")
	version := flags.String("version", "", "locked upstream version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected renderer argument")
	}
	output, err := bundle.RenderOwnedChart(input, *release, *component, *image, *version)
	if err != nil {
		return err
	}
	_, err = stdout.Write(output)
	return err
}
