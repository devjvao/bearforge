package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"

	"github.com/devjvao/bearforge/internal/domain"
)

func folderParts(root, id, folder, suffix string) []string {
	relative := domain.FolderName(id, folder) + suffix
	return append([]string{root}, strings.Split(relative, "/")...)
}

// RepositoryPath returns the validated absolute path of a stored bare clone.
func (s *Service) RepositoryPath(repo domain.Repository) (string, error) {
	if err := domain.ValidateFolder(repo.ID, repo.Folder); err != nil {
		return "", err
	}
	return s.Store.SafePath(folderParts("repos", repo.ID, repo.Folder, ".git")...)
}

func (s *Service) treePath(workspace, id, folder string) (string, error) {
	if err := domain.ValidateID(workspace); err != nil {
		return "", err
	}
	if err := domain.ValidateFolder(id, folder); err != nil {
		return "", err
	}
	return s.Store.SafePath(folderParts(workspace, id, folder, "")...)
}

// checkFolderEntries rejects file/directory and case-equivalent collisions on
// every OS, including a differently spelled project directory on Linux.
func (s *Service) checkFolderEntries(parts []string) error {
	reader, ok := s.Store.(directoryReader)
	if !ok {
		if len(parts) > 2 {
			return domain.E("unavailable", "project folders require directory inspection")
		}
		return nil
	}
	for i := 1; i < len(parts); i++ {
		entries, err := reader.ReadDirectory(parts[:i]...)
		if err != nil {
			return err
		}
		if i == 2 && gitParent(entries) {
			return domain.E("conflict", "parent %q contains a Git checkout or administrative directory", parts[1])
		}
		for _, entry := range entries {
			if !strings.EqualFold(entry.Name, parts[i]) {
				continue
			}
			if entry.Link || entry.Name != parts[i] || !entry.Directory {
				return domain.E("conflict", "destination component %q conflicts with %q", parts[i], entry.Name)
			}
		}
	}
	return nil
}

// A worktree has a .git entry. A bare repository is itself a Git directory:
// HEAD and its object/ref stores live directly under the proposed parent.
// A linked administrative directory can instead refer to a common directory.
func gitParent(entries []domain.DirectoryEntry) bool {
	var head, objects, refs, common bool
	for _, entry := range entries {
		switch strings.ToLower(entry.Name) {
		case ".git":
			return true
		case "head":
			head = true
		case "objects":
			objects = true
		case "refs", "reftable":
			refs = true
		case "commondir":
			common = true
		}
	}
	return head && (common || objects && refs)
}

func folderConflict(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func checkRepositoryFolder(c domain.Catalog, repo domain.Repository) error {
	dest := domain.FolderName(repo.ID, repo.Folder) + ".git"
	check := func(other domain.Repository) error {
		if other.ID != repo.ID && folderConflict(dest, domain.FolderName(other.ID, other.Folder)+".git") {
			return domain.E("conflict", "clone folder %s is reserved by %s", dest, other.ID)
		}
		return nil
	}
	for _, other := range c.Repositories {
		if err := check(other); err != nil {
			return err
		}
	}
	for _, retired := range c.Retired {
		if err := check(retired.Repository); err != nil {
			return err
		}
	}
	return nil
}

// Each exclusively created parent gets a fresh witness inside it and an
// independent ownership record under this root's private state. Neither the
// marker name nor a marker copied from another folder authorizes cleanup.
const folderMarkerPrefix = ".bearforge-parent-"

func folderOwnershipRecord(parent []string, token string) []string {
	digest := sha256.Sum256([]byte(parent[0] + "\x00" + parent[1] + "\x00" + token))
	return []string{".bearforge", "state", "folder-owners", hex.EncodeToString(digest[:])}
}

func (s *Service) ensureFolderParent(parts []string) error {
	parent := parts[:len(parts)-1]
	if len(parent) == 1 {
		return s.Store.EnsureDir(parent...)
	}
	if err := s.Store.CreateDir(parent...); err != nil {
		if os.IsExist(err) {
			return s.checkFolderEntries(parts)
		}
		return err
	}
	token := strings.ToLower(rand.Text())
	if err := s.Store.CreateDir(parent[0], parent[1], folderMarkerPrefix+token); err != nil {
		return err
	}
	return s.Store.CreateDir(folderOwnershipRecord(parent, token)...)
}

func (s *Service) cleanupFolderParent(parts []string) error {
	if len(parts) != 3 {
		return nil
	}
	parent := parts[:2]
	reader, ok := s.Store.(directoryReader)
	if !ok {
		return nil
	}
	entries, err := reader.ReadDirectory(parent...)
	if err != nil {
		return err
	}
	if len(entries) != 1 || !entries[0].Directory || entries[0].Link {
		return nil
	}
	token, ok := strings.CutPrefix(entries[0].Name, folderMarkerPrefix)
	if !ok || domain.ValidateID(token) != nil {
		return nil
	}
	record := folderOwnershipRecord(parent, token)
	owned, err := s.Store.Exists(record...)
	if err != nil || !owned {
		return err
	}
	marker := []string{parent[0], parent[1], entries[0].Name}
	contents, err := reader.ReadDirectory(marker...)
	if err != nil || len(contents) > 0 {
		return err
	}
	if err := s.Store.RemoveEmpty(marker...); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := s.Store.RemoveEmpty(parent...); err != nil && !os.IsNotExist(err) {
		// A sibling may appear after inspection. Leave its parent in place.
		remaining, readErr := reader.ReadDirectory(parent...)
		if readErr != nil || len(remaining) == 0 {
			return err
		}
	}
	return s.Store.RemoveEmpty(record...)
}

func (req AddRequest) worktreeFolder(id, registeredFolder, mappedFolder string, mapped bool) (string, error) {
	folder := registeredFolder
	if mapped {
		folder = mappedFolder
	}
	if req.Flat || req.Parent != "" {
		parts := strings.Split(domain.FolderName(id, folder), "/")
		folder = parts[len(parts)-1]
		if req.Parent != "" {
			folder = req.Parent + "/" + folder
		}
	}
	if value, ok := req.Folders[id]; ok {
		if value == "" {
			return "", domain.E("invalid", "empty folder override for %s", id)
		}
		folder = value
	}
	if folder == id {
		folder = ""
	}
	return folder, domain.ValidateFolder(id, folder)
}
