package domain

import "encoding/hex"

// ValidCommitID recognizes full SHA-1 and SHA-256 object names.
func ValidCommitID(oid string) bool {
	if len(oid) != 40 && len(oid) != 64 {
		return false
	}
	_, err := hex.DecodeString(oid)
	return err == nil
}
