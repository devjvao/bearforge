package domain

type CatalogMigration struct {
	From           int    `json:"from"`
	To             int    `json:"to"`
	SourceRevision string `json:"source_revision"`
	TargetRevision string `json:"target_revision"`
}
