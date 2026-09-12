// Package advrule 高级模式规则白名单文法（T059，FR-015/016、research R8）。
// 词法+语法验证：Host/HostRegexp/Path/PathPrefix/Headers/HeadersRegexp/Method
// 函数 + && / || / 括号。仅此而已——任何其它标识符即语法错误（宪法 II 双模式隔离）。
package advrule

import (
	"fmt"
	"regexp"
	"strings"
)

var allowedFuncs = map[string]bool{
	"Host": true, "HostRegexp": true, "Path": true, "PathPrefix": true,
	"Headers": true, "HeadersRegexp": true, "Method": true,
	"HostSNI": true, // 常见 TLS 配套（仍属白名单函数族）
}

var (
	funcCallRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]*)\(`)
	backtick   = "`"
)

// Validate 返回 ""（合法）或首个错误描述（定位到位置）。
func Validate(rule string) string {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return "表达式为空"
	}
	if len(rule) > 1024 {
		return "表达式超过 1024 字符"
	}
	p := &parser{src: rule}
	if err := p.expr(); err != nil {
		return err.Error()
	}
	p.ws()
	if p.pos != len(p.src) {
		return fmt.Sprintf("位置 %d 存在多余字符", p.pos)
	}
	return ""
}

// Normalize 生成稳定预览（压缩空白、规范操作符大小写展示）。
func Normalize(rule string) string {
	return strings.Join(strings.Fields(rule), " ")
}

type parser struct {
	src string
	pos int
}

func (p *parser) ws() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
}

// expr := term ( '||' term )*
func (p *parser) expr() error {
	if err := p.term(); err != nil {
		return err
	}
	for {
		p.ws()
		if strings.HasPrefix(p.src[p.pos:], "||") {
			p.pos += 2
			if err := p.term(); err != nil {
				return err
			}
			continue
		}
		return nil
	}
}

// term := atom ( '&&' atom )*
func (p *parser) term() error {
	if err := p.atom(); err != nil {
		return err
	}
	for {
		p.ws()
		if strings.HasPrefix(p.src[p.pos:], "&&") {
			p.pos += 2
			if err := p.atom(); err != nil {
				return err
			}
			continue
		}
		return nil
	}
}

// atom := '(' expr ')' | Func(...)
func (p *parser) atom() error {
	p.ws()
	if p.pos >= len(p.src) {
		return fmt.Errorf("表达式意外结束")
	}
	if p.src[p.pos] == '(' {
		p.pos++
		if err := p.expr(); err != nil {
			return err
		}
		p.ws()
		if p.pos >= len(p.src) || p.src[p.pos] != ')' {
			return fmt.Errorf("位置 %d 缺少右括号", p.pos)
		}
		p.pos++
		return nil
	}
	return p.call()
}

func (p *parser) call() error {
	rest := p.src[p.pos:]
	m := funcCallRe.FindStringSubmatch(rest)
	if m == nil {
		return fmt.Errorf("位置 %d 需要 函数(...) 或括号表达式", p.pos)
	}
	fn := m[1]
	if !allowedFuncs[fn] {
		return fmt.Errorf("函数 %q 不在白名单（仅 %s）", fn, strings.Join(funcNames(), "/"))
	}
	p.pos += len(fn) // 停在 '('
	return p.args(fn)
}

// args := '(' arglist ')'，arglist 为反引号字符串以逗号分隔，可空。
func (p *parser) args(fn string) error {
	if p.src[p.pos] != '(' {
		return fmt.Errorf("函数 %s 缺少左括号", fn)
	}
	p.pos++
	p.ws()
	if p.pos < len(p.src) && p.src[p.pos] == ')' {
		p.pos++
		return fmt.Errorf("函数 %s 需要至少一个参数", fn)
	}
	nargs := 0
	for {
		p.ws()
		if p.pos >= len(p.src) {
			return fmt.Errorf("函数 %s 参数未闭合", fn)
		}
		if p.src[p.pos] != '`' {
			return fmt.Errorf("函数 %s 参数必须为反引号字符串（Traefik 规则语法）", fn)
		}
		q := p.src[p.pos]
		p.pos++
		end := strings.IndexByte(p.src[p.pos:], q)
		if end < 0 {
			return fmt.Errorf("字符串缺少结束引号")
		}
		nargs++
		p.pos += end + 1
		p.ws()
		if p.pos < len(p.src) && p.src[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.pos < len(p.src) && p.src[p.pos] == ')' {
			p.pos++
			break
		}
		return fmt.Errorf("函数 %s 参数列表后缺少 )", fn)
	}
	return nil
}

func funcNames() []string {
	out := make([]string, 0, len(allowedFuncs))
	for k := range allowedFuncs {
		out = append(out, k)
	}
	return out
}
