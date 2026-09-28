package yekonga

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	mainConfig "github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/mongo"
	"github.com/robertkonga/yekonga-server-go/plugins/mysql"
)

// The SQL backend must behave like the MongoDB one. This builds a server on
// each from the same schema, loads the same records, runs the same queries
// and requires the same results. Point it at throwaway servers; it drops and
// recreates its own databases:
//
//	YEKONGA_TEST_MONGO_PORT=27999 YEKONGA_TEST_MYSQL_PORT=33999 go test -run TestSQLBackend ./yekonga/
func TestSQLBackendMatchesMongo(t *testing.T) {
	mongoPort, mysqlPort := os.Getenv("YEKONGA_TEST_MONGO_PORT"), os.Getenv("YEKONGA_TEST_MYSQL_PORT")
	if mongoPort == "" || mysqlPort == "" {
		t.Skip("set YEKONGA_TEST_MONGO_PORT and YEKONGA_TEST_MYSQL_PORT to run against throwaway servers")
	}

	t.Setenv("HOME", t.TempDir())

	const dbName = "yekonga_sql_parity_test"
	resetMySQLDatabase(t, mysqlPort, dbName)

	servers := map[string]*YekongaData{
		"mongo": newParityServer(t, mainConfig.DBTypeMongodb, mongoPort, dbName),
		"mysql": newParityServer(t, mainConfig.DBTypeMysql, mysqlPort, dbName),
	}
	mongoDB := servers["mongo"].dbConnect.mongodbClient.Database(dbName)
	mongoDB.Drop(context.Background())
	t.Cleanup(func() { mongoDB.Drop(context.Background()) })

	// The same records, with the same ids, on both.
	outletIds := make([]bson.ObjectID, 6)
	for i := range outletIds {
		outletIds[i] = bson.NewObjectID()
	}

	for _, y := range servers {
		for i, id := range outletIds {
			y.ModelQuery("Outlet").Create(datatype.DataMap{
				"id":       id.Hex(),
				"name":     fmt.Sprintf("Outlet %d", i),
				"region":   []string{"north", "south", "east"}[i%3],
				"active":   i%2 == 0,
				"rating":   float64(i) * 1.25,
				"openedAt": fmt.Sprintf("2026-0%d-15T08:30:00Z", i%9+1),
				"tags":     [][]interface{}{{"vip", "late"}, {"new"}, {"vip"}, {}, {"late"}, {"vip", "new", "late"}}[i],
				"meta":     map[string]interface{}{"city": []string{"arusha", "moshi", "dodoma"}[i%3], "size": i},
			})
		}

		for i := 0; i < 30; i++ {
			order := datatype.DataMap{
				"id":        fmt.Sprintf("%024x", i+1),
				"code":      fmt.Sprintf("ORD-%02d", i),
				"amount":    (i * 37) % 100,
				"price":     float64(i) * 1.5,
				"createdAt": time.Date(2026, time.Month(i%12+1), i%28+1, 10, 0, 0, 0, time.UTC),
			}
			if i%7 != 0 {
				order["status"] = []string{"new", "paid", "void"}[i%3]
			}
			if i%4 == 0 {
				order["note"] = []string{"Fast delivery", "call before", "fast DELIVERY please"}[i%3]
			}
			if i%9 != 0 {
				order["outletId"] = outletIds[i%5].Hex()
			}
			if created := y.ModelQuery("Order").Create(order); created == nil {
				t.Fatalf("create order %d failed", i)
			}
		}
	}

	compare := func(t *testing.T, name string, run func(y *YekongaData) interface{}) {
		t.Helper()
		got := map[string]string{}
		for kind, y := range servers {
			encoded, _ := json.Marshal(normalizeForParity(run(y)))
			got[kind] = string(encoded)
		}
		if got["mongo"] != got["mysql"] {
			t.Errorf("%s differs\nmongo: %s\nmysql: %s", name, got["mongo"], got["mysql"])
		}

		// Two empty results would match without proving anything.
		expectEmpty := strings.Contains(name, "unknown field") || strings.Contains(name, "none") || strings.Contains(name, "nothing")
		if empty := got["mongo"] == "null" || got["mongo"] == "[]" || got["mongo"] == "0"; empty != expectEmpty {
			t.Errorf("%s: result empty = %v, want %v (%s)", name, empty, expectEmpty, got["mongo"])
		}
	}

	orderFilters := map[string]datatype.DataMap{
		"equal":                   {"status": map[string]interface{}{"equalTo": "paid"}},
		"not equal (incl. null)":  {"status": map[string]interface{}{"notEqualTo": "paid"}},
		"equal null":              {"status": map[string]interface{}{"equalTo": "null"}},
		"greater than":            {"amount": map[string]interface{}{"greaterThan": 50.0}},
		"float range":             {"price": map[string]interface{}{"greaterThan": 5.0, "lessThanOrEqualTo": 20.0}},
		"date range":              {"createdAt": map[string]interface{}{"greaterThanOrEqualTo": "2026-03-01T00:00:00Z", "lessThan": "2026-06-01T00:00:00Z"}},
		"not less than":           {"amount": map[string]interface{}{"notLessThan": 50.0}},
		"in":                      {"status": map[string]interface{}{"in": []interface{}{"new", "void"}}},
		"not in (incl. null)":     {"status": map[string]interface{}{"notIn": []interface{}{"new"}}},
		"exists":                  {"note": map[string]interface{}{"exists": true}},
		"not exists (id field)":   {"outletId": map[string]interface{}{"exists": false}},
		"regex, case-insensitive": {"note": map[string]interface{}{"matchesRegex": "fast deli"}},
		"id in":                   {"outletId": map[string]interface{}{"in": []interface{}{outletIds[1].Hex(), outletIds[2].Hex()}}},
		"or":                      {"OR": []interface{}{map[string]interface{}{"status": "paid"}, map[string]interface{}{"amount": map[string]interface{}{"lessThan": 10.0}}}},
		"and + or":                {"AND": []interface{}{map[string]interface{}{"amount": map[string]interface{}{"greaterThan": 20.0}}}, "OR": []interface{}{map[string]interface{}{"status": "new"}, map[string]interface{}{"status": "void"}}},
		"nor":                     {"NOR": []interface{}{map[string]interface{}{"status": "paid"}, map[string]interface{}{"status": "new"}}},
		"relation":                {"outlet": map[string]interface{}{"name": map[string]interface{}{"equalTo": "Outlet 1"}}},
		"relation + link key":     {"outletId": map[string]interface{}{"equalTo": outletIds[1].Hex()}, "outlet": map[string]interface{}{"region": map[string]interface{}{"equalTo": "south"}}},
		"unknown field":           {"nonexistent": map[string]interface{}{"equalTo": "x"}},
		"missing field is null":   {"nonexistent": map[string]interface{}{"equalTo": "null"}},
	}

	outletFilters := map[string]datatype.DataMap{
		"array element":   {"tags": map[string]interface{}{"equalTo": "vip"}},
		"array in":        {"tags": map[string]interface{}{"in": []interface{}{"new", "late"}}},
		"array all":       {"tags": map[string]interface{}{"all": []interface{}{"vip", "late"}}},
		"json path":       {"meta.city": map[string]interface{}{"equalTo": "arusha"}},
		"boolean":         {"active": map[string]interface{}{"equalTo": true}},
		"date comparison": {"openedAt": map[string]interface{}{"lessThan": "2026-04-01T00:00:00Z"}},
	}

	t.Run("filters", func(t *testing.T) {
		for name, where := range orderFilters {
			compare(t, "orders: "+name, func(y *YekongaData) interface{} {
				return y.ModelQuery("Order").OrderBy("code", "asc").Find(copyDataMap(where))
			})
		}
		for name, where := range outletFilters {
			compare(t, "outlets: "+name, func(y *YekongaData) interface{} {
				return y.ModelQuery("Outlet").OrderBy("name", "asc").Find(copyDataMap(where))
			})
		}
	})

	t.Run("find one, sort, page", func(t *testing.T) {
		compare(t, "findOne", func(y *YekongaData) interface{} {
			return y.ModelQuery("Order").OrderBy("amount", "desc").FindOne(datatype.DataMap{"status": "paid"})
		})
		compare(t, "findOne none", func(y *YekongaData) interface{} {
			return y.ModelQuery("Order").FindOne(datatype.DataMap{"code": "nope"})
		})
		compare(t, "paginate", func(y *YekongaData) interface{} {
			return y.ModelQuery("Order").OrderBy("code", "asc").Take(7).Page(2).Paginate(nil)
		})
		compare(t, "skip", func(y *YekongaData) interface{} {
			return y.ModelQuery("Order").OrderBy("code", "desc").Skip(25).Find(nil)
		})
	})

	t.Run("counts and aggregates", func(t *testing.T) {
		compare(t, "count", func(y *YekongaData) interface{} {
			return y.ModelQuery("Order").Count(datatype.DataMap{"amount": map[string]interface{}{"greaterThan": 30.0}})
		})
		compare(t, "distinct count", func(y *YekongaData) interface{} {
			return y.ModelQuery("Order").DistinctAll([]string{"status"}).Count(nil)
		})
		compare(t, "sum", func(y *YekongaData) interface{} { return y.ModelQuery("Order").Sum("amount", nil) })
		compare(t, "average int", func(y *YekongaData) interface{} { return y.ModelQuery("Order").Average("amount", nil) })
		compare(t, "average float", func(y *YekongaData) interface{} { return y.ModelQuery("Order").Average("price", nil) })
		compare(t, "max", func(y *YekongaData) interface{} { return y.ModelQuery("Order").Max("amount", nil) })
		compare(t, "min date", func(y *YekongaData) interface{} { return y.ModelQuery("Order").Min("createdAt", nil) })
		compare(t, "sum of nothing", func(y *YekongaData) interface{} {
			return y.ModelQuery("Order").Sum("amount", datatype.DataMap{"code": "nope"})
		})
	})

	t.Run("grouping", func(t *testing.T) {
		compare(t, "group by status with total", func(y *YekongaData) interface{} {
			q := y.ModelQuery("Order")
			q.GroupByRaw("_id", map[string]interface{}{"group": "$status"})
			q.GroupByRaw("total", map[string]interface{}{"$sum": "$amount"})
			return sortedRecords(q.Find(nil))
		})
		compare(t, "group by month with count", func(y *YekongaData) interface{} {
			q := y.ModelQuery("Order")
			q.GroupByRaw("_id", map[string]interface{}{
				"period": map[string]interface{}{"$dateToString": map[string]interface{}{"format": "%Y-%m", "date": "$createdAt"}},
			})
			q.GroupByRaw("total", map[string]interface{}{"$sum": 1})
			return sortedRecords(q.Find(nil))
		})
		compare(t, "group by field list", func(y *YekongaData) interface{} {
			return sortedRecords(y.ModelQuery("Order").GroupBy("status").Find(nil))
		})
	})

	t.Run("graphql relations", func(t *testing.T) {
		queries := []string{
			`{ orders(orderBy: {code: ASC}) { code amount createdAt outlet { name tags meta active } } }`,
			`{ outlets(orderBy: {name: ASC}) { name orders(orderBy: {code: ASC}) { code status } } }`,
		}
		for _, query := range queries {
			compare(t, query, func(y *YekongaData) interface{} {
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
					t.Fatalf("%s: %v", query, result.Errors)
				}
				return result.Data
			})
		}
	})

	t.Run("writes", func(t *testing.T) {
		compare(t, "update first match", func(y *YekongaData) interface{} {
			return y.ModelQuery("Order").OrderBy("code", "desc").Where("status", "paid").
				Update(datatype.DataMap{"note": "updated", "amount": 999}, nil)
		})
		compare(t, "update leaving the filter", func(y *YekongaData) interface{} {
			return y.ModelQuery("Order").Where("code", "ORD-01").Update(datatype.DataMap{"code": "ORD-01b"}, nil)
		})
		compare(t, "update nothing", func(y *YekongaData) interface{} {
			return y.ModelQuery("Order").Where("code", "nope").Update(datatype.DataMap{"note": "x"}, nil)
		})
		compare(t, "update many", func(y *YekongaData) interface{} {
			updated, _ := y.ModelQuery("Order").UpdateMany(datatype.DataMap{"note": "bulk"}, datatype.DataMap{"status": "void"}).(*[]datatype.DataMap)
			return sortedRecords(updated)
		})
		compare(t, "update many leaving the filter", func(y *YekongaData) interface{} {
			// The update changes the field the filter matches on.
			updated, _ := y.ModelQuery("Order").OrderBy("code", "asc").UpdateMany(datatype.DataMap{"status": "archived"}, datatype.DataMap{"status": "void"}).(*[]datatype.DataMap)
			return updated
		})
		compare(t, "update many matching nothing", func(y *YekongaData) interface{} {
			updated, _ := y.ModelQuery("Order").UpdateMany(datatype.DataMap{"status": "x"}, datatype.DataMap{"code": "none"}).(*[]datatype.DataMap)
			return updated
		})
		compare(t, "delete", func(y *YekongaData) interface{} {
			return y.ModelQuery("Order").Delete(datatype.DataMap{"amount": map[string]interface{}{"lessThan": 20.0}})
		})
		compare(t, "import", func(y *YekongaData) interface{} {
			result := y.ModelQuery("Order").Import([]interface{}{
				map[string]interface{}{"id": fmt.Sprintf("%024x", 100), "code": "ORD-NEW", "amount": 5},
				map[string]interface{}{"code": "ORD-05", "amount": 55},
			}, []string{"code"})
			return result
		})
		compare(t, "state after writes", func(y *YekongaData) interface{} {
			return y.ModelQuery("Order").OrderBy("code", "asc").Find(nil)
		})
	})

	mysqlServer := servers["mysql"]

	t.Run("sql: values come back as the framework's types", func(t *testing.T) {
		order := *mysqlServer.ModelQuery("Order").FindOne(datatype.DataMap{"code": "ORD-02"})
		outlet := *mysqlServer.ModelQuery("Outlet").FindOne(datatype.DataMap{"name": "Outlet 0"})

		checks := []struct {
			name  string
			value interface{}
			want  interface{}
		}{
			{"_id", order["_id"], bson.ObjectID{}},
			{"id", order["id"], bson.ObjectID{}},
			{"outletId", order["outletId"], bson.ObjectID{}},
			{"createdAt", order["createdAt"], bson.DateTime(0)},
			{"amount", order["amount"], int64(0)},
			{"price", order["price"], float64(0)},
			{"active", outlet["active"], false},
			{"tags", outlet["tags"], []interface{}{}},
			{"meta", outlet["meta"], map[string]interface{}{}},
		}
		for _, c := range checks {
			if reflect.TypeOf(c.value) != reflect.TypeOf(c.want) {
				t.Errorf("%s is %T, want %T", c.name, c.value, c.want)
			}
		}
	})

	t.Run("sql: unique index and missing columns", func(t *testing.T) {
		client := mysqlServer.sqlClient()
		ctx := context.Background()

		ensureSQLModelIndexes(client, mysqlServer.Model("Order"))
		indexes, err := sqlExistingNames(ctx, client, "SELECT DISTINCT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?", "orders")
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"primary", "uniq_code", "idx_outletid"} {
			if !indexes[name] {
				t.Errorf("missing index %s; have %v", name, indexes)
			}
		}

		if _, err := mysqlServer.ModelQuery("Order").collection().create(datatype.DataMap{"_id": bson.NewObjectID(), "code": "ORD-02"}); err == nil {
			t.Error("a duplicate code was inserted despite the unique index")
		}

		// A column dropped (or added to database.json later) is created at startup.
		if _, err := client.ExecContext(ctx, "ALTER TABLE `outlets` DROP COLUMN `rating`"); err != nil {
			t.Fatal(err)
		}
		if err := ensureSQLTable(ctx, client, mysqlServer.Model("Outlet")); err != nil {
			t.Fatal(err)
		}
		columns, _ := sqlExistingNames(ctx, client, "SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?", "outlets")
		if !columns["rating"] {
			t.Error("missing column was not added back")
		}
	})

	t.Run("sql: the built-in models' tables can be created", func(t *testing.T) {
		const builtinDB = "yekonga_sql_builtin_test"
		resetMySQLDatabase(t, mysqlPort, builtinDB)

		var cfg mainConfig.YekongaConfig
		cfg.AppName = "sql-builtin"
		cfg.Database.Kind = mainConfig.DBTypeMysql
		cfg.Database.Host = "127.0.0.1"
		cfg.Database.Port = mysqlPort
		cfg.Database.DatabaseName = builtinDB
		cfg.Database.Username = "root"
		cfg.Database.DisableAutoMigrate = true // run below, to see each error
		cfg.IsAuthorizationServer = true
		cfg.HasTenant = true
		cfg.HasTenantBilling = true
		cfg.HasTenantCatch = true
		cfg.AuditTrail.Enabled = true

		y := ServerConfig(cfg, DatabaseStructure{})
		client := y.sqlClient()

		for _, model := range y.sortedModels() {
			if err := ensureSQLTable(context.Background(), client, model); err != nil {
				t.Errorf("%s: %v", model.Collection, err)
			}
			ensureSQLModelIndexes(client, model)
		}

		// A tenant lookup by domain, as every request does with tenants on.
		created := y.ModelQuery("Tenant").Create(datatype.DataMap{"name": "Acme", "domain": "acme.example.com"})
		if created == nil {
			t.Fatal("could not create a tenant")
		}
		if tenant, _ := y.tenantByHost("acme.example.com"); tenant == nil || (*tenant)["name"] != "Acme" {
			t.Errorf("tenant lookup by domain = %v", tenant)
		}
		t.Logf("%d built-in tables created", len(y.models))
	})

	t.Run("sql: delete needs a filter", func(t *testing.T) {
		if _, err := mysqlServer.ModelQuery("Order").collection().delete(); err == nil {
			t.Error("an unfiltered delete was allowed")
		}
	})
}

func resetMySQLDatabase(t *testing.T, port string, name string) {
	cfg := mysql.NewConfig()
	cfg.User = "root"
	cfg.Net = "tcp"
	cfg.Addr = "127.0.0.1:" + port

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, statement := range []string{"DROP DATABASE IF EXISTS " + quoteIdent(name), "CREATE DATABASE " + quoteIdent(name)} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	t.Cleanup(func() {
		if db, err := sql.Open("mysql", cfg.FormatDSN()); err == nil {
			db.Exec("DROP DATABASE IF EXISTS " + quoteIdent(name))
			db.Close()
		}
	})
}

func newParityServer(t *testing.T, kind mainConfig.DatabaseType, port string, dbName string) *YekongaData {
	var cfg mainConfig.YekongaConfig
	cfg.AppName = "sql-parity-" + string(kind)
	cfg.Database.Kind = kind
	cfg.Database.Host = "127.0.0.1"
	cfg.Database.Port = port
	cfg.Database.DatabaseName = dbName
	cfg.Database.DisableAutoIndexes = true // checked explicitly
	if kind == mainConfig.DBTypeMysql {
		cfg.Database.Username = "root"
	}

	y := ServerConfig(cfg, DatabaseStructure{
		"outlets": {
			"name":     {Kind: "String"},
			"region":   {Kind: "String", Index: true},
			"active":   {Kind: "Boolean"},
			"rating":   {Kind: "Float"},
			"openedAt": {Kind: "Date"},
			"tags":     {Kind: "Array"},
			"meta":     {Kind: "Object"},
		},
		"orders": {
			"code":      {Kind: "String", Unique: true},
			"amount":    {Kind: "Number"},
			"price":     {Kind: "Float"},
			"status":    {Kind: "String"},
			"note":      {Kind: "String"},
			"createdAt": {Kind: "Date"},
			"outletId":  {Kind: "ID", ForeignKey: CollectionFieldConfigForeignKey{Model: "Outlet", Key: "id"}},
		},
	})
	y.graphqlBuild.initialize()

	return y
}

func copyDataMap(m datatype.DataMap) datatype.DataMap {
	var out datatype.DataMap
	encoded, _ := json.Marshal(m)
	json.Unmarshal(encoded, &out)
	return out
}

// sortedRecords orders records that come back in no particular order.
func sortedRecords(records *[]datatype.DataMap) []interface{} {
	if records == nil {
		return nil
	}

	list := make([]interface{}, len(*records))
	for i, r := range *records {
		list[i] = normalizeForParity(r)
	}
	sort.Slice(list, func(i, j int) bool {
		a, _ := json.Marshal(list[i])
		b, _ := json.Marshal(list[j])
		return string(a) < string(b)
	})

	return list
}

// normalizeForParity removes differences that are only about how each
// driver represents a value: bson.D vs map, int32 vs int64, bson.DateTime vs
// time.Time, and so on.
func normalizeForParity(value interface{}) interface{} {
	switch v := value.(type) {
	case nil:
		return nil
	case bson.ObjectID:
		return v.Hex()
	case time.Time:
		return v.UTC().Format("2006-01-02T15:04:05.000Z")
	case bson.DateTime:
		return v.Time().UTC().Format("2006-01-02T15:04:05.000Z")
	case *datatype.DataMap:
		if v == nil || *v == nil {
			return nil
		}
		return normalizeForParity(*v)
	case *[]datatype.DataMap:
		if v == nil {
			return nil
		}
		return normalizeForParity(*v)
	case *mongo.DeleteResult:
		return map[string]interface{}{"DeletedCount": float64(v.DeletedCount)}
	case bson.D:
		out := map[string]interface{}{}
		for _, e := range v {
			out[e.Key] = normalizeForParity(e.Value)
		}
		return out
	case bool, string:
		return v
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return reflect.ValueOf(v).Convert(reflect.TypeOf(float64(0))).Float()
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Map:
		out := map[string]interface{}{}
		for _, key := range rv.MapKeys() {
			out[fmt.Sprint(key.Interface())] = normalizeForParity(rv.MapIndex(key).Interface())
		}
		return out
	case reflect.Slice, reflect.Array:
		out := make([]interface{}, rv.Len())
		for i := range out {
			out[i] = normalizeForParity(rv.Index(i).Interface())
		}
		return out
	case reflect.Ptr:
		if rv.IsNil() {
			return nil
		}
		return normalizeForParity(rv.Elem().Interface())
	}

	return strings.TrimSpace(fmt.Sprint(value))
}
