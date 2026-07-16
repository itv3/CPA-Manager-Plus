package app

import (
	"context"
	"io/fs"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	accountactionsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/accountaction"
	adminauthsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/adminauth"
	apikeyaliassvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/apikeyalias"
	automationsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/automation"
	bootstrapsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/bootstrap"
	codexinspectionsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/codexinspection"
	collectorsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/collector"
	dashboardsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/dashboard"
	managerconfigsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	modelpricesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/modelprice"
	monitoringsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/monitoring"
	panelsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/panel"
	proaccountsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccount"
	proaccountgatewaysvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountgateway"
	proaccountmodelssvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountmodels"
	proaccountoperationsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountoperation"
	proaccountprobesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountprobe"
	proaccounttestsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccounttest"
	proaccountusagesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proaccountusage"
	proxysvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proxy"
	setupsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/setup"
	usagesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/usage"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type AutomationRuntimeService interface {
	Reload(ctx context.Context) error
}

type Context struct {
	Config    config.Config
	Store     *store.Store
	Collector *collector.Manager

	StartedAt int64
	ServiceID string
	Bootstrap bootstrapsvc.Result

	SetupService                   *setupsvc.Service
	AdminAuthService               *adminauthsvc.Service
	ManagerConfigService           *managerconfigsvc.Service
	CollectorService               *collectorsvc.Service
	UsageService                   *usagesvc.Service
	DashboardService               *dashboardsvc.Service
	CodexInspectionService         *codexinspectionsvc.Service
	MonitoringService              *monitoringsvc.Service
	ModelPriceService              *modelpricesvc.Service
	APIKeyAliasService             *apikeyaliassvc.Service
	AccountActionService           *accountactionsvc.Service
	AccountProcessingPolicyService *automationsvc.Service
	ProxyService                   *proxysvc.Service
	PanelService                   *panelsvc.Service
	ProAccountService              *proaccountsvc.Service
	ProAccountGateway              *proaccountgatewaysvc.Client
	ProAccountModelsService        *proaccountmodelssvc.Service
	ProAccountOperationService     *proaccountoperationsvc.Service
	ProAccountProbeService         *proaccountprobesvc.Service
	ProAccountTestService          *proaccounttestsvc.Service
	ProAccountUsageService         *proaccountusagesvc.Service
	AutomationRuntimeService       AutomationRuntimeService
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
	automationRuntimeService ...AutomationRuntimeService,
) *Context {
	var runtimeService AutomationRuntimeService
	if len(automationRuntimeService) > 0 {
		runtimeService = automationRuntimeService[0]
	}
	collectorService := collectorsvc.New(collectorManager)
	managerConfigService := managerconfigsvc.New(cfg, st, collectorService)
	accountProcessingPolicyService := automationsvc.New(cfg, st)
	proAccountGateway := proaccountgatewaysvc.New(nil)
	proAccountService := proaccountsvc.New(st.ProAccounts, managerConfigService, proAccountGateway)
	proAccountOperationService := proaccountoperationsvc.New(st.ProAccountDrafts)
	proAccountModelsService := proaccountmodelssvc.New(proAccountService, st.ProAccounts, managerConfigService, proAccountGateway, proAccountOperationService)
	proAccountProbeService := proaccountprobesvc.New(nil)
	proAccountTestService := proaccounttestsvc.New(proAccountService, st.ProAccounts, managerConfigService, proAccountGateway, proAccountOperationService)
	proAccountUsageService := proaccountusagesvc.New(st.ProAccounts, proAccountService, managerConfigService)
	return &Context{
		Config:                         cfg,
		Store:                          st,
		Collector:                      collectorManager,
		StartedAt:                      startedAt,
		ServiceID:                      serviceID,
		AdminAuthService:               adminauthsvc.New(cfg, st),
		SetupService:                   setupsvc.New(cfg, st, collectorService, managerConfigService, startedAt, serviceID),
		ManagerConfigService:           managerConfigService,
		CollectorService:               collectorService,
		UsageService:                   usagesvc.New(st),
		DashboardService:               dashboardsvc.New(st, cfg.DashboardHourlyRollupEnabled),
		CodexInspectionService:         codexinspectionsvc.New(st, managerConfigService),
		MonitoringService:              monitoringsvc.New(st, cfg.DashboardHourlyRollupEnabled),
		ModelPriceService:              modelpricesvc.NewMultiSource(st, modelPriceSyncURL, openRouterModelPriceSyncURL, managerConfigService),
		APIKeyAliasService:             apikeyaliassvc.New(st),
		AccountActionService:           accountactionsvc.New(st, managerConfigService),
		AccountProcessingPolicyService: accountProcessingPolicyService,
		ProxyService:                   proxysvc.New(managerConfigService, st),
		PanelService:                   panelsvc.New(cfg.PanelPath, embeddedPanel),
		ProAccountService:              proAccountService,
		ProAccountGateway:              proAccountGateway,
		ProAccountModelsService:        proAccountModelsService,
		ProAccountOperationService:     proAccountOperationService,
		ProAccountProbeService:         proAccountProbeService,
		ProAccountTestService:          proAccountTestService,
		ProAccountUsageService:         proAccountUsageService,
		AutomationRuntimeService:       runtimeService,
	}
}
