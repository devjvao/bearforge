package cli

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/devjvao/bearforge/internal/auth"
	"github.com/devjvao/bearforge/internal/domain"
	"github.com/devjvao/bearforge/internal/gitcli"
)

func (o *options) connectionGit(client *gitcli.Client, connection, grantID, remote string) (*gitcli.Client, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return client.WithConnection(gitcli.ConnectionBinding{Connection: connection, GrantID: grantID, URL: remote, Executable: executable})
}

// addGitCredentialCommand implements Git's private stdin/stdout protocol.
// It must never render a normal JSON/result document on that stdout stream.
func addGitCredentialCommand(parent *cobra.Command, o *options) {
	var connection, grantID, expectedURL, registry string
	command := &cobra.Command{Use: "git-credential get|store|erase", Hidden: true, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		o.rendered = true
		if args[0] != "get" {
			return nil
		}
		refuse := func() error { _, err := fmt.Fprint(o.out, "quit=true\n\n"); return err }
		if connection == "" || grantID == "" || expectedURL == "" {
			return refuse()
		}
		values, err := readGitCredentialInput(o.in)
		if err != nil {
			return refuse()
		}
		requested := values["protocol"] + "://" + values["host"]
		if path := values["path"]; path != "" {
			requested += "/" + strings.TrimPrefix(path, "/")
		}
		expected, err := url.Parse(expectedURL)
		actual, parseErr := url.Parse(requested)
		if err != nil || parseErr != nil || expected.Scheme != "https" || actual.Scheme != "https" || !strings.EqualFold(expected.Host, actual.Host) || strings.TrimSuffix(expected.Path, "/") != strings.TrimSuffix(actual.Path, "/") || actual.User != nil {
			return refuse()
		}
		manager, err := o.authentication()
		if registry != "" && o.authManager == manager {
			manager = auth.NewManager(auth.Options{RegistryPath: registry})
		}
		if err != nil {
			return refuse()
		}
		credential, err := manager.Resolve(cmd.Context(), domain.CredentialRequest{Connection: connection, GrantID: grantID, GitURL: expectedURL, AllowRefresh: true})
		if err != nil || credential == nil {
			return refuse()
		}
		username, password, err := credential.GitCredential(actual)
		if err != nil || strings.ContainsAny(username+password, "\x00\r\n") {
			return refuse()
		}
		_, err = fmt.Fprintf(o.out, "username=%s\npassword=%s\n\n", username, password)
		return err
	}}
	command.Flags().StringVar(&connection, "connection", "", "Selected saved connection")
	command.Flags().StringVar(&grantID, "grant-id", "", "Reviewed grant identity")
	command.Flags().StringVar(&expectedURL, "url", "", "Reviewed remote URL")
	command.Flags().StringVar(&registry, "registry", "", "Authentication registry path")
	parent.AddCommand(command)
}

func readGitCredentialInput(input io.Reader) (map[string]string, error) {
	data, err := io.ReadAll(io.LimitReader(input, 64<<10+1))
	if err != nil || len(data) > 64<<10 {
		return nil, domain.E("invalid", "invalid credential request")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.ContainsAny(key+value, "\x00\r") {
			return nil, domain.E("invalid", "invalid credential request")
		}
		// Git sends repeated capability[], wwwauth[], and state[] attributes
		// during HTTP authentication. They are optional negotiation metadata;
		// this helper resolves only the scalar protocol/host/path binding.
		if strings.HasSuffix(key, "[]") {
			continue
		}
		if _, duplicate := values[key]; duplicate {
			return nil, domain.E("invalid", "ambiguous credential request")
		}
		values[key] = value
	}
	return values, nil
}
