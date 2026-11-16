package cli

import (
	"context"

	"bearforge/internal/app"
)

func addReferenceFormat(ctx context.Context, service *app.Service, result *app.Result, id, path string) error {
	inspector, ok := service.Git.(interface {
		ReferenceFormat(context.Context, string) (string, error)
	})
	if !ok {
		return nil
	}
	format, err := inspector.ReferenceFormat(ctx, path)
	if err != nil {
		return err
	}
	result.Items = append(result.Items, app.Item{Repository: id, Path: path, Outcome: "registered", RefFormat: format})
	return nil
}
