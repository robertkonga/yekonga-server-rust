package helper

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
)

// jsonRoundTrip is what ToMap did for every input before the fast path.
func jsonRoundTrip[T any](data interface{}) map[string]T {
	var result map[string]T
	b, _ := json.Marshal(data)
	if err := json.Unmarshal(b, &result); err != nil {
		return nil
	}
	return result
}

func toMapInputs() map[string]interface{} {
	id := bson.NewObjectID()
	when := time.Date(2026, 9, 27, 10, 30, 0, 123456789, time.UTC)
	var nilDataMap *datatype.DataMap

	return map[string]interface{}{
		"empty":        map[string]interface{}{},
		"nil map":      map[string]interface{}(nil),
		"nil pointer":  nilDataMap,
		"scalars":      map[string]interface{}{"s": "x", "b": true, "i": 5, "i64": int64(1 << 60), "u8": uint8(7), "f": 1.5, "neg0": math.Copysign(0, -1), "nil": nil},
		"float32":      map[string]interface{}{"f": float32(0.1)},
		"nested":       map[string]interface{}{"where": map[string]interface{}{"price": map[string]interface{}{"greaterThan": 5}, "in": []interface{}{1, "a", nil}}},
		"leaf types":   map[string]interface{}{"id": id, "when": when, "list": []string{"a"}, "strmap": map[string]string{"k": "v"}},
		"datamap":      datatype.DataMap{"a": datatype.DataMap{"b": 1}},
		"datamap ptr":  &datatype.DataMap{"a": []datatype.DataMap{{"b": 2}}},
		"invalid utf8": map[string]interface{}{"s": string([]byte{0xff, 'a'})},
		"html":         map[string]interface{}{"s": "<b>&</b>"},
		"nan":          map[string]interface{}{"ok": 1, "bad": math.NaN()},
		"orderBy":      map[string]interface{}{"name": "asc", "createdAt": "desc"},
		"orderBy null": map[string]interface{}{"name": nil},
	}
}

func TestToMapMatchesJSONRoundTrip(t *testing.T) {
	for name, input := range toMapInputs() {
		if got, want := ToMap[interface{}](input), jsonRoundTrip[interface{}](input); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: ToMap[interface{}]\n got: %#v\nwant: %#v", name, got, want)
		}

		if got, want := ToMap[string](input), jsonRoundTrip[string](input); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: ToMap[string]\n got: %#v\nwant: %#v", name, got, want)
		}
	}
}

func TestToMapCopiesInput(t *testing.T) {
	inner := map[string]interface{}{"b": 1}
	input := map[string]interface{}{"a": inner}

	out := ToMap[interface{}](input)
	out["a"].(map[string]interface{})["b"] = 2

	if inner["b"] != 1 {
		t.Fatal("ToMap returned a map that shares nested maps with its input")
	}
}

// bson.M isn't one of the fast-path types, so it takes the original
// reflection path and serves as the reference.
func TestGetMapValueMatchesGenericPath(t *testing.T) {
	data := map[string]interface{}{
		"name":   "x",
		"nested": map[string]interface{}{"inner": map[string]interface{}{"v": 3}},
		"list":   []interface{}{map[string]interface{}{"a": 1}},
		"nil":    nil,
		"":       "empty key",
	}
	reference := bson.M(data)

	keys := []string{"name", "missing", "nested.inner.v", "nested.inner", "nested.missing.v", "list.0.a", "nil", "", "name."}

	for _, key := range keys {
		want := GetMapValue(reference, key)
		for _, input := range []interface{}{data, datatype.DataMap(data), &data} {
			if got := GetMapValue(input, key); !reflect.DeepEqual(got, want) {
				t.Errorf("GetMapValue(%T, %q) = %#v, want %#v", input, key, got, want)
			}
		}
	}

	if got := GetMapValue(map[string]interface{}{}, "a"); got != nil {
		t.Errorf("empty map: got %#v", got)
	}

	var nilMap *datatype.DataMap
	if got := GetMapValue(nilMap, "a"); got != nil {
		t.Errorf("nil *DataMap: got %#v", got)
	}
}

func BenchmarkToMap(b *testing.B) {
	input := map[string]interface{}{"where": map[string]interface{}{"status": map[string]interface{}{"equalTo": "active"}, "price": map[string]interface{}{"greaterThan": 5}}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ToMap[interface{}](input)
	}
}

func BenchmarkToMapJSON(b *testing.B) {
	input := map[string]interface{}{"where": map[string]interface{}{"status": map[string]interface{}{"equalTo": "active"}, "price": map[string]interface{}{"greaterThan": 5}}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		jsonRoundTrip[interface{}](input)
	}
}

func BenchmarkGetMapValue(b *testing.B) {
	m := datatype.DataMap{"name": "x"}
	for i := 0; i < 30; i++ {
		m[string(rune('a'+i%26))+string(rune('A'+i))] = i
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		GetMapValue(m, "name")
	}
}
