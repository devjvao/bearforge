package cli

import (
	"runtime"
	"strings"
)

func recoveryRootArgument(root string) string {
	if runtime.GOOS == "windows" {
		return "'" + strings.ReplaceAll(root, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(root, "'", "'\"'\"'") + "'"
}

func (o *options) recoveryCommand(action, id string) string {
	return "bearforge --root " + recoveryRootArgument(o.activeRoot) + " operations " + action + " " + id
}
