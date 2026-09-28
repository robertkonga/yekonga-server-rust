package yekonga

import (
	"context"
	"time"

	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/plugins/graphql"
	"github.com/robertkonga/yekonga-server-go/plugins/graphql/gqlerrors"
	"github.com/robertkonga/yekonga-server-go/plugins/graphql/language/ast"
	"github.com/robertkonga/yekonga-server-go/plugins/graphql/language/parser"
	"github.com/robertkonga/yekonga-server-go/plugins/graphql/language/source"
)

const (
	// Clients send the same handful of queries over and over, so parsed and
	// validated documents are kept for a long time. Anything past the cap
	// (e.g. a client generating unique queries) just falls back to parsing.
	graphqlDocumentCacheTTL        = time.Hour
	graphqlDocumentCacheMaxEntries = 1000
	// Larger queries are still executed, just not cached, so a few huge
	// queries can't pin a lot of memory.
	graphqlDocumentCacheMaxQueryBytes = 16 << 10
)

// graphqlDocument is a query that parsed and validated against a schema.
// It's shared between requests and must be treated as read-only.
type graphqlDocument struct {
	ast       *ast.Document
	selectors map[uint][]string
}

func newGraphqlDocumentCache() *lookupCache[*graphqlDocument] {
	return newLookupCache[*graphqlDocument](graphqlDocumentCacheTTL, graphqlDocumentCacheMaxEntries)
}

// graphqlExecution is one GraphQL request, run against a schema.
type graphqlExecution struct {
	Schema         *graphql.Schema
	Cache          *lookupCache[*graphqlDocument]
	RequestString  string
	OperationName  string
	VariableValues map[string]interface{}
	RootObject     map[string]interface{}
	Context        *RequestContext
	Parent         context.Context

	// WithSelectors fills Context.QuerySelectors from the query before it runs.
	WithSelectors bool
}

// executeGraphql does what graphql.Do does (parse, validate, execute), but
// reuses the parsed and validated document for a query it has seen before.
// Parsing and validation cost more than executing a small query.
func executeGraphql(e graphqlExecution) *graphql.Result {
	doc, errResult := graphqlDocumentFor(e.Schema, e.Cache, e.RequestString, e.WithSelectors)
	if errResult != nil {
		return errResult
	}

	if e.WithSelectors && e.Context != nil {
		selectors := make(map[uint][]string, len(doc.selectors))
		for k, v := range doc.selectors {
			selectors[k] = v
		}
		e.Context.QuerySelectors = selectors
	}

	return graphql.Execute(&graphql.ExecuteParams{
		Schema:        *e.Schema,
		Root:          e.RootObject,
		AST:           doc.ast,
		OperationName: e.OperationName,
		Args:          e.VariableValues,
		Context:       context.WithValue(e.Parent, RequestContextKey, e.Context),
	})
}

// graphqlDocumentFor returns the parsed and validated document for query, or
// the parse/validation errors as a result to send back. Only valid documents
// are cached.
func graphqlDocumentFor(schema *graphql.Schema, cache *lookupCache[*graphqlDocument], query string, withSelectors bool) (*graphqlDocument, *graphql.Result) {
	cacheable := len(query) <= graphqlDocumentCacheMaxQueryBytes

	if cacheable {
		if doc, ok := cache.get(query); ok && (doc.selectors != nil || !withSelectors) {
			return doc, nil
		}
	}

	document, err := parser.Parse(parser.ParseParams{
		Source: source.NewSource(&source.Source{
			Body: []byte(query),
			Name: "GraphQL request",
		}),
	})
	if err != nil {
		return nil, &graphql.Result{Errors: gqlerrors.FormatErrors(err)}
	}

	validation := graphql.ValidateDocument(schema, document, nil)
	if !validation.IsValid {
		return nil, &graphql.Result{Errors: validation.Errors}
	}

	doc := &graphqlDocument{ast: document}
	if withSelectors {
		doc.selectors = helper.ExtractGraphqlQuery(helper.ToMap[interface{}](document), 0)
	}

	if cacheable {
		cache.set(query, doc)
	}

	return doc, nil
}
