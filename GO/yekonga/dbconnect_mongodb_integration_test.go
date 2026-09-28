package yekonga

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	mainConfig "github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
)

// Checks the MongoDB write paths, pagination, cancellation and indexes
// against a real server. Point it at a throwaway one; it drops its database:
//
//	YEKONGA_TEST_MONGO_PORT=27999 go test -run TestMongoBackend ./yekonga/
func TestMongoBackendAgainstMongo(t *testing.T) {
	port := os.Getenv("YEKONGA_TEST_MONGO_PORT")
	if port == "" {
		t.Skip("set YEKONGA_TEST_MONGO_PORT to run against a throwaway MongoDB")
	}

	t.Setenv("HOME", t.TempDir())

	var cfg mainConfig.YekongaConfig
	cfg.AppName = "mongo-backend-test"
	cfg.Database.Kind = mainConfig.DBTypeMongodb
	cfg.Database.Host = "127.0.0.1"
	cfg.Database.Port = port
	cfg.Database.DatabaseName = "yekonga_mongo_backend_test"
	cfg.Database.DisableAutoIndexes = true // run explicitly below
	cfg.Database.QueryTimeoutSeconds = 1
	cfg.AuditTrail.Enabled = true

	y := ServerConfig(cfg, DatabaseStructure{
		"outlets": {
			"name": {Kind: "String"},
		},
		"items": {
			"code":     {Kind: "String", Unique: true},
			"region":   {Kind: "String", Index: true},
			"status":   {Kind: "String"},
			"amount":   {Kind: "Number"},
			"price":    {Kind: "Float"},
			"when":     {Kind: "Date"},
			"meta":     {Kind: "Object"},
			"tags":     {Kind: "Array"},
			"outletId": {Kind: "ID", ForeignKey: CollectionFieldConfigForeignKey{Model: "Outlet", Key: "id"}},
		},
	})

	ctx := context.Background()
	db := y.dbConnect.mongodbClient.Database(cfg.Database.DatabaseName)
	db.Drop(ctx)
	t.Cleanup(func() { db.Drop(context.Background()) })

	reRead := func(id interface{}) datatype.DataMap {
		record := y.ModelQuery("Item").FindOne(datatype.DataMap{"id": id})
		if record == nil || *record == nil {
			t.Fatalf("re-read of %v found nothing", id)
		}
		return *record
	}

	newItem := func(code string) datatype.DataMap {
		return datatype.DataMap{
			"code": code, "region": "north", "status": "pending",
			"amount": 5, "price": 2.5, "when": "2026-09-01T10:00:00Z",
			"meta": map[string]interface{}{"a": 1, "b": map[string]interface{}{"c": "x"}},
			"tags": []interface{}{"x", "y"},
		}
	}

	t.Run("create returns what a re-read returns", func(t *testing.T) {
		created, ok := y.ModelQuery("Item").Create(newItem("A1")).(*datatype.DataMap)
		if !ok || created == nil {
			t.Fatalf("Create returned %v", created)
		}

		if want := reRead((*created)["_id"]); !reflect.DeepEqual(*created, want) {
			t.Fatalf("created record differs from a re-read\ncreated: %#v\nre-read: %#v", *created, want)
		}
	})

	t.Run("createMany returns what a re-read returns", func(t *testing.T) {
		query := y.ModelQuery("Item")
		rows, err := query.collection().createMany([]datatype.DataMap{
			*query.formatInputData(newItem("B1"), CreateInputAction),
			*query.formatInputData(newItem("B2"), CreateInputAction),
		})
		if err != nil || rows == nil || len(*rows) != 2 {
			t.Fatalf("createMany returned %v, %v", rows, err)
		}

		for _, record := range *rows {
			if want := reRead(record["_id"]); !reflect.DeepEqual(record, want) {
				t.Fatalf("created record differs from a re-read\ncreated: %#v\nre-read: %#v", record, want)
			}
		}
	})

	t.Run("import returns created and updated records", func(t *testing.T) {
		y.ModelQuery("Item").Create(newItem("F1"))

		y.setTrigger("Item", AfterCreateTriggerAction, nil, nil, func(rc *RequestContext, qc *QueryContext) (interface{}, error) {
			if rows, ok := qc.Data.(*[]datatype.DataMap); ok && rows != nil {
				for _, row := range *rows {
					row["flagged"] = true // edited in place, as a trigger may do
				}
			}
			return nil, nil
		})
		t.Cleanup(func() {
			y.mut.Lock()
			delete(y.triggerFunctions, "Item")
			y.mut.Unlock()
		})

		changed := newItem("F1")
		changed["status"] = "done"

		result := y.ModelQuery("Item").Import([]interface{}{newItem("F2"), newItem("F3"), changed}, []string{"code"}).(map[string]interface{})
		rows, _ := result["data"].([]interface{})

		if result["imported"] != 2 || result["updated"] != 1 || len(rows) != 3 {
			t.Fatalf("Import: imported %v, updated %v, %d records returned; want 2, 1, 3", result["imported"], result["updated"], len(rows))
		}

		for _, row := range rows {
			record := row.(datatype.DataMap)
			stored := reRead(record["_id"])

			if record["code"] == "F1" {
				if !reflect.DeepEqual(record, stored) || stored["status"] != "done" {
					t.Errorf("updated record differs from a re-read\n got: %#v\nwant: %#v", record, stored)
				}
				continue
			}

			if record["flagged"] != true {
				t.Errorf("created record %v is missing the after-create trigger's change", record["code"])
			}
			delete(record, "flagged")
			if !reflect.DeepEqual(record, stored) {
				t.Errorf("created record differs from a re-read\n got: %#v\nwant: %#v", record, stored)
			}
		}
	})

	t.Run("nested import attaches children to their own parent", func(t *testing.T) {
		y.graphqlBuild.initialize()
		items := db.Collection(y.Model("Item").Collection)
		outlets := db.Collection(y.Model("Outlet").Collection)

		runImport := func(query string) {
			result := executeGraphql(graphqlExecution{
				Schema:        &y.graphqlBuild.Schema,
				Cache:         y.graphqlBuild.documentCache,
				RequestString: query,
				RootObject:    map[string]interface{}{},
				Context:       &RequestContext{App: y},
				Parent:        context.Background(),
			})
			if len(result.Errors) > 0 {
				t.Fatalf("%s: %v", query, result.Errors)
			}
		}

		childrenOf := func(name string) []string {
			var outlet bson.M
			if err := outlets.FindOne(ctx, bson.M{"name": name}).Decode(&outlet); err != nil {
				t.Fatalf("outlet %s not created: %v", name, err)
			}

			cursor, _ := items.Find(ctx, bson.M{"outletId": outlet["_id"]})
			var found []bson.M
			cursor.All(ctx, &found)

			var codes []string
			for _, item := range found {
				codes = append(codes, item["code"].(string))
			}
			sort.Strings(codes)
			return codes
		}

		runImport(`mutation { importOutlets(uniqueKeys: [name], input: [
			{name: "N1", items: [{code: "N1-a"}, {code: "N1-b"}]},
			{name: "N2", items: [{code: "N2-a"}]}
		]) { status } }`)

		if got := childrenOf("N1"); !reflect.DeepEqual(got, []string{"N1-a", "N1-b"}) {
			t.Errorf("N1 children = %v, want [N1-a N1-b]", got)
		}
		if got := childrenOf("N2"); !reflect.DeepEqual(got, []string{"N2-a"}) {
			t.Errorf("N2 children = %v, want [N2-a]", got)
		}

		// With no unique keys there's nothing to pair records with inputs by,
		// so no children are attached, rather than the first input's children
		// being attached to every parent.
		runImport(`mutation { importOutlets(input: [
			{name: "M1", items: [{code: "M1-a"}]},
			{name: "M2", items: [{code: "M2-a"}]}
		]) { status } }`)

		if got := childrenOf("M2"); len(got) != 0 {
			t.Errorf("M2 children = %v, want none", got)
		}
	})

	t.Run("update returns the updated record even when it leaves the filter", func(t *testing.T) {
		created := y.ModelQuery("Item").Create(newItem("C1")).(*datatype.DataMap)

		// The filter matches status "pending"; the update changes status.
		updated, ok := y.ModelQuery("Item").Where("code", "C1").Where("status", "pending").
			Update(datatype.DataMap{"status": "done", "amount": 7}, nil).(*datatype.DataMap)
		if !ok || updated == nil {
			t.Fatalf("Update returned %v", updated)
		}

		want := reRead((*created)["_id"])
		if !reflect.DeepEqual(*updated, want) || want["status"] != "done" {
			t.Fatalf("updated record differs from a re-read\nupdated: %#v\nre-read: %#v", *updated, want)
		}

		if result := y.ModelQuery("Item").Where("code", "missing").Update(datatype.DataMap{"status": "x"}, nil); result != nil {
			if record, ok := result.(*datatype.DataMap); !ok || record != nil {
				t.Fatalf("update of nothing returned %#v", result)
			}
		}
	})

	t.Run("audited update records before and after", func(t *testing.T) {
		created := y.ModelQuery("Item").Create(newItem("D1")).(*datatype.DataMap)
		before := reRead((*created)["_id"])

		req := &Request{HttpRequest: httptest.NewRequest("POST", "/", nil), App: y, Context: datatype.Context{}}
		query := y.ModelQuery("Item").SetRequestContext(&RequestContext{App: y, Request: req})
		query.Where("code", "D1").Update(datatype.DataMap{
			"status": "done",
			"meta":   map[string]interface{}{"x": 1, "y": 2, "z": map[string]interface{}{"q": 1, "r": 2, "s": 3}},
		}, nil)

		changes := req.AuditChanges()
		if len(changes) != 1 {
			t.Fatalf("got %d audit changes, want 1", len(changes))
		}

		if !reflect.DeepEqual(changes[0].OldValues, before) {
			t.Errorf("audit old values\n got: %#v\nwant: %#v", changes[0].OldValues, before)
		}
		if after := reRead((*created)["_id"]); !reflect.DeepEqual(changes[0].NewValues, after) {
			t.Errorf("audit new values\n got: %#v\nwant: %#v", changes[0].NewValues, after)
		}
	})

	t.Run("updateMany returns records that leave the filter, and audits them", func(t *testing.T) {
		for _, code := range []string{"G1", "G2", "G3"} {
			item := newItem(code)
			item["region"] = "west"
			y.ModelQuery("Item").Create(item)
		}

		req := &Request{HttpRequest: httptest.NewRequest("POST", "/", nil), App: y, Context: datatype.Context{}}
		query := y.ModelQuery("Item").SetRequestContext(&RequestContext{App: y, Request: req}).OrderBy("code", "asc")

		// The filter matches on region; the update changes region.
		result, _ := query.UpdateMany(datatype.DataMap{"region": "moved"}, datatype.DataMap{"region": "west"}).(*[]datatype.DataMap)
		if result == nil || len(*result) != 3 {
			t.Fatalf("UpdateMany returned %v, want the 3 updated records", result)
		}

		for i, record := range *result {
			if want := []string{"G1", "G2", "G3"}[i]; record["code"] != want || record["region"] != "moved" {
				t.Errorf("record %d: code %v region %v, want %s moved", i, record["code"], record["region"], want)
			}
			if stored := reRead(record["_id"]); !reflect.DeepEqual(record, stored) {
				t.Errorf("record %v differs from a re-read", record["code"])
			}
		}

		changes := req.AuditChanges()
		if len(changes) != 3 {
			t.Fatalf("got %d audit changes, want 3", len(changes))
		}
		for _, change := range changes {
			if change.OldValues["region"] != "west" || change.NewValues["region"] != "moved" {
				t.Errorf("audit change old %v new %v, want west -> moved", change.OldValues["region"], change.NewValues["region"])
			}
		}
	})

	t.Run("paginate", func(t *testing.T) {
		all, _ := db.Collection(y.Model("Item").Collection).CountDocuments(ctx, bson.M{})
		north, _ := db.Collection(y.Model("Item").Collection).CountDocuments(ctx, bson.M{"region": "north", "status": "pending"})

		page := *y.ModelQuery("Item").Take(2).Paginate(nil)
		if page["total"] != all || len(*page["data"].(*[]datatype.DataMap)) != 2 {
			t.Errorf("unfiltered: total %v (want %d), %d rows", page["total"], all, len(*page["data"].(*[]datatype.DataMap)))
		}

		page = *y.ModelQuery("Item").Take(10).Paginate(datatype.DataMap{"region": "north", "status": "pending"})
		if page["total"] != north || int64(len(*page["data"].(*[]datatype.DataMap))) != north {
			t.Errorf("filtered: total %v, %d rows, want %d", page["total"], len(*page["data"].(*[]datatype.DataMap)), north)
		}
	})

	t.Run("distinct count past one cursor batch", func(t *testing.T) {
		collection := db.Collection(y.Model("Item").Collection)
		for i := 0; i < 150; i++ {
			collection.InsertOne(ctx, bson.M{"region": "bulk", "status": fmt.Sprint("s", i)})
		}

		distinct, err := collection.Distinct(ctx, "status", bson.M{"region": "bulk"}).Raw()
		if err != nil {
			t.Fatal(err)
		}
		values, _ := distinct.Values()

		got := y.ModelQuery("Item").DistinctAll([]string{"status"}).Count(datatype.DataMap{"region": "bulk"})
		if got != int64(len(values)) || got != 150 {
			t.Errorf("distinct count = %d, want %d (it used to stop at 101)", got, len(values))
		}
	})

	t.Run("cancelled request stops reads but not writes", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()

		req := &Request{HttpRequest: httptest.NewRequest("GET", "/", nil), App: y, Context: datatype.Context{}, dbCtx: cancelled}
		rc := &RequestContext{App: y, Request: req}

		// None of these may panic on the failed read.
		if rows := y.ModelQuery("Item").SetRequestContext(rc).Find(nil); len(*rows) != 0 {
			t.Errorf("Find on a cancelled request returned %d rows", len(*rows))
		}
		y.ModelQuery("Item").SetRequestContext(rc).Count(nil)
		y.ModelQuery("Item").SetRequestContext(rc).Sum("amount", nil)
		y.ModelQuery("Item").SetRequestContext(rc).Max("amount", nil)
		y.ModelQuery("Item").SetRequestContext(rc).Average("amount", nil)
		y.ModelQuery("Item").SetRequestContext(rc).DistinctAll([]string{"region"}).Count(nil)
		y.ModelQuery("Item").SetRequestContext(rc).Paginate(nil)

		created, ok := y.ModelQuery("Item").SetRequestContext(rc).Create(newItem("E1")).(*datatype.DataMap)
		if !ok || created == nil {
			t.Fatalf("Create on a cancelled request returned %v, want the write to go through", created)
		}
	})

	t.Run("query timeout applies", func(t *testing.T) {
		start := time.Now()
		_, err := db.Collection(y.Model("Item").Collection).Find(ctx, bson.M{"$where": "sleep(3000) || true"})
		if err == nil {
			t.Skip("server-side JavaScript is disabled; can't make a slow query")
		}
		if elapsed := time.Since(start); elapsed > 2500*time.Millisecond {
			t.Errorf("slow query ran %v, timeout is 1s", elapsed)
		}
	})

	t.Run("indexes", func(t *testing.T) {
		// A duplicate code stops the unique index but not the others.
		db.Collection(y.Model("Item").Collection).InsertOne(ctx, bson.M{"code": "A1"})

		y.createModelIndexes()
		y.createModelIndexes() // a second run must be a no-op

		cursor, err := db.Collection(y.Model("Item").Collection).Indexes().List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var specs []bson.M
		cursor.All(ctx, &specs)

		found := map[string]bool{}
		for _, spec := range specs {
			found[spec["name"].(string)] = true
		}

		for _, name := range []string{"_id_", "region_1", "outletId_1"} {
			if !found[name] {
				t.Errorf("missing index %s; have %v", name, found)
			}
		}
		if found["code_1"] {
			t.Error("unique index on code was built despite duplicate values")
		}
	})
}
