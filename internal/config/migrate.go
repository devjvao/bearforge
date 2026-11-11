package config

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"

	"go.yaml.in/yaml/v3"

	"bearforge/internal/domain"
)

func validRevision(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (s *Store) CatalogVersion() (int, error) {
	path, err := s.SafePath(".bearforge", "catalog.yaml")
	if err != nil {
		return 0, err
	}
	data, revision, err := read(path)
	if err != nil {
		return 0, err
	}
	if revision == "" {
		return 2, nil
	}
	var dto catalogDTO
	if err := decode(data, &dto); err != nil {
		return 0, err
	}
	if dto.Version != 1 && dto.Version != 2 {
		return 0, domain.E("invalid", "unsupported catalog schema %d", dto.Version)
	}
	return dto.Version, nil
}

func (s *Store) PlanCatalogMigration(target int) (domain.CatalogMigration, error) {
	plan := domain.CatalogMigration{To: target}
	if target != 2 {
		return plan, domain.E("invalid", "only migration to catalog schema 2 is supported")
	}
	_, validatedRevision, err := s.LoadCatalog()
	if err != nil {
		return plan, err
	}
	path, err := s.SafePath(".bearforge", "catalog.yaml")
	if err != nil {
		return plan, err
	}
	data, revision, err := read(path)
	if err != nil {
		return plan, err
	}
	if revision == "" {
		return plan, domain.E("not_found", "catalog is not initialized")
	}
	if revision != validatedRevision {
		return plan, domain.E("conflict", "catalog changed during migration planning")
	}
	var dto catalogDTO
	if err := decode(data, &dto); err != nil {
		return plan, err
	}
	plan.From, plan.SourceRevision = dto.Version, revision
	if plan.From == target {
		plan.TargetRevision = revision
		return plan, nil
	}
	if plan.From != 1 {
		return plan, domain.E("invalid", "unsupported source catalog schema %d", plan.From)
	}
	next, err := migrateCatalogBytes(data)
	if err != nil {
		return plan, err
	}
	plan.TargetRevision = Revision(next)
	return plan, nil
}

func migrateCatalogBytes(data []byte) ([]byte, error) {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	if err := validateNode(&node, 0); err != nil {
		return nil, err
	}
	if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return nil, domain.E("invalid", "catalog must be a YAML mapping")
	}
	mapping := node.Content[0]
	found := false
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "schema_version" {
			mapping.Content[i+1].Value = "2"
			mapping.Content[i+1].Tag = "!!int"
			found = true
		}
	}
	if !found {
		return nil, domain.E("invalid", "catalog schema version is missing")
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&node); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	if out.Len() > maxDocument {
		return nil, domain.E("invalid", "migrated catalog exceeds 2 MiB")
	}
	return out.Bytes(), nil
}

func (s *Store) ApplyCatalogMigration(ctx context.Context, plan domain.CatalogMigration, operationID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if plan.From != 1 || plan.To != 2 || !validRevision(plan.SourceRevision) || !validRevision(plan.TargetRevision) {
		return domain.E("invalid", "invalid catalog migration journal")
	}
	if err := domain.ValidateID(operationID); err != nil {
		return err
	}
	path, err := s.SafePath(".bearforge", "catalog.yaml")
	if err != nil {
		return err
	}
	data, current, err := read(path)
	if err != nil {
		return err
	}
	backupParts := []string{".bearforge", "state", "backups", operationID, "catalog.yaml"}
	backupPath, err := s.SafePath(backupParts...)
	if err != nil {
		return err
	}
	_, backupRevision, err := read(backupPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if current == plan.TargetRevision && backupRevision == plan.SourceRevision {
		_, _, err := s.LoadCatalog()
		return err
	}
	if current != plan.SourceRevision {
		return domain.E("conflict", "catalog changed since migration was planned")
	}
	if backupRevision != "" && backupRevision != plan.SourceRevision {
		return domain.E("conflict", "migration backup conflicts with source bytes")
	}
	next, err := migrateCatalogBytes(data)
	if err != nil {
		return err
	}
	if Revision(next) != plan.TargetRevision {
		return domain.E("conflict", "migration target does not match its journal")
	}
	if backupRevision == "" {
		if err := s.Write(backupParts, data); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, current, err = read(path)
	if err != nil {
		return err
	}
	if current != plan.SourceRevision {
		return domain.E("conflict", "catalog changed before migration replacement")
	}
	return s.Write([]string{".bearforge", "catalog.yaml"}, next)
}
