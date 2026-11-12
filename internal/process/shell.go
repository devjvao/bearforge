package process

import (
	"runtime"

	"bearforge/internal/domain"
)

func ShellArgv(shell, expression string) ([]string, error) {
	if shell == "" {
		shell = "sh"
		if runtime.GOOS == "windows" {
			shell = "cmd"
		}
	}
	switch shell {
	case "sh":
		return []string{"sh", "-c", expression}, nil
	case "pwsh":
		return []string{"pwsh", "-NoProfile", "-Command", expression}, nil
	case "powershell":
		return []string{"powershell", "-NoProfile", "-Command", expression}, nil
	case "cmd":
		return []string{"cmd.exe", "/D", "/S", "/C", expression}, nil
	default:
		return nil, domain.E("invalid", "unsupported shell %q; use sh, pwsh, powershell or cmd", shell)
	}
}
