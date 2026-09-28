package helper

import (
	"encoding/json"
	"math"
	"unicode/utf8"

	"github.com/robertkonga/yekonga-server-go/datatype"
)

// normalizeJSON returns what json.Unmarshal would produce from json.Marshal(v)
// into an interface{} (maps become map[string]interface{}, slices
// []interface{}, numbers float64), without encoding to bytes and back. Common
// types are walked directly; anything else goes through JSON, so the result
// is always the same as a real round-trip. ok is false when json.Marshal
// would fail (e.g. NaN), in which case callers must treat the whole value as
// unconvertible, as a round-trip would.
func normalizeJSON(v interface{}) (result interface{}, ok bool) {
	switch x := v.(type) {
	case nil:
		return nil, true
	case bool:
		return x, true
	case string:
		// json.Marshal replaces invalid UTF-8, so let JSON handle those.
		if utf8.ValidString(x) {
			return x, true
		}
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, false
		}
		return x, true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case map[string]interface{}:
		return normalizeJSONMap(x)
	case datatype.DataMap:
		return normalizeJSONMap(x)
	case *datatype.DataMap:
		if x == nil {
			return nil, true
		}
		return normalizeJSONMap(*x)
	case []interface{}:
		if x == nil {
			return nil, true
		}

		out := make([]interface{}, len(x))
		for i, item := range x {
			normalized, ok := normalizeJSON(item)
			if !ok {
				return nil, false
			}
			out[i] = normalized
		}
		return out, true
	}

	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}

	if err := json.Unmarshal(b, &result); err != nil {
		return nil, false
	}

	return result, true
}

func normalizeJSONMap(m map[string]interface{}) (interface{}, bool) {
	if m == nil {
		return nil, true
	}

	out := make(map[string]interface{}, len(m))
	for k, item := range m {
		normalized, ok := normalizeJSON(item)
		if !ok {
			return nil, false
		}
		out[k] = normalized
	}

	return out, true
}

// toMapFast is ToMap for the inputs it can convert without JSON: map
// arguments converted to map[string]interface{} or map[string]string.
// handled is false when the caller should fall back to the JSON round-trip.
func toMapFast[T any](data interface{}) (result map[string]T, handled bool) {
	switch data.(type) {
	case map[string]interface{}, datatype.DataMap, *datatype.DataMap:
	default:
		return nil, false
	}

	normalized, ok := normalizeJSON(data)
	if !ok {
		return nil, true // json.Marshal would fail, so ToMap returns nil
	}

	m, isMap := normalized.(map[string]interface{})
	if !isMap {
		return nil, true // JSON null
	}

	switch any(result).(type) {
	case map[string]interface{}:
		return any(m).(map[string]T), true
	case map[string]string:
		out := make(map[string]string, len(m))
		for k, v := range m {
			switch s := v.(type) {
			case string:
				out[k] = s
			case nil:
				out[k] = "" // json.Unmarshal leaves a string as "" for null
			default:
				return nil, true // json.Unmarshal fails on a non-string value
			}
		}
		return any(out).(map[string]T), true
	}

	return nil, false
}
