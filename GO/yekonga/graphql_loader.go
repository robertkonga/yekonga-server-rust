package yekonga

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/plugins/graphql"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
)

// Relation fields (order.outlet, outlet.orders) used to run one query per
// parent row: a list of 50 orders asking for their outlet ran 50 queries.
//
// The loader batches only the database fetch. Each row still builds its own
// query and runs its own before-find and after-find triggers, with itself as
// the parent, exactly as before. Rows whose queries are identical apart from
// the relation key share one "$in" query. This works because the GraphQL
// executor resolves a list's fields for every row before it calls any of the
// thunks they return.

// batchRelationLookups turns batching off, e.g. to compare results with
// per-row lookups in tests.
var batchRelationLookups = true

// relationLoader batches relation lookups for one GraphQL request.
type relationLoader struct {
	mut     sync.Mutex
	pending map[string]*relationBatch
}

type relationBatch struct {
	query  *DataModelQuery // the first row's prepared query: its filter and order apply to all
	field  string          // the where key that holds the relation value, e.g. "_id" or "outletId"
	keys   []interface{}
	seen   map[string]bool
	loaded bool
	rows   map[string][]datatype.DataMap
}

func (c *RequestContext) relationLoader() *relationLoader {
	c.mut.Lock()
	defer c.mut.Unlock()

	if c.relations == nil {
		c.relations = &relationLoader{pending: make(map[string]*relationBatch)}
	}

	return c.relations
}

// add queues value for the batch of queries shaped like q, and returns that
// batch.
func (l *relationLoader) add(q *DataModelQuery, field string, value interface{}) *relationBatch {
	fingerprint := relationFingerprint(q, field)

	l.mut.Lock()
	defer l.mut.Unlock()

	batch := l.pending[fingerprint]
	if batch == nil || batch.loaded {
		batch = &relationBatch{query: q, field: field, seen: make(map[string]bool)}
		l.pending[fingerprint] = batch
	}

	key, _ := relationKey(value)
	if !batch.seen[key] {
		batch.seen[key] = true
		batch.keys = append(batch.keys, value)
	}

	return batch
}

// rowsFor returns the records matching value, loading the whole batch on the
// first call. Each caller gets its own copies, since after-find triggers may
// change them and several rows can share a related record.
func (l *relationLoader) rowsFor(batch *relationBatch, value interface{}) []datatype.DataMap {
	l.mut.Lock()
	defer l.mut.Unlock()

	if !batch.loaded {
		batch.load()
	}

	key, _ := relationKey(value)
	matches := batch.rows[key]

	rows := make([]datatype.DataMap, len(matches))
	for i, match := range matches {
		row := make(datatype.DataMap, len(match))
		for k, v := range match {
			row[k] = v
		}
		rows[i] = row
	}

	return rows
}

func (b *relationBatch) load() {
	where := make(datatype.DataMap, len(b.query.where))
	for k, v := range b.query.where {
		where[k] = v
	}
	where[b.field] = map[string]interface{}{"in": b.keys}

	// The tenant filter is already in the prepared where, and triggers ran per
	// row, so this goes straight to the collection.
	query := &DataModelQuery{
		Model:          b.query.Model,
		RequestContext: b.query.RequestContext,
		where:          where,
		orderBy:        b.query.orderBy,
	}

	b.rows = make(map[string][]datatype.DataMap)
	if found := query.collection().find(); found != nil {
		for _, row := range *found {
			for _, key := range relationRowKeys(row[b.field]) {
				b.rows[key] = append(b.rows[key], row)
			}
		}
	}

	b.loaded = true
}

// relationFingerprint identifies queries that can share a batch: same model,
// relation field, filter (apart from the relation value) and order. %#v
// includes types, so an ObjectID and a string never share a batch.
func relationFingerprint(q *DataModelQuery, field string) string {
	where := make(map[string]interface{}, len(q.where))
	for k, v := range q.where {
		if k != field {
			where[k] = v
		}
	}

	return fmt.Sprintf("%s|%s|%#v|%#v", q.Model.Name, field, where, q.orderBy)
}

// relationKey maps a relation value to the key it's grouped under. Numbers
// share one form because MongoDB matches 5 and 5.0 as equal; strings and
// ObjectIDs stay distinct because MongoDB doesn't match them to each other.
func relationKey(value interface{}) (string, bool) {
	switch v := value.(type) {
	case bson.ObjectID:
		return "o:" + v.Hex(), true
	case string:
		return "s:" + v, true
	case int:
		return "n:" + strconv.FormatFloat(float64(v), 'g', -1, 64), true
	case int32:
		return "n:" + strconv.FormatFloat(float64(v), 'g', -1, 64), true
	case int64:
		return "n:" + strconv.FormatFloat(float64(v), 'g', -1, 64), true
	case float64:
		return "n:" + strconv.FormatFloat(v, 'g', -1, 64), true
	}

	return "", false
}

// relationRowKeys returns the keys a fetched record belongs under. An array
// field matches every value it contains, as it would in a per-row query.
func relationRowKeys(value interface{}) []string {
	var items []interface{}

	switch v := value.(type) {
	case bson.A:
		items = v
	case []interface{}:
		items = v
	default:
		items = []interface{}{value}
	}

	keys := make([]string, 0, len(items))
	for _, item := range items {
		if key, ok := relationKey(item); ok {
			keys = append(keys, key)
		}
	}

	return keys
}

// relationWhereKey is the where key setModelParams filters a relation on.
func relationWhereKey(model *DataModelQuery, foreignKey string, targetKey string, single bool) string {
	key := targetKey
	if !single && helper.Contains(model.Model.ParentKeys, foreignKey) {
		key = foreignKey
	}

	if key == "id" {
		key = "_id"
	}

	return key
}

// isRelationField reports whether a resolver call is a relation field on a
// parent row, rather than a root query.
func isRelationField(p *graphql.ResolveParams, foreignKey string) bool {
	if foreignKey == "" {
		return false
	}

	switch p.Source.(type) {
	case map[string]interface{}, datatype.DataMap:
		return true
	}

	return false
}

// resolveRelation resolves a relation field for one parent row, after
// setModelParams has built its query. single is true for a to-one relation
// (order.outlet) and false for a to-many one (outlet.orders).
func (g *GraphqlAutoBuild) resolveRelation(model *DataModelQuery, p *graphql.ResolveParams, foreignKey string, targetKey string, single bool) (interface{}, error) {
	if !model.prepareFind(nil) {
		if single {
			return nil, nil
		}
		return []datatype.DataMap{}, nil
	}

	finish := func(rows []datatype.DataMap) (interface{}, error) {
		if single {
			var record datatype.DataMap
			if len(rows) > 0 {
				record = rows[0]
			}
			return g.relationRecordOutput(model, model.finishFindOne(&record), p, foreignKey, targetKey), nil
		}

		data := model.finishFind(&rows)
		g.loadRelatedData(data, model, p, foreignKey, targetKey)
		return *data, nil
	}

	field := relationWhereKey(model, foreignKey, targetKey, single)
	value, hasValue := model.where[field]
	_, keyable := relationKey(value)

	batchable := batchRelationLookups && hasValue && keyable &&
		model.RequestContext != nil &&
		model.Model.DatabaseType == config.DBTypeMongodb &&
		model.limit == 0 && model.page == 0 && model.skip == 0 &&
		len(model.groupBy) == 0 && len(model.groupByRaw) == 0 && len(model.distinct) == 0

	if !batchable {
		// Same as FindOne/Find, whose before-find triggers already ran above.
		if single {
			return g.relationRecordOutput(model, model.finishFindOne(model.collection().findOne()), p, foreignKey, targetKey), nil
		}

		data := model.finishFind(model.collection().find())
		g.loadRelatedData(data, model, p, foreignKey, targetKey)
		return *data, nil
	}

	loader := model.RequestContext.relationLoader()
	batch := loader.add(model, field, value)

	return func() (interface{}, error) {
		return finish(loader.rowsFor(batch, value))
	}, nil
}

// relationRecordOutput shapes a to-one result as the single-record resolver
// does.
func (g *GraphqlAutoBuild) relationRecordOutput(model *DataModelQuery, data *datatype.DataMap, p *graphql.ResolveParams, foreignKey string, targetKey string) interface{} {
	if helper.IsNotEmpty(data) {
		dataMap := g.formateOutputData(model, *data, foreignKey, targetKey)
		dataMap["_params"] = p.Args

		return dataMap
	}

	return nil
}
