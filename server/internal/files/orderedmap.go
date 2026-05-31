package files

import (
	"bytes"
	"encoding/json"
)

// orderedMap marshals to a JSON object preserving insertion order. Go's
// encoding/json sorts map keys, which would scramble event-story line order
// (story flow). encoding/json calls MarshalJSON to get compact bytes and then
// applies indentation, so nested orderedMaps indent correctly under
// MarshalIndentCompat.
type orderedMap struct {
	keys []string
	vals []any
}

func newOrderedMap() *orderedMap { return &orderedMap{} }

func (o *orderedMap) set(key string, val any) {
	o.keys = append(o.keys, key)
	o.vals = append(o.vals, val)
}

func (o *orderedMap) len() int { return len(o.keys) }

func (o *orderedMap) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := marshalValue(k)
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		vb, err := marshalValue(o.vals[i])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// marshalValue encodes a single value as compact JSON without HTML escaping,
// matching the legacy files (which keep &, <, > literal).
func marshalValue(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
