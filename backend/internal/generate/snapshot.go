package generate

// Snapshot 是自包含业务快照（research R8、宪法 V）：
// 生成与回滚只读此结构，绝不回读当前业务表 —— 「回滚不会回出一半新半旧」的前提。
// JSON 序列化后整体存 ConfigVersion.snapshot (JSONB)。

type Snapshot struct {
	NodeID       string           `json:"node_id"`
	NodeName     string           `json:"node_name"`
	EnvType      string           `json:"env_type"`
	Routers      []RouterSnap     `json:"routers"`
	Services     []ServiceSnap    `json:"services"`
	Middlewares  []MiddlewareSnap `json:"middlewares"`
	Certificates []CertSnap       `json:"certificates"`
}

type RouterSnap struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"` // Traefik 资源名（= slug）
	Mode            string   `json:"mode"` // simple / advanced
	DomainName      string   `json:"domain_name,omitempty"`
	Path            string   `json:"path,omitempty"`
	MatchType       string   `json:"match_type,omitempty"` // exact / prefix
	AdvancedRule    string   `json:"advanced_rule,omitempty"`
	ServiceName     string   `json:"service_name"`
	HTTPS           bool     `json:"https"`
	EntryPoint      string   `json:"entry_point"`         // web / websecure（生成层约定，节点 static 预配）
	CertMode        string   `json:"cert_mode,omitempty"` // off / acme_http / acme_dns / imported
	Resolver        string   `json:"resolver,omitempty"`  // acme_* 时引用节点预配 resolver 名
	MiddlewareNames []string `json:"middleware_names"`    // 顺序 == route_middlewares.position（FR-018）
	Priority        int      `json:"priority"`
}

type ServiceSnap struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Targets []TargetSnap `json:"targets"`
}

type TargetSnap struct {
	URL    string `json:"url"`
	Weight int    `json:"weight"`
}

type MiddlewareSnap struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
}

// CertSnap 是导入证书观测/下发材料：私钥不落快照明文（R7），
// 以 PrivateKeyRef 指向加密行，Deploy 时由管线解密写入 0600 文件。
type CertSnap struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DomainName    string `json:"domain_name"`
	CertPEM       string `json:"cert_pem"` // 公钥链，可入快照
	PrivateKeyRef string `json:"private_key_ref"`
}
