package yekonga

import (
	"context"
	"testing"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/plugins/graphql"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
)

// A relation whose parent row has no link value must match nothing, even for
// resolvers that ignore setModelParams' result and run the query anyway.
func TestSetModelParamsUnlinkedRelationMatchesNothing(t *testing.T) {
	g := &GraphqlAutoBuild{}
	model := &DataModelQuery{Model: &DataModel{Name: "Tag"}}

	p := graphql.ResolveParams{
		Source:  map[string]interface{}{"name": "a post without tags"},
		Args:    map[string]interface{}{},
		Context: context.Background(),
	}

	if g.setModelParams(model, &p, "tagId", "id", false) {
		t.Fatal("setModelParams reported a link for a parent without one")
	}

	value, ok := model.where["_id"]
	if !ok || value != nil {
		t.Fatalf("where = %v, want a match-nothing _id filter", model.where)
	}
}

func TestImportInputMatches(t *testing.T) {
	id := bson.NewObjectID()
	saved := datatype.DataMap{"_id": id, "id": id, "code": "A1", "outletId": id, "qty": int32(5)}

	tests := []struct {
		name       string
		input      datatype.DataMap
		uniqueKeys []string
		want       bool
	}{
		{"same unique key", datatype.DataMap{"code": "A1"}, []string{"code"}, true},
		{"different unique key", datatype.DataMap{"code": "B1"}, []string{"code"}, false},
		{"missing unique key", datatype.DataMap{"name": "x"}, []string{"code"}, false},
		{"all unique keys must match", datatype.DataMap{"code": "A1", "qty": 6}, []string{"code", "qty"}, false},
		{"numbers of different types", datatype.DataMap{"code": "A1", "qty": 5}, []string{"code", "qty"}, true},
		{"ObjectID and its hex string", datatype.DataMap{"outletId": id.Hex()}, []string{"outletId"}, true},
		{"no unique keys: same id", datatype.DataMap{"id": id.Hex()}, nil, true},
		{"no unique keys: same _id", datatype.DataMap{"_id": id}, nil, true},
		{"no unique keys and no id", datatype.DataMap{"code": "A1"}, nil, false},
		{"no unique keys: other id", datatype.DataMap{"id": bson.NewObjectID().Hex()}, nil, false},
	}

	for _, tt := range tests {
		if got := importInputMatches(saved, tt.input, tt.uniqueKeys); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}
