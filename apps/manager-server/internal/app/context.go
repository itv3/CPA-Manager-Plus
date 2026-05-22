package app

import (
	"io/fs"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	apikeyaliassvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/apikeyalias"
	collectorsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/collector"
	dashboardsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/dashboard"
	managerconfigsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	modelpricesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/modelprice"
	monitoringsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/monitoring"
	panelsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/panel"
	proxysvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proxy"
	setupsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/setup"
	usagesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/usage"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type Context struct {
	Config    config.Config
	Store     *store.Store
	Collector *collector.Manager

	StartedAt int64
	ServiceID string

	SetupService         *setupsvc.Service
	ManagerConfigService *managerconfigsvc.Service
	CollectorService     *collectorsvc.Service
	UsageService         *usagesvc.Service
	DashboardService     *dashboardsvc.Service
	MonitoringService    *monitoringsvc.Service
	ModelPriceService    *modelpricesvc.Service
	APIKeyAliasService   *apikeyaliassvc.Service
	ProxyService         *proxysvc.Service
	PanelService         *panelsvc.Service
}

func FromExisting(
	cfg config.Config,
	st *store.Store,
	collectorManager *collector.Manager,
	startedAt int64,
	embeddedPanel fs.FS,
	modelPriceSyncURL *string,
	openRouterModelPriceSyncURL *string,
	serviceID string,
) *Context {
	collectorService := collectorsvc.New(collectorManager)
	managerConfigService := managerconfigsvc.New(cfg, st, collectorService)
	return &Context{
		Config:               cfg,
		Store:                st,
		Collector:            collectorManager,
		StartedAt:            startedAt,
		ServiceID:            serviceID,
		SetupService:         setupsvc.New(cfg, st, collectorService, managerConfigService, startedAt, serviceID),
		ManagerConfigService: managerConfigService,
		CollectorService:     collectorService,
		UsageService:         usagesvc.New(st),
		DashboardService:     dashboardsvc.New(st),
		MonitoringService:    monitoringsvc.New(st),
		ModelPriceService:    modelpricesvc.NewMultiSource(st, modelPriceSyncURL, openRouterModelPriceSyncURL, managerConfigService),
		APIKeyAliasService:   apikeyaliassvc.New(st),
		ProxyService:         proxysvc.New(managerConfigService),
		PanelService:         panelsvc.New(cfg.PanelPath, embeddedPanel),
	}
}
