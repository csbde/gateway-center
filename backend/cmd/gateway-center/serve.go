// serve.go：API 服务装配与生命周期（T001/T026，宪章 XII 单向依赖装配点）。
// 组合顺序：config → store → repos → cipher/tokens → auditrec → 各 application service →
// pipeline（Version/Deploy）→ Handler → router → scheduler（探测/健康）→ http.Server（优雅关停）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gateway-center/backend/internal/api"
	"gateway-center/backend/internal/api/handlers"
	"gateway-center/backend/internal/api/middleware"
	"gateway-center/backend/internal/application/auditrec"
	"gateway-center/backend/internal/application/authsvc"
	"gateway-center/backend/internal/application/certsvc"
	"gateway-center/backend/internal/application/domainsvc"
	"gateway-center/backend/internal/application/healthsvc"
	"gateway-center/backend/internal/application/nodesvc"
	"gateway-center/backend/internal/application/pipeline"
	"gateway-center/backend/internal/application/probesvc"
	"gateway-center/backend/internal/application/routesvc"
	"gateway-center/backend/internal/application/scheduler"
	"gateway-center/backend/internal/application/servicesvc"
	"gateway-center/backend/internal/application/settingsvc"
	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/config"
	"gateway-center/backend/internal/infrastructure/cryptox"
	"gateway-center/backend/internal/infrastructure/deployer"
	"gateway-center/backend/internal/infrastructure/logging"
	"gateway-center/backend/internal/infrastructure/pgstore"
	"gateway-center/backend/internal/infrastructure/tokensx"
	"gateway-center/backend/internal/infrastructure/traefikapi"
)

func runServe(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "", "监听地址（覆盖 GC_ADDR）")
	_ = fs.Parse(args)
	if *addr != "" {
		cfg.Addr = *addr
	}
	logger := logging.New(slog.LevelInfo)
	slog.SetDefault(logger)

	masterKey, err := cfg.MasterKey()
	if err != nil {
		return err
	}
	jwtKey, err := cfg.JWTSigningKey()
	if err != nil {
		return err
	}
	cipher, err := cryptox.NewCipher(masterKey)
	if err != nil {
		return err
	}
	store, err := pgstore.Open(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("连接数据库: %w", err)
	}
	defer closeDB(store)

	// ---- repos ----
	nodes := pgstore.NewNodeRepo(store.DB)
	doms := pgstore.NewDomainRepo(store.DB)
	svcs := pgstore.NewServiceRepo(store.DB)
	targets := pgstore.NewTargetRepo(store.DB)
	routes := pgstore.NewRouteRepo(store.DB)
	mws := pgstore.NewMiddlewareRepo(store.DB)
	vers := pgstore.NewVersionRepo(store.DB)
	deploys := pgstore.NewDeploymentRepo(store.DB)
	certs := pgstore.NewCertRepo(store.DB)
	users := pgstore.NewUserRepo(store.DB)
	toks := pgstore.NewTokenRepo(store.DB)
	setRepo := pgstore.NewSettingsRepo(store.DB)
	rec := auditrec.New(pgstore.NewAuditRepo(store.DB))

	// ---- 启动恢复（R16/NFR-REL-01：进程重启时中断中的部署 → failed）----
	bootCtx, bootCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if n, err := deploys.MarkInterruptedFailed(bootCtx); err != nil {
		bootCancel()
		return fmt.Errorf("启动恢复中断部署: %w", err)
	} else if n > 0 {
		slog.Warn("启动恢复：中断部署已标记 failed", "count", n)
	}
	bootCancel()

	// ---- application services ----
	settingsSvc := settingsvc.New(setRepo, rec)
	tm := tokensx.NewManager(jwtKey, cfg.AccessTokenTTL)
	authSvc := authsvc.NewService(users, toks, tm, rec, cfg.RefreshTokenTTL)
	nodeSvc := nodesvc.New(nodes, rec, cipher)

	traefikFactory := pipeline.TraefikFactory(func(n *domain.GatewayNode) (*traefikapi.Client, error) {
		user, pass, err := nodeSvc.APIAuth(context.Background(), n)
		if err != nil {
			return nil, fmt.Errorf("节点 %s API 凭证: %w", n.Name, err)
		}
		return traefikapi.NewClient(n.BaseURL, user, pass), nil
	})

	versionSvc := pipeline.NewVersionService(
		pipeline.NewGraphLoader(nodes, doms, svcs, targets, routes, mws),
		vers, deploys, certs, cipher, rec)
	// gate=nil：非 production 放行；production 直发在 Deploy 内 403（US6 T077 接入审批 Gate）。
	deploySvc := pipeline.NewDeployService(vers, deploys, nodes, certs, cipher,
		deployer.NewFileDeployer(), traefikFactory, rec, nil)

	h := &handlers.Handler{
		Auth:     authSvc,
		Nodes:    nodeSvc,
		Domains:  domainsvc.New(doms, nodes, rec),
		Certs:    certsvc.New(certs, doms, cipher, rec),
		Services: servicesvc.New(svcs, targets, nodes, rec),
		Routes:   routesvc.New(routes, doms, svcs, mws, nodes, rec),
		Versions: versionSvc,
		Deploys:  deploySvc,
		Settings: settingsSvc,
		Vers:     vers,
		Deps:     deploys,
	}

	// ---- scheduler：节点探测 + Target 健康 ----
	probe := probesvc.New(nodes, probesvc.SettingsAdapter{
		Snap: func() *domain.PlatformSettings { return settingsSvc.Current(context.Background()) },
	}, probesvc.ClientFactory(traefikFactory))
	health := healthsvc.New(targets, svcs)

	sched := scheduler.New(32)
	probeInterval := time.Duration(settingsSvc.Current(context.Background()).ProbeIntervalSec) * time.Second
	if probeInterval <= 0 {
		probeInterval = cfg.ProbeInterval
	}
	sched.Every("node-probe", probeInterval, probe.RunOnce)
	sched.Every("target-health", 15*time.Second, health.RunOnce)
	sched.Start()

	// ---- HTTP ----
	router := api.NewRouter(h, middleware.Authenticate(tm, users))
	var handler http.Handler = router
	if cfg.SPAStaticDir != "" {
		handler = withSPA(router, cfg.SPAStaticDir)
	}
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("gateway-center 启动", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP 服务退出", "err", err.Error())
			os.Exit(1)
		}
	}()

	// ---- 优雅关停：先停调度 → 关 HTTP（拒新连接）→ 等在途后台部署 ----
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	slog.Info("收到退出信号，开始优雅关停")
	sched.Stop(context.Background())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("HTTP 关停超时", "err", err.Error())
	}
	h.WaitBackground() // 等待异步部署落盘/校验完成
	slog.Info("已关停")
	return nil
}

// withSPA 未匹配 /api 与 /healthz 的 GET 交给静态目录（T026 SPA embed 占位；
// index.html 回退支持前端路由）。
func withSPA(api http.Handler, dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && !isAPIPath(r.URL.Path) {
			path := dir + r.URL.Path
			if _, err := os.Stat(path); err != nil {
				http.ServeFile(w, r, dir+"/index.html")
				return
			}
			fs.ServeHTTP(w, r)
			return
		}
		api.ServeHTTP(w, r)
	})
}

func isAPIPath(p string) bool {
	return len(p) >= 4 && (p[:4] == "/api" || p == "/healthz")
}

func closeDB(s *pgstore.Store) {
	if sqlDB, err := s.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
