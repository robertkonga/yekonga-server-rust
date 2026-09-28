package yekonga

import (
	"reflect"
	"testing"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
)

func sqlTestModel() *DataModel {
	return &DataModel{
		Name:       "Order",
		Collection: "orders",
		Fields: map[string]DataModelField{
			"id":       {Name: "id", Kind: DataModelID},
			"status":   {Name: "status", Kind: DataModelString},
			"amount":   {Name: "amount", Kind: DataModelNumber},
			"outletId": {Name: "outletId", Kind: DataModelID},
			"tags":     {Name: "tags", Kind: DataModelArray},
			"meta":     {Name: "meta", Kind: DataModelObject},
		},
		ParentFields:   map[string]DataModelFieldForeignKey{},
		ChildrenFields: map[string]DataModelFieldForeignKey{},
	}
}

func TestSQLFilterTranslation(t *testing.T) {
	id := bson.NewObjectID()

	tests := []struct {
		name   string
		filter datatype.DataMap
		sql    string
		args   []interface{}
	}{
		{
			name:   "empty",
			filter: datatype.DataMap{},
			sql:    "",
		},
		{
			name:   "equality and id",
			filter: datatype.DataMap{"status": datatype.DataMap{"$eq": "paid"}, "_id": datatype.DataMap{"$eq": id}},
			sql:    "(((`_id` = ?) IS TRUE) AND ((`status` = ?) IS TRUE))",
			args:   []interface{}{id.Hex(), "paid"},
		},
		{
			name:   "$ne also matches null",
			filter: datatype.DataMap{"status": datatype.DataMap{"$ne": "paid"}},
			sql:    "(((NOT (`status` = ?) OR `status` IS NULL)) IS TRUE)",
			args:   []interface{}{"paid"},
		},
		{
			name:   "$in with null",
			filter: datatype.DataMap{"status": datatype.DataMap{"$in": []interface{}{"a", nil}}},
			sql:    "(((`status` IN (?) OR `status` IS NULL)) IS TRUE)",
			args:   []interface{}{"a"},
		},
		{
			name:   "array element",
			filter: datatype.DataMap{"tags": datatype.DataMap{"$eq": "vip"}},
			sql:    "((JSON_CONTAINS(`tags`, ?)) IS TRUE)",
			args:   []interface{}{`"vip"`},
		},
		{
			name:   "json path",
			filter: datatype.DataMap{"meta.city": datatype.DataMap{"$eq": "arusha"}},
			sql:    "((JSON_UNQUOTE(JSON_EXTRACT(`meta`, '$.\"city\"')) = ?) IS TRUE)",
			args:   []interface{}{"arusha"},
		},
		{
			name:   "unknown field behaves as missing",
			filter: datatype.DataMap{"nope": datatype.DataMap{"$eq": "x"}},
			sql:    "(((NULL = ?) IS TRUE))",
			args:   []interface{}{"x"},
		},
		{
			name: "nor",
			filter: datatype.DataMap{"$nor": []interface{}{
				datatype.DataMap{"status": datatype.DataMap{"$eq": "a"}},
				datatype.DataMap{"status": datatype.DataMap{"$eq": "b"}},
			}},
			sql:  "(NOT ((((`status` = ?) IS TRUE)) OR (((`status` = ?) IS TRUE))))",
			args: []interface{}{"a", "b"},
		},
	}

	for _, tt := range tests {
		b := &sqlFilterBuilder{model: sqlTestModel()}
		sql, err := b.build(tt.filter)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}

		// The builder wraps each part; compare with whitespace as written.
		if tt.sql != "" && sql != tt.sql && sql != "("+tt.sql+")" {
			t.Errorf("%s:\n got: %s\nwant: %s", tt.name, sql, tt.sql)
		}
		if tt.sql == "" && sql != "" {
			t.Errorf("%s: got %q, want no condition", tt.name, sql)
		}
		if len(tt.args) > 0 && !reflect.DeepEqual(b.args, tt.args) {
			t.Errorf("%s: args %#v, want %#v", tt.name, b.args, tt.args)
		}
	}
}

func TestSQLFilterRejectsUnsupportedOperator(t *testing.T) {
	b := &sqlFilterBuilder{model: sqlTestModel()}
	if _, err := b.build(datatype.DataMap{"status": datatype.DataMap{"$type": "string"}}); err == nil {
		t.Error("expected an error for $type")
	}
}

func TestSQLValueRoundTrip(t *testing.T) {
	id := bson.NewObjectID()

	encoded, _ := encodeSQLValue(DataModelID, id)
	if decoded := decodeSQLValue(DataModelID, []byte(encoded.(string))); decoded != id {
		t.Errorf("id: got %v, want %v", decoded, id)
	}

	encoded, _ = encodeSQLValue(DataModelObject, map[string]interface{}{"a": 1.0})
	if decoded := decodeSQLValue(DataModelObject, []byte(encoded.(string))); !reflect.DeepEqual(decoded, map[string]interface{}{"a": 1.0}) {
		t.Errorf("object: got %#v", decoded)
	}

	if decoded := decodeSQLValue(DataModelNumber, []byte("42")); decoded != int64(42) {
		t.Errorf("number: got %#v", decoded)
	}
	if decoded := decodeSQLValue(DataModelBool, int64(1)); decoded != true {
		t.Errorf("bool: got %#v", decoded)
	}
}
