package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// workspacePlan binds approval to the action, destination, definitions and
// resolved Git state. All fields have JSON representations that cannot fail.
type workspacePlan struct {
	Kind, Root, Workspace, CatalogRevision, WorkspaceRevision string
	DefaultBranch, Outcome                                    string
	Steps                                                     []Step
}

func (p workspacePlan) digest() string {
	data, _ := json.Marshal(p)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
