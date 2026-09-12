// Package state 集中定义五类状态机与合法迁移表（T018，data-model §6/§8/§9/§10/§2）。
// 非法迁移一律返回错误（宪法 XII：状态语义不散落各处）。
package state

import "fmt"

// Machine 是一条状态机：值集合 + 允许迁移边。
type Machine struct {
	Name    string
	States  map[string]bool
	Allowed map[string][]string // from → to 列表
}

func (m Machine) Valid(s string) bool { return m.States[s] }

// Can 判定迁移合法性；未知状态返回错误。
func (m Machine) Can(from, to string) error {
	if !m.Valid(from) {
		return fmt.Errorf("%s 非法状态 %q", m.Name, from)
	}
	if !m.Valid(to) {
		return fmt.Errorf("%s 非法状态 %q", m.Name, to)
	}
	for _, t := range m.Allowed[from] {
		if t == to {
			return nil
		}
	}
	return fmt.Errorf("%s 不允许 %s → %s", m.Name, from, to)
}

// Route（FR-017：draft → enabled ⇄ disabled → archived 终态）。
var Route = Machine{
	Name:   "route",
	States: set("draft", "enabled", "disabled", "archived"),
	Allowed: map[string][]string{
		"draft":    {"enabled", "archived"},
		"enabled":  {"disabled", "archived"},
		"disabled": {"enabled", "archived"},
		"archived": {},
	},
}

// ConfigVersion（UF-4：pending → validating → ready；ready 后方可部署）。
var ConfigVersion = Machine{
	Name:   "config_version",
	States: set("pending", "validating", "ready", "failed"),
	Allowed: map[string][]string{
		"pending":    {"validating", "failed"},
		"validating": {"ready", "failed"},
		"ready":      {},
		"failed":     {},
	},
}

// Deployment（FR-028 / 宪法 XII / R15）。
var Deployment = Machine{
	Name:   "deployment",
	States: set("pending", "validating", "ready", "deploying", "success", "failed"),
	Allowed: map[string][]string{
		"pending":    {"validating", "failed"},
		"validating": {"ready", "failed"},
		"ready":      {"deploying", "failed"}, // failed：离线/审批拒绝/重启冻结
		"deploying":  {"success", "failed"},
		"success":    {},
		"failed":     {},
	},
}

// ReleaseRequest（FR-037）。
var ReleaseRequest = Machine{
	Name:   "release_request",
	States: set("pending", "approved", "rejected", "cancelled"),
	Allowed: map[string][]string{
		"pending":   {"approved", "rejected", "cancelled"},
		"approved":  {},
		"rejected":  {},
		"cancelled": {},
	},
}

// Node 生命周期（data-model §2；启停用 enabled 列，生命周期状态独立）。
var Node = Machine{
	Name:   "node",
	States: set("active", "inactive"),
	Allowed: map[string][]string{
		"active":   {"inactive"},
		"inactive": {"active"},
	},
}

// NodeProbeStatus 探测态（FR-002/003，非生命周期）。
var NodeProbeStatus = set("online", "offline", "degraded", "unknown")

func set(vals ...string) map[string]bool {
	m := make(map[string]bool, len(vals))
	for _, v := range vals {
		m[v] = true
	}
	return m
}
