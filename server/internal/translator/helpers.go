package translator

import (
	"strconv"
	"strings"
)

// ---- masterdata accessors (ported verbatim from legacy) ----

func getString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func getInt(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch n := m[key].(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	default:
		return 0
	}
}

func byIntID(list []map[string]any, key string) map[int]map[string]any {
	out := make(map[int]map[string]any, len(list))
	for _, item := range list {
		if id := getInt(item, key); id != 0 {
			out[id] = item
		}
	}
	return out
}

func toMapSlice(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func safeText(s string) string {
	s = strings.TrimSpace(s)
	if s == "-" {
		return ""
	}
	return s
}

func toParts(v any) map[string][]map[string]any {
	res := map[string][]map[string]any{}
	m, ok := v.(map[string]any)
	if !ok {
		return res
	}
	for k, raw := range m {
		res[k] = toMapSlice(raw)
	}
	return res
}

// ---- trace map: field -> jpText -> []refID ----

type traceMap map[string]map[string][]string

func newTraceMap(fields ...string) traceMap {
	tm := make(traceMap, len(fields))
	for _, f := range fields {
		tm[f] = map[string][]string{}
	}
	return tm
}

func (tm traceMap) add(field, jpText string, refID int) {
	tm.addStr(field, jpText, strconv.Itoa(refID))
}

func (tm traceMap) addStr(field, jpText, refID string) {
	jpText = strings.TrimSpace(jpText)
	refID = strings.TrimSpace(refID)
	if jpText == "" || refID == "" || refID == "0" {
		return
	}
	if tm[field] == nil {
		tm[field] = map[string][]string{}
	}
	for _, existing := range tm[field][jpText] {
		if existing == refID {
			return
		}
	}
	tm[field][jpText] = append(tm[field][jpText], refID)
}

// collectPair stores jp->cn, blanking cn when it equals jp (untranslated).
func collectPair(target map[string]string, jp, cn string) {
	jp = strings.TrimSpace(jp)
	cn = strings.TrimSpace(cn)
	if jp == "" {
		return
	}
	if cn == jp {
		cn = ""
	}
	target[jp] = cn
}
