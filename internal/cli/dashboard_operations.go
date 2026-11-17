package cli

import (
	"context"

	"github.com/devjvao/bearforge/internal/app"
)

func (s dashboardService) InspectOperation(ctx context.Context, id string) (*app.Operation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.Journal.Load(id)
}

func (s dashboardService) ResumeOperation(ctx context.Context, id string, observe func(app.Event)) (app.Result, error) {
	service := *s.Service
	service.Observe = observe
	return service.Resume(ctx, id)
}

func (s dashboardService) AbandonOperation(ctx context.Context, id string) (app.Result, error) {
	return s.Service.Abandon(ctx, id)
}
