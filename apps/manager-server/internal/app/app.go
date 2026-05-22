package app

import (
	"context"
	"io/fs"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type Options struct {
	EmbeddedPanel               fs.FS
	ModelPriceSyncURL           *string
	OpenRouterModelPriceSyncURL *string
	ServiceID                   string
	StartedAt                   int64
}

func New(ctx context.Context, cfg config.Config, options Options) (*Context, error) {
	_ = ctx
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	manager := collector.NewManager(cfg, st)
	serviceID := options.ServiceID
	if serviceID == "" {
		serviceID = "cpa-manager-plus"
	}
	startedAt := options.StartedAt
	if startedAt <= 0 {
		startedAt = time.Now().UnixMilli()
	}
	return FromExisting(
		cfg,
		st,
		manager,
		startedAt,
		options.EmbeddedPanel,
		options.ModelPriceSyncURL,
		options.OpenRouterModelPriceSyncURL,
		serviceID,
	), nil
}
