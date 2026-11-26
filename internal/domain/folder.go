package domain

import "strings"

// FolderName resolves the omitted destination in existing catalogs and journals.
func FolderName(id, folder string) string {
	if folder == "" {
		return id
	}
	return folder
}

// ValidateFolder accepts a portable leaf, optionally under one project directory.
func ValidateFolder(id, folder string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	if folder == "" {
		return nil
	}
	parts := strings.Split(folder, "/")
	if len(parts) > 2 {
		return E("invalid", "folder must be a leaf or parent/leaf")
	}
	for _, part := range parts {
		if err := ValidateID(part); err != nil {
			return err
		}
	}
	if len(parts) == 2 && strings.HasSuffix(parts[0], ".git") {
		return E("invalid", "parent folder cannot end in .git")
	}
	return nil
}
