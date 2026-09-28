package yekonga

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	mainConfig "github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/mongo"
)

// Runs GraphQL relation queries against a real MongoDB with relation batching
// on and off, and checks both give the same data while batching runs fewer
// queries. Point it at a throwaway server; it drops its own database:
//
//	YEKONGA_TEST_MONGO_PORT=27999 go test -run TestRelationLoader ./yekonga/
func TestRelationLoaderAgainstMongo(t *testing.T) {
	port := os.Getenv("YEKONGA_TEST_MONGO_PORT")
	if port == "" {
		t.Skip("set YEKONGA_TEST_MONGO_PORT to run against a throwaway MongoDB")
	}

	t.Setenv("HOME", t.TempDir()) // ServerConfig creates ~/.yekonga-server/<app>

	var cfg mainConfig.YekongaConfig
	cfg.AppName = "relation-loader-test"
	cfg.Database.Kind = mainConfig.DBTypeMongodb
	cfg.Database.Host = "127.0.0.1"
	cfg.Database.Port = port
	cfg.Database.DatabaseName = "yekonga_relation_loader_test"
	cfg.Database.DisableAutoIndexes = true // index builds would show up in the query counts

	y := ServerConfig(cfg, DatabaseStructure{
		"outlets": {
			"name":   {Kind: "String"},
			"region": {Kind: "String"},
		},
		"orders": {
			"code":     {Kind: "String"},
			"amount":   {Kind: "Number"},
			"outletId": {Kind: "ID", ForeignKey: CollectionFieldConfigForeignKey{Model: "Outlet", Key: "id"}},
		},
	})
	y.graphqlBuild.initialize()

	ctx := context.Background()
	db := y.dbConnect.mongodbClient.Database(cfg.Database.DatabaseName)
	db.Drop(ctx)
	t.Cleanup(func() { db.Drop(context.Background()) })

	outletCollection := y.Model("Outlet").Collection
	orderCollection := y.Model("Order").Collection

	var outletIds []bson.ObjectID
	for i := 0; i < 6; i++ {
		id := bson.NewObjectID()
		outletIds = append(outletIds, id)
		region := "north"
		if i%2 == 1 {
			region = "south"
		}
		db.Collection(outletCollection).InsertOne(ctx, bson.M{"_id": id, "name": fmt.Sprintf("Outlet %d", i), "region": region})
	}

	for i := 0; i < 40; i++ {
		order := bson.M{"_id": bson.NewObjectID(), "code": fmt.Sprintf("ORD-%02d", i), "amount": (i * 37) % 100}
		switch {
		case i%10 == 9: // no outlet
		case i%10 == 8: // an outlet that doesn't exist
			order["outletId"] = bson.NewObjectID()
		default: // the last outlet has no orders
			order["outletId"] = outletIds[i%5]
		}
		db.Collection(orderCollection).InsertOne(ctx, order)
	}

	var outletOrders string
	for name := range y.Model("Outlet").ChildrenFields {
		outletOrders = name
	}
	if outletOrders == "" || y.Model("Order").ParentFields["outlet"].ModelName == "" {
		t.Fatalf("unexpected relation fields: outlet children %v, order parents %v", y.Model("Outlet").ChildrenFields, y.Model("Order").ParentFields)
	}

	queries := []struct {
		name       string
		query      string
		maxBatched int // queries against outlets/orders with batching on
	}{
		{"to-one", `{ orders { code amount outlet { name region } } }`, 2},
		{"to-many", `{ outlets { name ` + outletOrders + ` { code amount } } }`, 2},
		{"to-many with where and order", `{ outlets(where: {region: {equalTo: "north"}}) { name ` + outletOrders + `(where: {amount: {notEqualTo: 11}}, orderBy: {amount: DESC}) { code amount } } }`, 2},
		{"nested", `{ outlets { name ` + outletOrders + ` { code outlet { name } } } }`, 3},
		{"limit stays per row", `{ outlets { name ` + outletOrders + `(limit: 2) { code } } }`, 7},
	}

	run := func(query string, batched bool) (string, int64) {
		batchRelationLookups = batched
		defer func() { batchRelationLookups = true }()

		resetProfiler(t, db)

		req := &Request{HttpRequest: httptest.NewRequest("POST", "/graphql", nil), App: y, Context: datatype.Context{}}
		req.SetContext(string(ClientPayloadKey), ClientPayload{Host: "localhost", Origin: "http://localhost"})

		result := executeGraphql(graphqlExecution{
			Schema:        &y.graphqlBuild.Schema,
			Cache:         y.graphqlBuild.documentCache,
			RequestString: query,
			RootObject:    map[string]interface{}{},
			Context:       &RequestContext{App: y, Request: req, Client: req.Client()},
			Parent:        context.Background(),
		})
		if len(result.Errors) > 0 {
			t.Fatalf("query %s: %v", query, result.Errors)
		}

		count, err := db.Collection("system.profile").CountDocuments(ctx, bson.M{
			"ns": bson.M{"$in": []string{db.Name() + "." + outletCollection, db.Name() + "." + orderCollection}},
			"op": bson.M{"$in": []string{"query", "command"}},
		})
		if err != nil {
			t.Fatalf("reading profiler: %v", err)
		}

		return helper.ToJson(result.Data), count
	}

	for _, q := range queries {
		t.Run(q.name, func(t *testing.T) {
			perRow, perRowQueries := run(q.query, false)
			batched, batchedQueries := run(q.query, true)

			if batched != perRow {
				t.Fatalf("batched result differs from per-row result\nper-row: %s\nbatched: %s", perRow, batched)
			}

			if !strings.Contains(batched, "ORD-") && !strings.Contains(batched, "Outlet") {
				t.Fatalf("query returned no data: %s", batched)
			}

			if batchedQueries > int64(q.maxBatched) {
				t.Errorf("batched run made %d queries, want at most %d", batchedQueries, q.maxBatched)
			}

			t.Logf("queries: per-row %d, batched %d", perRowQueries, batchedQueries)
		})
	}

	// rows runs a query and returns the list under field.
	rows := func(t *testing.T, query string, field string) []interface{} {
		t.Helper()
		out, _ := run(query, true)
		list, _ := helper.ToMap[interface{}](out)[field].([]interface{})
		return list
	}

	t.Run("numeric range filter", func(t *testing.T) {
		got := rows(t, `{ orders(where: {amount: {greaterThan: 50}}) { amount } }`, "orders")
		want, _ := db.Collection(orderCollection).CountDocuments(ctx, bson.M{"amount": bson.M{"$gt": 50}})

		if int64(len(got)) != want || want == 0 {
			t.Fatalf("got %d orders, want %d", len(got), want)
		}
		for _, row := range got {
			if amount := helper.ToFloat(row.(map[string]interface{})["amount"]); amount <= 50 {
				t.Errorf("order with amount %v passed greaterThan 50", amount)
			}
		}
	})

	t.Run("exists false", func(t *testing.T) {
		got := rows(t, `{ orders(where: {outletId: {exists: false}}) { code } }`, "orders")
		if len(got) != 4 { // ORD-09, 19, 29, 39 have no outlet
			t.Fatalf("got %d orders without an outlet, want 4: %v", len(got), got)
		}
	})

	t.Run("relation filter and direct filter on the same key both apply", func(t *testing.T) {
		matching := rows(t, `{ orders(where: {outletId: {equalTo: "`+outletIds[1].Hex()+`"}, outlet: {name: {equalTo: "Outlet 1"}}}) { code } }`, "orders")
		if len(matching) != 8 {
			t.Errorf("outletId = outlet 1 and outlet name = Outlet 1: got %d orders, want 8", len(matching))
		}

		contradicting := rows(t, `{ orders(where: {outletId: {equalTo: "`+outletIds[0].Hex()+`"}, outlet: {name: {equalTo: "Outlet 1"}}}) { code } }`, "orders")
		if len(contradicting) != 0 {
			t.Errorf("outletId = outlet 0 and outlet name = Outlet 1: got %d orders, want 0", len(contradicting))
		}
	})

	t.Run("relation summary counts only related records", func(t *testing.T) {
		summaryField := ""
		for name := range y.graphqlBuild.QueryTypes["Outlet"].Fields() {
			if strings.HasSuffix(name, "Summary") && name != "outletSummary" {
				summaryField = name
			}
		}

		got := rows(t, `{ outlets { name `+summaryField+` { count } } }`, "outlets")
		for _, row := range got {
			outlet := row.(map[string]interface{})
			count := helper.ToFloat(outlet[summaryField].(map[string]interface{})["count"])
			want, _ := db.Collection(orderCollection).CountDocuments(ctx, bson.M{"outletId": outletIdByName(outletIds, outlet["name"])})
			if int64(count) != want {
				t.Errorf("%v: %s.count = %v, want %d", outlet["name"], summaryField, count, want)
			}
		}
	})

	// Triggers must still run once per row, see a single record for a to-one
	// relation, and not see each other's edits when rows share a record.
	t.Run("triggers run per row", func(t *testing.T) {
		var afterCalls, beforeCalls, singleCalls int

		y.setTrigger("Order", BeforeFindTriggerAction, nil, nil, func(rc *RequestContext, qc *QueryContext) (interface{}, error) {
			beforeCalls++
			return datatype.DataMap{"code": map[string]interface{}{"notEqualTo": "ORD-00"}}, nil
		})
		y.setTrigger("Outlet", AfterFindTriggerAction, nil, nil, func(rc *RequestContext, qc *QueryContext) (interface{}, error) {
			afterCalls++
			record, ok := qc.Data.(*datatype.DataMap)
			if !ok {
				return nil, nil // the root outlets list
			}
			singleCalls++
			if record != nil && *record != nil {
				(*record)["name"] = fmt.Sprint((*record)["name"], "!") // edits the shared record in place
			}
			return nil, nil
		})
		t.Cleanup(func() {
			y.mut.Lock()
			delete(y.triggerFunctions, "Order")
			delete(y.triggerFunctions, "Outlet")
			y.mut.Unlock()
		})

		query := `{ outlets { name ` + outletOrders + ` { code outlet { name } } } }`

		beforeCalls, afterCalls, singleCalls = 0, 0, 0
		perRow, _ := run(query, false)
		perRowBefore, perRowAfter, perRowSingle := beforeCalls, afterCalls, singleCalls

		beforeCalls, afterCalls, singleCalls = 0, 0, 0
		batched, _ := run(query, true)

		if batched != perRow {
			t.Fatalf("batched result differs from per-row result\nper-row: %s\nbatched: %s", perRow, batched)
		}

		if beforeCalls != perRowBefore || afterCalls != perRowAfter {
			t.Errorf("trigger calls (before, after): per-row (%d, %d), batched (%d, %d)", perRowBefore, perRowAfter, beforeCalls, afterCalls)
		}

		if singleCalls != perRowSingle || singleCalls == 0 {
			t.Errorf("to-one AfterFind calls with a single record: per-row %d, batched %d", perRowSingle, singleCalls)
		}

		if strings.Contains(batched, "ORD-00") || strings.Contains(batched, "!!") {
			t.Errorf("trigger effects wrong: %s", batched)
		}

		t.Logf("trigger calls: before %d, after %d", beforeCalls, afterCalls)
	})
}

func resetProfiler(t *testing.T, db *mongo.Database) {
	ctx := context.Background()
	for _, cmd := range []bson.D{{{Key: "profile", Value: 0}}} {
		if err := db.RunCommand(ctx, cmd).Err(); err != nil {
			t.Fatalf("profiler: %v", err)
		}
	}

	db.Collection("system.profile").Drop(ctx)

	if err := db.RunCommand(ctx, bson.D{{Key: "profile", Value: 2}}).Err(); err != nil {
		t.Fatalf("profiler: %v", err)
	}
}

func outletIdByName(ids []bson.ObjectID, name interface{}) bson.ObjectID {
	var i int
	fmt.Sscanf(fmt.Sprint(name), "Outlet %d", &i)
	return ids[i]
}
