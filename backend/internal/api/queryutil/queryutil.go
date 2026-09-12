// Package queryutil 统一列表参数与响应（T023，contracts/README.md「列表与分页」）。
// GET ?page=1&page_size=20(≤100)&q=&sort=-created_at&node_id= → {items,page,page_size,total}
package queryutil

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"gateway-center/backend/internal/api/httperr"
)

var safeSortRe = regexp.MustCompile(`^-?[a-z_]{1,32}$`)

type ListQuery struct {
	Page     int
	PageSize int
	Q        string
	Sort     string // SQL ORDER 片段（仅白名单列）
	NodeID   string
}

func Parse(r *http.Request) (ListQuery, *httperr.APIError) {
	q := ListQuery{Page: 1, PageSize: 20}
	qq := r.URL.Query()
	if v := qq.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return q, httperr.ValidationFailed("page 必须为正整数", httperr.Detail{Field: "page", Hint: "从 1 开始"})
		}
		q.Page = n
	}
	if v := qq.Get("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			return q, httperr.ValidationFailed("page_size 范围 1–100", httperr.Detail{Field: "page_size"})
		}
		q.PageSize = n
	}
	q.Q = strings.TrimSpace(qq.Get("q"))
	q.NodeID = firstNonEmpty(qq.Get("node_id"), r.Header.Get("X-Gateway-Node"))
	if s := qq.Get("sort"); s != "" {
		col := strings.TrimPrefix(s, "-")
		if !safeSortRe.MatchString(col) {
			return q, httperr.ValidationFailed("sort 列名不合法", httperr.Detail{Field: "sort", Hint: "如 -created_at"})
		}
		dir := "ASC"
		if strings.HasPrefix(s, "-") {
			dir = "DESC"
		}
		q.Sort = col + " " + dir
	}
	return q, nil
}

// ListResponse 是统一列表响应体。
type ListResponse struct {
	Items    any   `json:"items"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

func Respond(w http.ResponseWriter, l ListQuery, items any, total int64) {
	writeJSON(w, http.StatusOK, ListResponse{Items: items, Page: l.Page, PageSize: l.PageSize, Total: total})
}

// WriteObject 单对象/自定义体响应（handlers 统一出口）。
func WriteObject(w http.ResponseWriter, code int, v any) { writeJSON(w, code, v) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	encJSON(w, v)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
