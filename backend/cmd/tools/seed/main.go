// Command seed 规模种子工具（T088，SC-006 压测数据源）。
//
// 生成 10 节点 / 200 域名 / 100 服务 / 200 Target / 1000 路由 / 100 中间件，
// 供 k6 列表 p95≤3s 性能压测与生成耗时断言使用。
//
// 幂等性：以 --prefix 命名空间隔离（默认 scale）；同前缀重跑会因唯一约束报错——
// 视为全新库注入，重跑前请清空业务数据或换前缀。
//
// 用法：
//
//	GC_DATABASE_URL=... GC_MASTER_KEY=... go run ./cmd/tools/seed [--prefix=scale]
//
// 直接经 GORM Create 落库（镜像 tests/testenv Seed* 工厂的最小合法实体），
// 不经 application 服务/审计，属离线数据注入。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gateway-center/backend/internal/domain"
	"gateway-center/backend/internal/infrastructure/config"
	"gateway-center/backend/internal/infrastructure/logging"
	"gateway-center/backend/internal/infrastructure/pgstore"
)

func main() {
	prefix := flag.String("prefix", "scale", "资源名前缀（命名空间隔离）")
	nodesN := flag.Int("nodes", 10, "节点数")
	domainsPer := flag.Int("domains-per-node", 20, "每节点域名数")
	servicesPer := flag.Int("services-per-node", 10, "每节点服务数")
	targetsPer := flag.Int("targets-per-service", 2, "每服务 Target 数")
	routesPer := flag.Int("routes-per-node", 100, "每节点路由数")
	mwsPer := flag.Int("mws-per-node", 10, "每节点中间件数")
	flag.Parse()

	slog.SetDefault(logging.New(slog.LevelInfo))
	cfg, err := config.Load()
	if err != nil {
		fatal("加载配置: %v", err)
	}
	store, err := pgstore.Open(cfg.DatabaseURL)
	if err != nil {
		fatal("连接数据库: %v", err)
	}
	defer func() {
		if sqlDB, err := store.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rootTmp, err := os.MkdirTemp("", "gc-seed-*")
	if err != nil {
		fatal("创建临时根: %v", err)
	}
	defer os.RemoveAll(rootTmp)

	var tot counters
	start := time.Now()

	for ni := 0; ni < *nodesN; ni++ {
		nodeName := fmt.Sprintf("%s-node-%02d", *prefix, ni)
		deployRoot := filepath.Join(rootTmp, nodeName)
		if err := os.MkdirAll(deployRoot, 0o755); err != nil {
			fatal("创建节点目录: %v", err)
		}
		n := &domain.GatewayNode{
			Name: nodeName, BaseURL: "http://localhost:" + strconv.Itoa(9000+ni),
			DeployRoot: deployRoot, EnvType: envType(ni), Enabled: true,
		}
		if err := store.DB.WithContext(ctx).Create(n).Error; err != nil {
			fatal("创建节点 %s: %v", nodeName, err)
		}
		tot.nodes++

		// 域名
		domIDs := make([]string, 0, *domainsPer)
		for di := 0; di < *domainsPer; di++ {
			dname := fmt.Sprintf("%s-%02d-%03d.example.com", *prefix, ni, di)
			d := &domain.Domain{NodeID: n.ID, Name: dname, HTTPSPolicy: domain.PolicyOff, Enabled: true}
			if err := store.DB.WithContext(ctx).Create(d).Error; err != nil {
				fatal("创建域名 %s: %v", dname, err)
			}
			domIDs = append(domIDs, d.ID)
			tot.domains++
		}

		// 服务 + Target
		svcIDs := make([]string, 0, *servicesPer)
		for si := 0; si < *servicesPer; si++ {
			sname := fmt.Sprintf("%s-svc-%02d-%02d", *prefix, ni, si)
			s := &domain.Service{NodeID: n.ID, Name: sname, ExpectedCodes: "2xx-3xx",
				IntervalSec: 30, TimeoutSec: 2, Enabled: true}
			if err := store.DB.WithContext(ctx).Create(s).Error; err != nil {
				fatal("创建服务 %s: %v", sname, err)
			}
			svcIDs = append(svcIDs, s.ID)
			tot.services++
			for ti := 0; ti < *targetsPer; ti++ {
				t := &domain.Target{ServiceID: s.ID,
					URL: fmt.Sprintf("http://10.%d.%d.%d:8080", ni, si, ti), Weight: 1, Enabled: true}
				if err := store.DB.WithContext(ctx).Create(t).Error; err != nil {
					fatal("创建 Target: %v", err)
				}
				tot.targets++
			}
		}

		// 路由（轮询引用本节点的域名/服务）
		for ri := 0; ri < *routesPer; ri++ {
			did := domIDs[ri%len(domIDs)]
			sid := svcIDs[ri%len(svcIDs)]
			rname := fmt.Sprintf("%s-route-%02d-%03d", *prefix, ni, ri)
			r := &domain.Route{NodeID: n.ID, Name: rname, Mode: "simple",
				DomainID: &did, ServiceID: sid, Path: "/" + *prefix + "/" + strconv.Itoa(ri),
				MatchType: "prefix", Priority: 100, Status: "enabled", HTTPS: false}
			if err := store.DB.WithContext(ctx).Create(r).Error; err != nil {
				fatal("创建路由 %s: %v", rname, err)
			}
			tot.routes++
		}

		// 中间件（security_headers，参数合法可被生成器消费）
		for mi := 0; mi < *mwsPer; mi++ {
			mwname := fmt.Sprintf("%s-mw-%02d-%02d", *prefix, ni, mi)
			mw := &domain.Middleware{NodeID: n.ID, Name: mwname, Type: "security_headers",
				Params: map[string]any{
					"sts_seconds":     31536000,
					"frame_options":   "deny",
					"referrer_policy": "strict-origin-when-cross-origin",
				},
				Enabled: true}
			if err := store.DB.WithContext(ctx).Create(mw).Error; err != nil {
				fatal("创建中间件 %s: %v", mwname, err)
			}
			tot.mws++
		}
	}

	slog.Info("规模种子注入完成",
		"prefix", *prefix, "elapsed", time.Since(start).Round(time.Millisecond),
		"nodes", tot.nodes, "domains", tot.domains, "services", tot.services,
		"targets", tot.targets, "routes", tot.routes, "middlewares", tot.mws)
}

// counters 汇总注入计数（日志输出）。
type counters struct {
	nodes, domains, services, targets, routes, mws int
}

// envType 轮转四种环境类型，覆盖 env_type 维度。
func envType(i int) domain.EnvType {
	switch i % 4 {
	case 0:
		return domain.EnvDevelopment
	case 1:
		return domain.EnvTest
	case 2:
		return domain.EnvStaging
	default:
		return domain.EnvProduction
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "seed: "+format+"\n", args...)
	os.Exit(1)
}
