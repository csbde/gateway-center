// Command gateway-center 是控制平面单二进制（T001）。
// 子命令：serve（API + SPA）、migrate-seed（初始 Super Admin 种子，T010）。
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "serve":
		return runServe()
	case "migrate-seed":
		return runMigrateSeed()
	case "-h", "--help", "help":
		return usage()
	default:
		return fmt.Errorf("未知子命令 %q（可用：serve, migrate-seed）", args[0])
	}
}

func usage() error {
	fmt.Fprint(os.Stderr, `gateway-center —— Traefik 可视化网关控制平面

用法:
  gateway-center serve         启动 API 服务（含 SPA）
  gateway-center migrate-seed  创建初始 Super Admin（幂等）

环境变量: GC_ADDR, GC_DATABASE_URL, GC_MASTER_KEY(64hex), GC_INITIAL_ADMIN_*
`)
	return nil
}
