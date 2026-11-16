package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"bearforge/internal/app"
	"bearforge/internal/auth"
	"bearforge/internal/domain"
)

func (o *options) authentication() (*auth.Manager, error) {
	if o.authManager != nil {
		return o.authManager, nil
	}
	path, err := auth.DefaultRegistryPath()
	if err != nil {
		return nil, err
	}
	o.authManager = auth.NewManager(auth.Options{RegistryPath: path, Browser: authBrowser{}})
	return o.authManager, nil
}

func addAuthCommands(root *cobra.Command, o *options) {
	command := &cobra.Command{Use: "auth", Short: "Authenticate GitHub and Bitbucket connections"}
	root.AddCommand(command)
	addGitCredentialCommand(command, o)
	var provider, host, api, name, method, username, tokenEnv string
	var tokenStdin bool
	login := &cobra.Command{
		Use: "login", Short: "Sign in to GitHub or Bitbucket using a browser or guided token setup",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			manager, err := o.authentication()
			if err != nil {
				return err
			}
			if tokenStdin && tokenEnv != "" {
				return domain.E("invalid", "choose only one of --token-stdin or --token-env")
			}
			if method != "auto" && method != "browser" && method != "token" {
				return domain.E("invalid", "method must be auto, browser, or token")
			}
			if method == "browser" && (o.output != "human" || o.quiet) {
				return domain.E("invalid", "browser login requires human output without --quiet so authorization instructions are visible; use --token-stdin or --token-env for machine output")
			}
			if provider == "" {
				if !o.interactive() {
					return domain.E("invalid", "choose --provider github or bitbucket")
				}
				provider, err = o.authPrompt(cmd.Context(), "Provider (github or bitbucket)", "github")
				if err != nil {
					return err
				}
			}
			if host == "" && o.interactive() {
				defaultHost := "https://github.com"
				if strings.HasPrefix(provider, "bitbucket") {
					defaultHost = "https://bitbucket.org"
				}
				host, err = o.authPrompt(cmd.Context(), "Server URL", defaultHost)
				if err != nil {
					return err
				}
			}
			request, err := auth.NormalizeLoginRequest(auth.LoginRequest{
				Name: name, Provider: provider, Host: host, APIURL: api, Username: username,
			})
			if err != nil {
				return domain.E("invalid", "%v", err)
			}
			if method != "auto" {
				request.Method = method
			}
			request.NoBrowser = tokenStdin || tokenEnv != "" || o.noInput
			if tokenStdin || tokenEnv != "" {
				request.Method = auth.MethodToken
			}
			if request.Username == "" && strings.HasPrefix(request.Provider, "bitbucket") && o.interactive() {
				label := "Git username"
				if request.Provider == "bitbucket-cloud" {
					label = "Atlassian account email (for API tokens)"
				}
				request.Username, err = o.authPrompt(cmd.Context(), label, "")
				if err != nil {
					return err
				}
			}
			if o.dry {
				r := app.NewResult("auth.login")
				r.Outcome, r.Data = "planned", map[string]string{"connection": request.Name, "provider": request.Provider, "host": request.Host}
				return o.finishAuthentication(r, nil)
			}
			if !o.interactive() && !tokenStdin && tokenEnv == "" && request.Method != auth.MethodBrowser {
				return domain.E("invalid", "noninteractive login requires --token-stdin, --token-env NAME, or an explicitly configured --method browser")
			}
			hooks := auth.LoginHooks{
				ReadSecret: func(ctx context.Context, label string) (string, error) {
					if tokenEnv != "" {
						value := os.Getenv(tokenEnv)
						if value == "" {
							return "", domain.E("unauthenticated", "configured token environment variable is unset")
						}
						return value, nil
					}
					if tokenStdin {
						return readAuthInput(ctx, o.in, func(input io.Reader) (string, error) { return readAuthLine(input, 64<<10) })
					}
					return o.authSecret(ctx, label)
				},
				Notify: func(message string) {
					if o.output == "human" && !o.quiet {
						_, _ = fmt.Fprintln(o.errOut, repositoryListText(message))
					}
				},
				Confirm: func(ctx context.Context, connection auth.Connection) error {
					_, previousErr := manager.Connection(ctx, connection.Name)
					if previousErr != nil && !errors.Is(previousErr, auth.ErrDisconnected) {
						return previousErr
					}
					if previousErr == nil && !o.yes {
						if !o.interactive() {
							return domain.E("confirmation", "connection already exists; use --yes to replace its grant")
						}
						answer, promptErr := o.authPrompt(ctx, "Replace connection "+connection.Name+"? (y/N)", "n")
						if promptErr != nil {
							return promptErr
						}
						if !strings.EqualFold(answer, "y") {
							return domain.E("confirmation", "connection replacement declined")
						}
					}
					return nil
				},
			}
			if o.interactive() {
				hooks.ChooseMethod = func(ctx context.Context, methods []string) (string, error) {
					if len(methods) == 1 {
						return methods[0], nil
					}
					return o.authPrompt(ctx, "Login method (browser or token)", methods[0])
				}
			}
			connection, loginErr := manager.Login(cmd.Context(), request, hooks)
			r := app.NewResult("auth.login")
			if loginErr == nil {
				r.Data = connection
			}
			return o.finishAuthentication(r, loginErr)
		},
	}
	login.Flags().StringVar(&provider, "provider", "", "Provider: github or bitbucket")
	login.Flags().StringVar(&host, "host", "", "Server URL (defaults to public provider)")
	login.Flags().StringVar(&api, "api-url", "", "Explicit HTTPS API base for enterprise installations")
	login.Flags().StringVar(&name, "connection", "", "Name of the saved connection")
	login.Flags().StringVar(&method, "method", "auto", "Login method: auto, browser, or token")
	login.Flags().StringVar(&username, "username", "", "Git username or Bitbucket Cloud account email")
	login.Flags().StringVar(&tokenEnv, "token-env", "", "Read token from this environment variable")
	login.Flags().BoolVar(&tokenStdin, "token-stdin", false, "Read a token from stdin without echoing it")
	command.AddCommand(login)
	var connection, repository string
	var local bool
	status := &cobra.Command{Use: "status", Short: "Check saved connections and repository API/Git read access", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return o.authenticationStatus(cmd.Context(), connection, repository, local || o.dry)
	}}
	status.Flags().StringVar(&connection, "connection", "", "Check only this saved connection")
	status.Flags().StringVar(&repository, "repo", "", "Check one registered repository")
	status.Flags().BoolVar(&local, "local", false, "Read configuration only, without network or secret-store access")
	command.AddCommand(status)
	var logoutConnection string
	logout := &cobra.Command{Use: "logout", Short: "Remove one local saved grant without revoking provider access", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		manager, err := o.authentication()
		if err != nil {
			return err
		}
		if logoutConnection == "" {
			return domain.E("invalid", "choose --connection NAME to log out")
		}
		r := app.NewResult("auth.logout")
		if o.dry {
			r.Outcome = "planned"
			r.Data = map[string]string{"connection": logoutConnection}
			return o.finishAuthentication(r, nil)
		}
		result, err := manager.Logout(cmd.Context(), logoutConnection)
		r.Data = result
		return o.finishAuthentication(r, err)
	}}
	logout.Flags().StringVar(&logoutConnection, "connection", "", "Local connection to remove")
	command.AddCommand(logout)
}

func (o *options) finishAuthentication(result app.Result, err error) error {
	if err != nil {
		result.Outcome, result.Problem = "failed", app.ProblemFor(err)
	}
	o.rendered = true
	return errors.Join(err, o.render(result))
}

func (o *options) authPrompt(ctx context.Context, label, fallback string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if fallback != "" {
		label += " [" + fallback + "]"
	}
	label = repositoryListText(label)
	if _, err := fmt.Fprint(o.errOut, label+": "); err != nil {
		return "", err
	}
	value, err := readAuthInput(ctx, o.in, func(input io.Reader) (string, error) { return readAuthLine(input, 4096) })
	if err != nil {
		return "", err
	}
	if value == "" {
		value = fallback
	}
	return value, nil
}

// readAuthLine reads exactly one line without buffering the next prompt's input.
func readAuthLine(input io.Reader, limit int) (string, error) {
	var value []byte
	var buffer [1]byte
	for len(value) <= limit {
		_, err := io.ReadFull(input, buffer[:])
		if err != nil {
			if errors.Is(err, io.EOF) && len(value) > 0 {
				return strings.TrimSuffix(string(value), "\r"), nil
			}
			return "", err
		}
		if buffer[0] == '\n' {
			return strings.TrimSuffix(string(value), "\r"), nil
		}
		value = append(value, buffer[0])
	}
	return "", domain.E("invalid", "authentication input exceeds its limit")
}

func (o *options) authSecret(ctx context.Context, label string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	input, ok := o.in.(*os.File)
	if !ok || !term.IsTerminal(int(input.Fd())) {
		return "", domain.E("invalid", "hidden token input requires a terminal; use --token-stdin or --token-env")
	}
	if _, err := fmt.Fprint(o.errOut, label+": "); err != nil {
		return "", err
	}
	secret, err := readAuthHidden(ctx, input, func() (func() error, error) {
		state, err := term.MakeRaw(int(input.Fd()))
		if err != nil {
			return nil, err
		}
		return func() error { return term.Restore(int(input.Fd()), state) }, nil
	})
	_, _ = fmt.Fprintln(o.errOut)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		return "", domain.E("invalid", "could not read hidden token input")
	}
	return secret, nil
}
