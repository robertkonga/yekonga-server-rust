package yekonga

import (
	"reflect"
	"testing"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
)

func mongoFilterFor(where datatype.DataMap) datatype.DataMap {
	model := &DataModel{
		Name:           "Order",
		IDKeys:         []string{"outletId", "tenantId"},
		ParentFields:   map[string]DataModelFieldForeignKey{},
		ChildrenFields: map[string]DataModelFieldForeignKey{},
	}

	query := &DataModelQuery{Model: model}
	query.WhereAll(where)

	return *(&mongodbConnection{query: query}).where()
}

func TestMongoWhereFilter(t *testing.T) {
	tenantHex := bson.NewObjectID().Hex()
	tenantId, _ := bson.ObjectIDFromHex(tenantHex)

	tests := []struct {
		name  string
		where datatype.DataMap
		want  datatype.DataMap
	}{
		{
			name:  "empty",
			where: datatype.DataMap{},
			want:  datatype.DataMap{},
		},
		{
			name:  "plain values become $eq, ID keys become ObjectIDs",
			where: datatype.DataMap{"status": "active", "tenantId": tenantHex},
			want: datatype.DataMap{
				"status":   datatype.DataMap{"$eq": "active"},
				"tenantId": datatype.DataMap{"$eq": tenantId},
			},
		},
		{
			name:  "operators mixed with plain values",
			where: datatype.DataMap{"name": map[string]interface{}{"equalTo": "x"}, "status": map[string]interface{}{"notIn": []interface{}{"a"}}, "kind": "b"},
			want: datatype.DataMap{
				"name":   datatype.DataMap{"$eq": "x"},
				"status": datatype.DataMap{"$nin": []interface{}{"a"}},
				"kind":   datatype.DataMap{"$eq": "b"},
			},
		},
		{
			name: "logical keys alongside plain values",
			where: datatype.DataMap{
				"OR":     []interface{}{map[string]interface{}{"code": "x"}, map[string]interface{}{"code": map[string]interface{}{"equalTo": "y"}}},
				"status": "a",
			},
			want: datatype.DataMap{
				"$or":    []interface{}{datatype.DataMap{"code": datatype.DataMap{"$eq": "x"}}, datatype.DataMap{"code": datatype.DataMap{"$eq": "y"}}},
				"status": datatype.DataMap{"$eq": "a"},
			},
		},
	}

	tests = append(tests, []struct {
		name  string
		where datatype.DataMap
		want  datatype.DataMap
	}{
		{
			name:  "numeric comparisons keep their value",
			where: datatype.DataMap{"price": map[string]interface{}{"greaterThan": 5, "lessThanOrEqualTo": 9.5}, "qty": map[string]interface{}{"notLessThan": "3"}},
			want: datatype.DataMap{
				"price": datatype.DataMap{"$gt": 5, "$lte": 9.5},
				"qty":   datatype.DataMap{"$not": datatype.DataMap{"$lt": 3.0}},
			},
		},
		{
			name:  "exists false matches missing or null",
			where: datatype.DataMap{"deletedAt": map[string]interface{}{"exists": false}},
			want:  datatype.DataMap{"deletedAt": datatype.DataMap{"$eq": nil}},
		},
		{
			name:  "exists true matches present and not null",
			where: datatype.DataMap{"deletedAt": map[string]interface{}{"exists": true}},
			want:  datatype.DataMap{"deletedAt": datatype.DataMap{"$exists": true, "$nin": []interface{}{nil}}},
		},
		{
			name: "exists false alongside OR keeps both",
			where: datatype.DataMap{
				"deletedAt": map[string]interface{}{"exists": false},
				"OR":        []interface{}{map[string]interface{}{"a": 1}, map[string]interface{}{"b": 2}},
			},
			want: datatype.DataMap{
				"deletedAt": datatype.DataMap{"$eq": nil},
				"$or":       []interface{}{datatype.DataMap{"a": datatype.DataMap{"$eq": 1}}, datatype.DataMap{"b": datatype.DataMap{"$eq": 2}}},
			},
		},
		{
			name: "a list of plain values doesn't disturb AND",
			where: datatype.DataMap{
				"AND":  []interface{}{map[string]interface{}{"a": 1}},
				"tags": []interface{}{"x", "y"},
			},
			want: datatype.DataMap{
				"$and": []interface{}{datatype.DataMap{"a": datatype.DataMap{"$eq": 1}}},
				"tags": datatype.DataMap{"$eq": []interface{}{"x", "y"}},
			},
		},
	}...)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mongoFilterFor(tt.where); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("filter mismatch\n got: %#v\nwant: %#v", got, tt.want)
			}
		})
	}
}

// Plain values used to be filtered as $eq or $in depending on map iteration
// order whenever another key used an operator map.
func TestMongoWhereFilterIsDeterministic(t *testing.T) {
	where := datatype.DataMap{
		"tags":  []interface{}{"a", "b"},
		"name":  map[string]interface{}{"equalTo": "x"},
		"price": map[string]interface{}{"greaterThanOrEqualTo": "5"},
	}

	first := mongoFilterFor(where)
	for i := 0; i < 50; i++ {
		if got := mongoFilterFor(where); !reflect.DeepEqual(got, first) {
			t.Fatalf("filter changed between runs\nfirst: %#v\n  got: %#v", first, got)
		}
	}

	if want := (datatype.DataMap{"$eq": []interface{}{"a", "b"}}); !reflect.DeepEqual(first["tags"], want) {
		t.Errorf("tags = %#v, want %#v", first["tags"], want)
	}
}

func TestAddFilterClause(t *testing.T) {
	tests := []struct {
		name      string
		existing  datatype.DataMap
		key       string
		condition interface{}
		want      datatype.DataMap
	}{
		{
			name:      "new key",
			existing:  datatype.DataMap{},
			key:       "outletId",
			condition: datatype.DataMap{"$in": []interface{}{1}},
			want:      datatype.DataMap{"outletId": datatype.DataMap{"$in": []interface{}{1}}},
		},
		{
			name:      "different operators on one field merge",
			existing:  datatype.DataMap{"outletId": datatype.DataMap{"$eq": 1}},
			key:       "outletId",
			condition: datatype.DataMap{"$in": []interface{}{1, 2}},
			want:      datatype.DataMap{"outletId": datatype.DataMap{"$eq": 1, "$in": []interface{}{1, 2}}},
		},
		{
			name:      "the same operator goes under $and",
			existing:  datatype.DataMap{"outletId": datatype.DataMap{"$in": []interface{}{1}}},
			key:       "outletId",
			condition: datatype.DataMap{"$in": []interface{}{2}},
			want: datatype.DataMap{"$and": []interface{}{
				datatype.DataMap{"outletId": datatype.DataMap{"$in": []interface{}{1}}},
				datatype.DataMap{"outletId": datatype.DataMap{"$in": []interface{}{2}}},
			}},
		},
		{
			name:      "two $or conditions both apply",
			existing:  datatype.DataMap{"$or": []interface{}{"a"}},
			key:       "$or",
			condition: []interface{}{"b"},
			want: datatype.DataMap{"$and": []interface{}{
				datatype.DataMap{"$or": []interface{}{"a"}},
				datatype.DataMap{"$or": []interface{}{"b"}},
			}},
		},
		{
			name:      "$and lists concatenate",
			existing:  datatype.DataMap{"$and": []interface{}{"a"}},
			key:       "$and",
			condition: []interface{}{"b"},
			want:      datatype.DataMap{"$and": []interface{}{"a", "b"}},
		},
		{
			name:      "an identical condition is not repeated",
			existing:  datatype.DataMap{"x": datatype.DataMap{"$eq": 1}},
			key:       "x",
			condition: datatype.DataMap{"$eq": 1},
			want:      datatype.DataMap{"x": datatype.DataMap{"$eq": 1}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addFilterClause(tt.existing, tt.key, tt.condition)
			if !reflect.DeepEqual(tt.existing, tt.want) {
				t.Errorf("\n got: %#v\nwant: %#v", tt.existing, tt.want)
			}
		})
	}
}

func TestMongoWhereIsBuiltOnce(t *testing.T) {
	query := &DataModelQuery{Model: &DataModel{Name: "Order"}}
	query.Where("status", "a")
	con := &mongodbConnection{query: query}

	if con.where() != con.where() {
		t.Error("where() rebuilt the filter on the same connection")
	}
}
