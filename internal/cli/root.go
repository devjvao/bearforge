// Package cli adapts command arguments and streams to application use cases.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"bearforge/internal/app"
	"bearforge/internal/config"
	"bearforge/internal/domain"
	"bearforge/internal/gitcli"
	"bearforge/internal/operation"
	"bearforge/internal/tui"
)

type options struct {
	root, output, ui, color                 string
	json, quiet, verbose, noInput, dry, yes bool
	jobs                                    int
	in                                      io.Reader
	out, errOut                             io.Writer
	rendered                                bool
}

func Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	o := &options{in: in, out: out, errOut: errOut}
	root := newRoot(o)
	root.SetArgs(args)
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(errOut)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	if !o.rendered {
		r := app.NewResult("bearforge")
		r.Outcome = "failed"
		r.Problem = app.ProblemFor(err)
		if o.json || o.output == "json" || o.output == "jsonl" {
			_ = json.NewEncoder(out).Encode(r)
		} else {
			fmt.Fprintln(errOut, "Error:", err)
		}
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	var de *domain.Error
	if errors.As(err, &de) {
		switch de.Code {
		case "invalid":
			return 2
		case "conflict", "unsafe_path", "unsafe_removal", "confirmation", "unhealthy":
			return 3
		case "unavailable":
			return 4
		}
		return 1
	}
	if !o.rendered {
		return 2
	}
	return 1
}

func newRoot(o *options) *cobra.Command {
	root := &cobra.Command{Use: "bearforge", Short: "Manage shared Git repositories and workspaces", SilenceUsage: true, SilenceErrors: true}
	f := root.PersistentFlags()
	f.StringVar(&o.root, "root", "", "Storage root (default: ~/code)")
	f.BoolVar(&o.json, "json", false, "Emit one JSON result")
	f.StringVar(&o.output, "output", "human", "Output: human, json, jsonl")
	f.StringVar(&o.ui, "ui", "auto", "Progress: auto, plain, inline")
	f.StringVar(&o.color, "color", "auto", "Color: auto, always, never")
	f.BoolVarP(&o.quiet, "quiet", "q", false, "Suppress progress")
	f.BoolVarP(&o.verbose, "verbose", "v", false, "Show progress details")
	f.BoolVar(&o.noInput, "no-input", false, "Never prompt")
	f.BoolVar(&o.dry, "dry-run", false, "Preview without writing")
	f.BoolVar(&o.yes, "yes", false, "Confirm the displayed operation")
	f.IntVarP(&o.jobs, "jobs", "j", 0, "Concurrent fetches (default 4)")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if o.json {
			o.output = "json"
		}
		if o.output != "human" && o.output != "json" && o.output != "jsonl" {
			return domain.E("invalid", "invalid output mode")
		}
		if o.ui != "auto" && o.ui != "plain" && o.ui != "inline" {
			return domain.E("invalid", "invalid UI mode")
		}
		if o.color != "auto" && o.color != "always" && o.color != "never" {
			return domain.E("invalid", "invalid color mode")
		}
		if o.quiet && o.verbose {
			return domain.E("invalid", "quiet and verbose conflict")
		}
		if o.ui == "inline" && !o.interactive() {
			return domain.E("invalid", "inline mode requires terminal streams and human interactive output")
		}
		return nil
	}
	root.AddCommand(&cobra.Command{Use: "version", Short: "Print version", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { fmt.Fprintln(o.out, "bearforge 0.1.0-dev"); return nil }})
	addCommands(root, o)
	return root
}
func isTerminal(stream any) bool {
	f, ok := stream.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
func (o *options) interactive() bool {
	return o.output == "human" && !o.noInput && !o.quiet && os.Getenv("CI") == "" && os.Getenv("TERM") != "dumb" && isTerminal(o.in) && isTerminal(o.out) && isTerminal(o.errOut)
}
func (o *options) store() (*config.Store, error) {
	root := o.root
	if root == "" {
		root = os.Getenv("BEARFORGE_ROOT")
	}
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		for p := cwd; ; p = filepath.Dir(p) {
			if _, err := os.Stat(filepath.Join(p, ".bearforge", "catalog.yaml")); err == nil {
				root = p
				break
			}
			if filepath.Dir(p) == p {
				break
			}
		}
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(home, "code")
	}
	return config.New(root), nil
}

func (o *options) perform(ctx context.Context, needsGit, progress bool, work func(context.Context, *app.Service) (app.Result, error)) error {
	store, err := o.store()
	if err != nil {
		return err
	}
	var git app.Git
	if needsGit {
		git, err = gitcli.New()
		if err != nil {
			return err
		}
	}
	service := app.New(store, git, operation.New(store))
	var result app.Result
	if progress && !o.dry && o.ui != "plain" && o.interactive() {
		result, err = tui.Run(ctx, o.in, o.errOut, func(ctx context.Context, observe func(app.Event)) (app.Result, error) {
			service.Observe = observe
			return work(ctx, service)
		})
	} else {
		var mu sync.Mutex
		service.Observe = func(e app.Event) {
			mu.Lock()
			defer mu.Unlock()
			if o.output == "jsonl" {
				_ = json.NewEncoder(o.out).Encode(map[string]any{"schema_version": 1, "type": "progress", "event": e})
			} else if o.output == "human" && !o.quiet {
				fmt.Fprintf(o.errOut, "%s: %s\n", e.Repository, e.State)
			}
		}
		result, err = work(ctx, service)
	}
	if err != nil {
		if result.Outcome == "success" {
			result.Outcome = "failed"
		}
		result.Problem = app.ProblemFor(err)
	}
	o.rendered = true
	return errors.Join(err, o.render(result))
}
func (o *options) render(r app.Result) error {
	if o.output == "json" || o.output == "jsonl" {
		return json.NewEncoder(o.out).Encode(r)
	}
	for _, item := range r.Items {
		name := item.Repository
		if name == "" {
			name = item.Workspace
		}
		fmt.Fprintf(o.out, "%s\t%s", name, item.Outcome)
		if item.Branch != "" {
			fmt.Fprintf(o.out, "\t%s", item.Branch)
		}
		if item.Path != "" {
			fmt.Fprintf(o.out, "\t%s", item.Path)
		}
		fmt.Fprintln(o.out)
		if item.Problem != nil {
			fmt.Fprintln(o.errOut, item.Problem.Message)
		}
	}
	if r.Data != nil {
		b, err := json.MarshalIndent(r.Data, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(o.out, string(b))
	}
	if r.Problem != nil {
		fmt.Fprintln(o.errOut, "Error:", r.Problem.Message)
	}
	if r.OperationID != "" && (r.Outcome == "partial") {
		fmt.Fprintln(o.errOut, "Resume with: bearforge operations resume", r.OperationID)
	}
	return nil
}
func (o *options) confirm(ctx context.Context, plan app.Result) error {
	if o.yes {
		return nil
	}
	if o.noInput || o.output != "human" || !isTerminal(o.in) {
		return domain.E("confirmation", "confirmation required; review --dry-run then use --yes")
	}
	for _, item := range plan.Items {
		fmt.Fprintf(o.errOut, "Remove %s (%s)\n", item.Repository, item.Path)
	}
	fmt.Fprint(o.errOut, "Proceed? [y/N] ")
	line, err := readConfirmation(ctx, o.in)
	if err != nil {
		return err
	}
	if strings.TrimSpace(strings.ToLower(line)) != "y" {
		return domain.E("confirmation", "cancelled")
	}
	return nil
}

func readConfirmation(ctx context.Context, in io.Reader) (string, error) {
	type answer struct {
		line string
		err  error
	}
	ch := make(chan answer, 1)
	go func() { line, err := bufio.NewReader(in).ReadString('\n'); ch <- answer{line: line, err: err} }()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case a := <-ch:
		return a.line, a.err
	}
}
