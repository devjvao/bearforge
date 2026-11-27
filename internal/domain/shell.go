package domain

import (
	"runtime"
	"strings"
)

// PowerShell treats the four typographic single quotes as string delimiters.
var powerShellSingleQuotes = strings.NewReplacer("'", "''", "‘", "‘‘", "’", "’’", "‚", "‚‚", "‛", "‛‛")

// QuoteShellArgument quotes text for PowerShell on Windows and a POSIX shell
// elsewhere. Callers generating native commands must also consider shell-to-argv
// conversion, especially for embedded double quotes on older PowerShell versions.
func QuoteShellArgument(value string) string {
	if runtime.GOOS == "windows" {
		return "'" + powerShellSingleQuotes.Replace(value) + "'"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
