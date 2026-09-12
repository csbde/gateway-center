// Package diff 以逻辑资源为单位比较两份快照（T022，research R11、FR-029/AC-011）。
// 输出 added/modified/removed，用户语言展示（「哪些路由变了」）而非文本行 diff。
package diff

import (
	"encoding/json"
	"fmt"
	"sort"

	"gateway-center/backend/internal/generate"
)

type ChangeKind string

const (
	Added    ChangeKind = "added"
	Modified ChangeKind = "modified"
	Removed  ChangeKind = "removed"
)

// Item 是一条资源级差异。
type Item struct {
	Kind     ChangeKind `json:"kind"`
	Resource string     `json:"resource"` // router|service|middleware|certificate
	Name     string     `json:"name"`     // 业务名称（用户语言）
	Detail   string     `json:"detail,omitempty"`
}

type key struct {
	kind, id string
}

// SnapshotDiff 对比 old→new；old 为 nil 视为空平台。
func SnapshotDiff(old, new *generate.Snapshot) []Item {
	if old == nil {
		old = &generate.Snapshot{}
	}
	if new == nil {
		new = &generate.Snapshot{}
	}
	oldIdx := index(old)
	newIdx := index(new)

	var items []Item
	for k, nv := range newIdx {
		ov, existed := oldIdx[k]
		if !existed {
			items = append(items, Item{Added, k.kind, nv.name, ""})
			continue
		}
		if ov.fingerprint != nv.fingerprint {
			items = append(items, Item{Modified, k.kind, nv.name, describeChange(k.kind, ov.fingerprint, nv.fingerprint)})
		}
	}
	for k, ov := range oldIdx {
		if _, still := newIdx[k]; !still {
			items = append(items, Item{Removed, k.kind, ov.name, ""})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Resource != items[j].Resource {
			return items[i].Resource < items[j].Resource
		}
		order := map[ChangeKind]int{Removed: 0, Modified: 1, Added: 2}
		if order[items[i].Kind] != order[items[j].Kind] {
			return order[items[i].Kind] < order[items[j].Kind]
		}
		return items[i].Name < items[j].Name
	})
	return items
}

type entry struct {
	name, fingerprint string
}

func index(s *generate.Snapshot) map[key]entry {
	m := map[key]entry{}
	for _, r := range s.Routers {
		m[key{"router", r.ID}] = entry{r.Name, fp(r)}
	}
	for _, sv := range s.Services {
		m[key{"service", sv.ID}] = entry{sv.Name, fp(sv)}
	}
	for _, mw := range s.Middlewares {
		m[key{"middleware", mw.ID}] = entry{mw.Name, fp(mw)}
	}
	for _, c := range s.Certificates {
		m[key{"certificate", c.ID}] = entry{c.Name, fp(c)}
	}
	return m
}

// fp：结构体语义指纹（JSON 归一化后 sha 太热，直接用序列化串比对即可，规模小）。
func fp(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func describeChange(kind, oldFP, newFP string) string {
	return fmt.Sprintf("%s 配置已变更", kind)
}

// Summary 生成 changes_summary（FR-029：随版本入库，资源计数）。
func Summary(items []Item) map[string]int {
	s := map[string]int{"added": 0, "modified": 0, "removed": 0}
	for _, it := range items {
		s[string(it.Kind)]++
	}
	return s
}
