package cli

import "strings"

func recoveryCommand(action, id string) string {
	return "bearforge operations " + action + " " + repositoryListText(id)
}

func recoveryInstructions(root string, lines ...string) string {
	if root != "" {
		return "Directory: " + repositoryListText(root) + "\n" + strings.Join(lines, "\n")
	}
	return strings.Join(lines, "\n")
}

func (o *options) writePartialRecovery(command, id string) error {
	if command == "exec" {
		return o.confirmationText("%s\n", recoveryInstructions(o.activeRoot,
			"Inspect execution with:", recoveryCommand("show", id), "Execution commands are never replayed."))
	}
	return o.confirmationText("%s\n", recoveryInstructions(o.activeRoot, "Resume with:", recoveryCommand("resume", id)))
}
