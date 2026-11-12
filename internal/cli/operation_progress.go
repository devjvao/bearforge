package cli

import (
	"context"

	"bearforge/internal/app"
	"bearforge/internal/tui"
)

func (o *options) operationProgress(ctx context.Context, service *app.Service, work func(context.Context, *app.Service) (app.Result, error)) (app.Result, error) {
	if o.dry || o.ui == "plain" || !o.interactive() {
		return work(ctx, service)
	}
	previous := service.Observe
	defer func() { service.Observe = previous }()
	return tui.Run(ctx, o.in, o.errOut, func(ctx context.Context, observe func(app.Event)) (app.Result, error) {
		service.Observe = observe
		return work(ctx, service)
	}, tui.ProgressOptions{Title: o.progressTitle, Color: o.color})
}
