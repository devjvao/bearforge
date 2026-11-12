package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"bearforge/internal/release"
)

func main() { os.Exit(run()) }
func run() int {
	var options release.Options
	var targets string
	flag.StringVar(&options.Source, "source", ".", "Source checkout")
	flag.StringVar(&options.Output, "out", "dist", "New or empty output directory")
	flag.StringVar(&options.Version, "version", "", "Exact version tag (for example v0.2.0)")
	flag.BoolVar(&options.Snapshot, "snapshot", false, "Build local unsigned snapshots; allow dirty source and other Go patches")
	flag.StringVar(&targets, "targets", "", "Comma-separated os/arch targets; default all six supported targets")
	flag.Parse()
	if targets != "" {
		options.Targets = strings.Split(targets, ",")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	manifest, err := release.Build(ctx, options)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := json.NewEncoder(os.Stdout).Encode(manifest); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
