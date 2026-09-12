// Package traefikapi 是 Traefik 只读 Dashboard/API 采集客户端（T019，research R5）。
// 宪法 III/XI：仅 GET，用于观测实际态（版本、加载的 router/service/middleware 集合）；
// 平台永不通过此通道写入任何配置。
package traefikapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrUnreachable = errors.New("Traefik API 不可达")

type Client struct {
	baseURL    string
	user, pass string
	http       *http.Client
}

func NewClient(baseURL, user, pass string) *Client {
	return &Client{baseURL: baseURL, user: user, pass: pass,
		http: &http.Client{Timeout: 5 * time.Second}}
}

type Overview struct {
	Version string `json:"version"`
	Total   struct {
		HTTP struct {
			Routers     map[string]int `json:"routers"`
			Services    map[string]int `json:"services"`
			Middlewares map[string]int `json:"middlewares"`
		} `json:"http"`
	} `json:"total"`
}

// ResourceRef 是实际加载资源的最小投影（名称 + 状态位）。
type ResourceRef struct {
	Name   string `json:"name"`
	Router struct {
		Rule        string   `json:"rule"`
		Service     string   `json:"service"`
		EntryPoints []string `json:"entryPoints"`
		Status      string   `json:"status"`
	} `json:"router,omitempty"`
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: HTTP %d", ErrUnreachable, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func (c *Client) Overview(ctx context.Context) (*Overview, error) {
	var o Overview
	if err := c.get(ctx, "/api/overview", &o); err != nil {
		return nil, err
	}
	return &o, nil
}

// HTTPRouters 返回实际加载的 http router 名称→规则摘要。
func (c *Client) HTTPRouters(ctx context.Context) (map[string]map[string]any, error) {
	var raw struct {
		Items []map[string]any `json:"items"`
	}
	if err := c.get(ctx, "/api/http/routers", &raw); err != nil {
		return nil, err
	}
	m := map[string]map[string]any{}
	for _, it := range raw.Items {
		name, _ := it["name"].(string)
		if base, ok := platformName(name); ok {
			m[base] = it
		}
	}
	return m, nil
}

// platformName 归一化实际态资源名并过滤平台治理域之外的资源：
// file provider 将路由器以 "名称@file" 上报（快照按裸名期望，须剥后缀比对）；
// "@internal"（api/dashboard 等 Traefik 内建路由）不属于平台产物，直接剔除（宪章 XI 分域）。
func platformName(name string) (string, bool) {
	base, _, has := strings.Cut(name, "@")
	if base == "" {
		return "", false
	}
	if has && strings.EqualFold(name[len(base)+1:], "internal") {
		return "", false
	}
	return base, true
}

// HTTPServices / HTTPMiddlewares 实际加载集合。
func (c *Client) HTTPServices(ctx context.Context) (map[string]bool, error) {
	return c.names(ctx, "/api/http/services")
}

func (c *Client) HTTPMiddlewares(ctx context.Context) (map[string]bool, error) {
	return c.names(ctx, "/api/http/middlewares")
}

func (c *Client) names(ctx context.Context, path string) (map[string]bool, error) {
	var raw struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := c.get(ctx, path, &raw); err != nil {
		return nil, err
	}
	m := map[string]bool{}
	for _, it := range raw.Items {
		// 与 HTTPRouters 一致：剥 @file 后缀、滤 @internal（宪章 XI 分域 + drift 比对名空间统一）
		if base, ok := platformName(it.Name); ok {
			m[base] = true
		}
	}
	return m, nil
}

// Live 轻量可达性探测（节点 status 判定输入，FR-002/003）。
func (c *Client) Live(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.Overview(ctx)
	return err
}
