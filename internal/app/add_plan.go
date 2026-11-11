package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func addPlanDigest(root, workspace, catalogRevision, workspaceRevision string, steps []Step) string {
	// Include storage and definition identities as well as resolved Git state so
	// approval cannot move to a different destination or survive definition drift.
	data, _ := json.Marshal(struct {
		Root, Workspace, CatalogRevision, WorkspaceRevision string
		Steps                                               []Step
	}{root, workspace, catalogRevision, workspaceRevision, steps})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
