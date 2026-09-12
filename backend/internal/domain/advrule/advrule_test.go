// T062 [US3] · advrule 白名单文法边界（FR-015/016、宪法 II）：
// 合法组合、非法函数/引号/括号、长度上限、Normalize 稳定性。纯函数测试。
package advrule_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"gateway-center/backend/internal/domain/advrule"
)

func TestValidGrammar(t *testing.T) {
	valid := []string{
		"Host(`crm.example.com`)",
		"PathPrefix(`/api`)",
		"Host(`a.com`) && Path(`/x`)",
		"Host(`a.com`) || Host(`b.com`)",
		"(Host(`a.com`) || Host(`b.com`)) && PathPrefix(`/api`)",
		"Headers(`X-Tenant`,`t1`) && Method(`GET`,`POST`)",
		"HostRegexp(`^[a-z]+\\.example\\.com$`)",
		"HostSNI(`*.example.com`)",
		"  Host(`a.com`)&&Path(`/b`)  ", // 紧凑书写 + 首尾空白
	}
	for _, r := range valid {
		assert.Equal(t, "", advrule.Validate(r), "应合法: %s", r)
	}
}

func TestRejectsOutsideWhitelist(t *testing.T) {
	cases := map[string]string{
		"":                                "表达式为空",
		"   ":                             "表达式为空",
		"Foo(`x`)":                        "不在白名单",
		"Host(`a.com`) && Router(`x`)":    "不在白名单",
		"Host(`a.com`) &&":                "意外结束",
		"&& Path(`/x`)":                   "需要 函数",
		"Host(a.com)":                     "反引号字符串",
		"Host(`a.com)":                    "缺少结束引号",
		"Host()":                          "至少一个参数",
		"(Host(`a.com`)":                  "缺少右括号",
		"Host(`a.com`))":                  "多余字符",
		"Host(`a.com`) | Path(`/x`)":      "多余字符", // 单 | 非法
		"Host(`a.com`) || || Path(`/x`)":  "需要 函数",
		strings.Repeat("a", 1025):         "1024",
		"Path(`/x`); DROP TABLE routes;":  "多余字符", // 注入尝试止步于文法
		"ClientHost(`1.2.3.4`)":           "不在白名单",
	}
	for rule, wantSubstr := range cases {
		msg := advrule.Validate(rule)
		assert.NotEqual(t, "", msg, "应拒绝: %q", rule)
		assert.Contains(t, msg, wantSubstr, "错误信息应定位原因: %q -> %s", rule, msg)
	}
}

func TestNormalize(t *testing.T) {
	assert.Equal(t, "Host(`a.com`) && Path(`/x`)",
		advrule.Normalize("  Host(`a.com`)    &&\tPath(`/x`) "))
	// 幂等：Normalize 结果再 Normalize 不变（预览稳定）
	once := advrule.Normalize("( Host(`a.com`)\t|| Host(`b.com`) ) && Path(`/api`)")
	assert.Equal(t, once, advrule.Normalize(once))
}
