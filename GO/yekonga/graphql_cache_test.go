package yekonga

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/plugins/graphql"
)

func newTestGraphqlSchema(t *testing.T) *graphql.Schema {
	item := graphql.NewObject(graphql.ObjectConfig{
		Name: "Item",
		Fields: graphql.Fields{
			"id":   &graphql.Field{Type: graphql.String},
			"name": &graphql.Field{Type: graphql.String},
		},
	})

	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Query",
			Fields: graphql.Fields{
				"hello": &graphql.Field{
					Type: graphql.String,
					Args: graphql.FieldConfigArgument{"name": &graphql.ArgumentConfig{Type: graphql.String}},
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return "hello " + p.Args["name"].(string), nil
					},
				},
				"items": &graphql.Field{
					Type: graphql.NewList(item),
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return []map[string]interface{}{{"id": "1", "name": "a"}, {"id": "2", "name": "b"}}, nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	return &schema
}

func runTestGraphql(schema *graphql.Schema, cache *lookupCache[*graphqlDocument], query string, vars map[string]interface{}) (*graphql.Result, *RequestContext) {
	rc := &RequestContext{}
	result := executeGraphql(graphqlExecution{
		Schema:         schema,
		Cache:          cache,
		RequestString:  query,
		VariableValues: vars,
		Context:        rc,
		Parent:         context.Background(),
		WithSelectors:  true,
	})

	return result, rc
}

func TestExecuteGraphqlMatchesDo(t *testing.T) {
	schema := newTestGraphqlSchema(t)
	cache := newGraphqlDocumentCache()

	queries := []struct {
		query string
		vars  map[string]interface{}
	}{
		{`{ items { id name } }`, nil},
		{`query Q($n: String) { hello(name: $n) items { id } }`, map[string]interface{}{"n": "x"}},
		{`{ items { missing } }`, nil}, // validation error
		{`{ items { `, nil},            // parse error
	}

	for _, q := range queries {
		want := graphql.Do(graphql.Params{Schema: *schema, RequestString: q.query, VariableValues: q.vars, Context: context.Background()})

		for i := 0; i < 2; i++ { // the second run is served from the cache
			got, _ := runTestGraphql(schema, cache, q.query, q.vars)

			if !reflect.DeepEqual(got.Data, want.Data) || len(got.Errors) != len(want.Errors) {
				t.Fatalf("%q run %d:\n got: %v %v\nwant: %v %v", q.query, i, got.Data, got.Errors, want.Data, want.Errors)
			}

			for j := range want.Errors {
				if got.Errors[j].Message != want.Errors[j].Message {
					t.Errorf("%q: error %q, want %q", q.query, got.Errors[j].Message, want.Errors[j].Message)
				}
			}
		}
	}
}

func TestGraphqlDocumentCache(t *testing.T) {
	schema := newTestGraphqlSchema(t)
	cache := newGraphqlDocumentCache()

	runTestGraphql(schema, cache, `{ items { id } }`, nil)
	first, ok := cache.get(`{ items { id } }`)
	if !ok {
		t.Fatal("valid query was not cached")
	}

	runTestGraphql(schema, cache, `{ items { id } }`, nil)
	if second, _ := cache.get(`{ items { id } }`); second != first {
		t.Error("second run re-parsed instead of reusing the cached document")
	}

	runTestGraphql(schema, cache, `{ items { missing } }`, nil)
	if _, ok := cache.get(`{ items { missing } }`); ok {
		t.Error("invalid query was cached")
	}

	huge := `{ items { id } ` + strings.Repeat(" ", graphqlDocumentCacheMaxQueryBytes) + `}`
	if result, _ := runTestGraphql(schema, cache, huge, nil); len(result.Errors) > 0 {
		t.Fatalf("large query failed: %v", result.Errors)
	}
	if _, ok := cache.get(huge); ok {
		t.Error("query over the size limit was cached")
	}
}

// QuerySelectors used to be computed by parsing the query a second time.
func TestGraphqlQuerySelectorsUnchanged(t *testing.T) {
	schema := newTestGraphqlSchema(t)
	cache := newGraphqlDocumentCache()
	query := `{ items { id name } hello(name: "x") }`

	want := helper.ExtractGraphqlQuery(helper.ToMap[interface{}](graphql.Parser(query)), 0)

	for i := 0; i < 2; i++ {
		_, rc := runTestGraphql(schema, cache, query, nil)
		if !reflect.DeepEqual(rc.QuerySelectors, want) {
			t.Fatalf("run %d: QuerySelectors = %v, want %v", i, rc.QuerySelectors, want)
		}

		rc.QuerySelectors[99] = []string{"changed by a handler"}
	}
}

func TestGraphqlCachedDocumentConcurrentUse(t *testing.T) {
	schema := newTestGraphqlSchema(t)
	cache := newGraphqlDocumentCache()
	query := `query Q($n: String) { hello(name: $n) items { id name } }`

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, _ := runTestGraphql(schema, cache, query, map[string]interface{}{"n": "x"})
			if len(result.Errors) > 0 {
				t.Errorf("concurrent run failed: %v", result.Errors)
			}
		}()
	}
	wg.Wait()
}

func BenchmarkExecuteGraphqlCached(b *testing.B) {
	schema := newTestGraphqlSchema(&testing.T{})
	cache := newGraphqlDocumentCache()
	query := `query Q($n: String) { hello(name: $n) items { id name } }`
	vars := map[string]interface{}{"n": "x"}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		runTestGraphql(schema, cache, query, vars)
	}
}

func BenchmarkGraphqlDo(b *testing.B) {
	schema := newTestGraphqlSchema(&testing.T{})
	query := `query Q($n: String) { hello(name: $n) items { id name } }`
	vars := map[string]interface{}{"n": "x"}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		requestQueryMap := helper.ToMap[interface{}](graphql.Parser(query)) // the old double parse
		helper.ExtractGraphqlQuery(requestQueryMap, 0)
		graphql.Do(graphql.Params{Schema: *schema, RequestString: query, VariableValues: vars, Context: context.Background()})
	}
}
