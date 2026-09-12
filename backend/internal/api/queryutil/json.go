package queryutil

import (
	"encoding/json"
	"io"
)

// DecodeStrict 拒绝未知字段（契约严格性，FR-043 API First）。
func DecodeStrict(body io.Reader, v any) error {
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func encJSON(w io.Writer, v any) {
	_ = json.NewEncoder(w).Encode(v)
}
