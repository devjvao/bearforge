package domain

import (
	"runtime"
	"strings"
)

// PowerShell treats the four typographic single quotes as string delimiters.
var powerShellSingleQuotes = strings.NewReplacer("'", "''", "‘", "‘‘", "’", "’’", "‚", "‚‚", "‛", "‛‛")

// QuoteShellArgument keeps a displayed argument literal when copied into
// PowerShell on Windows or a POSIX shell elsewhere.
func QuoteShellArgument(value string) string {
	if runtime.GOOS == "windows" {
		return "'" + powerShellSingleQuotes.Replace(value) + "'"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
