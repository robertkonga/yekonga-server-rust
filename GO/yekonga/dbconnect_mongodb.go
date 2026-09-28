package yekonga

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/console"
	"github.com/robertkonga/yekonga-server-go/helper/logger"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/mongo"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/mongo/options"
)

type mongodbConnection struct {
	query  *DataModelQuery
	ctx    *context.Context
	mut    sync.RWMutex
	client *mongo.Client
	filter *datatype.DataMap // built once by where(); a connection serves a single query
}

func (con *mongodbConnection) collection() *mongo.Collection {

	// Get collection
	collection := con.client.
		Database(con.query.Model.Config.Database.DatabaseName).
		Collection(con.query.Model.Collection)

	return collection
}

// readCtx is the context for reads: cancelled if the request's client
// disconnects (see Request.DatabaseContext).
func (con *mongodbConnection) readCtx() context.Context {
	if con.ctx == nil || *con.ctx == nil {
		return context.Background()
	}

	return *con.ctx
}

// writeCtx is the context for writes and cursor cleanup. It ignores the
// client disconnecting, so a handler's writes aren't cut off half-way; the
// configured query timeout still applies.
func (con *mongodbConnection) writeCtx() context.Context {
	return context.WithoutCancel(con.readCtx())
}

func (con *mongodbConnection) findOne() *datatype.DataMap {
	var result datatype.DataMap

	opts := options.FindOne()
	if con.hasOrderBy() {
		opts = opts.SetSort(con.orderBy())
	}
	localWhere := con.where()

	// if con.query.Model.Name == "Certificate" {
	// 	console.Log("mongodbConnection.findOne", "Cursor: %v", localWhere)
	// }

	res := con.collection().FindOne(con.readCtx(), localWhere, opts)
	err := res.Decode(&result)
	if err != nil {
		// logger.Error("mongodbConnection.findOne", err.Error())
	} else if result != nil {
		result["id"] = result["_id"]
		result["_collection"] = con.query.Model.Collection
		result["_model"] = con.query.Model.Name
	}

	return &result
}

func (con *mongodbConnection) findAll() *[]datatype.DataMap {
	return con.find()
}

func (con *mongodbConnection) find() *[]datatype.DataMap {
	var cursor *mongo.Cursor
	var err error

	if con.hasGroup() {
		opts := options.Aggregate()
		// Aggregation pipeline to sum the "amount" field
		pipeline := mongo.Pipeline{}

		if con.where() != nil {
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: con.where()}})
		}

		pipeline = append(pipeline, bson.D{{Key: "$group", Value: con.groupBy()}})

		if con.hasOrderBy() {
			pipeline = append(pipeline, bson.D{{Key: "$sort", Value: con.orderBy()}})
		}

		if con.hasProjection() {
			pipeline = append(pipeline, bson.D{{Key: "$project", Value: con.projection()}})
		}

		if con.skip() > 0 {
			pipeline = append(pipeline, bson.D{{Key: "$skip", Value: int64(con.skip())}})
		}

		if con.limit() > 0 {
			pipeline = append(pipeline, bson.D{{Key: "$limit", Value: int64(con.limit())}})
		}

		// console.Log("mongodbConnection.find", "Pipeline: %v", pipeline)

		cursor, err = con.collection().Aggregate(con.readCtx(), pipeline, opts)
		if err != nil {
			logger.Error("mongodbConnection.find 0", err.Error())
		}
	} else {

		// Find with limit and skip
		opts := options.Find()

		if con.limit() > 0 {
			opts = opts.SetLimit(int64(con.limit()))
		}

		if con.skip() > 0 {
			opts = opts.SetSkip(int64(con.skip()))
		}

		if con.hasOrderBy() {
			opts = opts.SetSort(con.orderBy())
		}

		cursor, err = con.collection().Find(con.readCtx(), con.where(), opts)
	}

	if err != nil {
		logger.Error("mongodbConnection.find", err.Error())
		return &[]datatype.DataMap{}
	}

	defer cursor.Close(con.writeCtx())

	// if con.query.Model.Collection == "user_verifications" {
	// 	console.Log("mongodbConnection.find", "Cursor: %v", con.where())
	// }

	if err := cursor.Err(); err != nil {
		// logger.Error("mongodbConnection.find", err.Error())
	} else {
		result := make([]datatype.DataMap, 0, cursor.RemainingBatchLength())

		// cursor.All(context.TODO(), &result)
		for cursor.Next(con.readCtx()) {
			// To decode into a struct, use cursor.Decode()
			var data datatype.DataMap
			err := cursor.Decode(&data)
			if err != nil {
				logger.Error("mongodbConnection.find 3", err.Error())
			} else if data != nil {
				data["id"] = data["_id"]
				data["_collection"] = con.query.Model.Collection
				data["_model"] = con.query.Model.Name

				if con.hasGroup() {
					if v, ok := data["_id"].(bson.D); ok {
						for _, vi := range v {
							data[vi.Key] = vi.Value
						}
					}
				}
				result = append(result, data)
			}
		}
		// if con.query.Model.Collection == "outlets" {
		// 	console.Log("mongodbConnection.find", "Found %d documents", result)
		// }

		// Stopped part-way (cancelled or timed out): don't pass off a
		// truncated list as the full result.
		if err := cursor.Err(); err != nil {
			logger.Error("mongodbConnection.find", err.Error())
			return &[]datatype.DataMap{}
		}

		return &result
	}

	return &[]datatype.DataMap{}
}

func (con *mongodbConnection) pagination() *datatype.DataMap {
	var lastPage int64
	perPage := con.limit()
	currentPage := con.page()
	from := (perPage * (currentPage - 1)) + 1
	to := perPage * (currentPage)

	con.query.Take(perPage)

	// The count and the page don't depend on each other, so they run at the
	// same time. The filter is built first: it's shared and may run queries.
	con.where()

	var total int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			// A panic here would take down the process, not just the request.
			if r := recover(); r != nil {
				logger.Error("mongodbConnection.pagination count", r)
			}
		}()

		total = con.count()
	}()

	data := con.find()
	wg.Wait()

	remainder := total % int64(perPage)

	if remainder == 0 {
		lastPage = (total) / int64(perPage)
	} else {
		lastPage = (total + (int64(perPage) - total%int64(perPage))) / int64(perPage)
	}

	result := datatype.DataMap{
		"total":       total,
		"perPage":     perPage,
		"currentPage": currentPage,
		"lastPage":    lastPage,
		"from":        from,
		"to":          to,
		"data":        data,
	}

	return &result
}

func (con *mongodbConnection) summary() *datatype.DataMap {

	result := datatype.DataMap{
		"count": 0,
		"sum":   0,
		"max":   0,
		"min":   0,
		"graph": datatype.DataMap{},
	}

	return &result
}

func (con *mongodbConnection) count() int64 {
	var cursor int64
	var err error

	if len(con.query.distinct) > 0 {
		opts := options.Aggregate()

		// Aggregation pipeline to sum the "amount" field
		groupId := bson.M{}
		for _, k := range con.query.distinct {
			groupId[k] = "$" + k
		}
		// Count the distinct groups on the server. (The size of the cursor's
		// first batch, used before, caps out at 101.)
		pipeline := mongo.Pipeline{
			{{Key: "$match", Value: con.where()}},
			{{Key: "$group", Value: bson.M{"_id": groupId}}},
			{{Key: "$count", Value: "aggregateValue"}},
		}

		cursorResult, err := con.collection().Aggregate(con.readCtx(), pipeline, opts)
		if err != nil {
			logger.Error("mongodbConnection.count 1", err.Error())
			return 0
		}
		defer cursorResult.Close(con.writeCtx())

		var counted struct {
			AggregateValue int64 `bson:"aggregateValue"`
		}
		if cursorResult.Next(con.readCtx()) {
			if err := cursorResult.Decode(&counted); err != nil {
				logger.Error("mongodbConnection.count 2", err.Error())
			}
		}

		cursor = counted.AggregateValue
	} else {
		if len(*con.where()) == 0 {
			// Nothing to filter on: read the count from collection metadata
			// instead of scanning every record.
			cursor, err = con.collection().EstimatedDocumentCount(con.readCtx())
		} else {
			cursor, err = con.collection().CountDocuments(con.readCtx(), con.where())
		}
		if err != nil {
			logger.Error("mongodbConnection.count", err.Error())
		}
	}

	return cursor
}

func (con *mongodbConnection) sum(key string) float64 {
	// Find with limit and skip
	opts := options.Aggregate()

	// Aggregation pipeline to sum the "amount" field
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: con.where()}},
		{{Key: "$group", Value: bson.M{"_id": nil, "aggregateValue": bson.M{"$sum": "$" + key}}}},
	}

	cursor, err := con.collection().Aggregate(con.readCtx(), pipeline, opts)
	if err != nil {
		logger.Error("mongodbConnection.sum 1", err.Error())
		return 0
	}
	defer cursor.Close(con.writeCtx())

	// Retrieve the result
	var result struct {
		AggregateValue float64 `bson:"aggregateValue"`
	}
	if cursor.Next(con.readCtx()) {
		if err := cursor.Decode(&result); err != nil {
			logger.Error("mongodbConnection.sum 2", err.Error())
		}
	} else {
		fmt.Println("No data found")
	}

	return result.AggregateValue
}

func (con *mongodbConnection) max(key string) interface{} {
	opts := options.Aggregate()

	// Aggregation pipeline to sum the "amount" field
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: con.where()}},
		{{Key: "$group", Value: bson.M{"_id": nil, "aggregateValue": bson.M{"$max": "$" + key}}}},
	}

	cursor, err := con.collection().Aggregate(con.readCtx(), pipeline, opts)
	if err != nil {
		logger.Error("mongodbConnection.max 1", err.Error())
		return nil
	}
	defer cursor.Close(con.writeCtx())

	// Retrieve the result
	var result struct {
		AggregateValue interface{} `bson:"aggregateValue"`
	}
	if cursor.Next(con.readCtx()) {
		if err := cursor.Decode(&result); err != nil {
			logger.Error("mongodbConnection.max 2", err.Error())
		}

		switch v := result.AggregateValue.(type) {
		case float64:
			return v
		case string:
			parsedTime, err := time.Parse(time.RFC3339, v) // Convert string to time.Time
			if err != nil {
				return v
			}
			return parsedTime
		case bson.DateTime:
			return v.Time() // Convert BSON DateTime to Go time.Time
		default:
			logger.Error(fmt.Errorf("unexpected type: %s", reflect.TypeOf(result.AggregateValue)))
			return v
		}
	} else {
		fmt.Println("No data found")
	}

	return result.AggregateValue
}

func (con *mongodbConnection) min(key string) interface{} {

	opts := options.Aggregate()

	// Aggregation pipeline to sum the "amount" field
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: con.where()}},
		{{Key: "$group", Value: bson.M{"_id": nil, "aggregateValue": bson.M{"$min": "$" + key}}}},
	}

	cursor, err := con.collection().Aggregate(con.readCtx(), pipeline, opts)
	if err != nil {
		logger.Error("mongodbConnection.min 1", err.Error())
		return nil
	}
	defer cursor.Close(con.writeCtx())

	// Retrieve the result
	var result struct {
		AggregateValue interface{} `bson:"aggregateValue"`
	}
	if cursor.Next(con.readCtx()) {

		if err := cursor.Decode(&result); err != nil {
			logger.Error("mongodbConnection.min 2", err.Error())
		}

		switch v := result.AggregateValue.(type) {
		case float64:
			return v
		case string:
			parsedTime, err := time.Parse(time.RFC3339, v) // Convert string to time.Time
			if err != nil {
				return v
			}
			return parsedTime
		case bson.DateTime:
			return v.Time() // Convert BSON DateTime to Go time.Time
		default:
			logger.Error(fmt.Errorf("unexpected type: %s", reflect.TypeOf(result.AggregateValue)))
			return v
		}
	} else {
		fmt.Println("No data found")
	}

	return result.AggregateValue
}

func (con *mongodbConnection) average(key string) float64 {
	// Find with limit and skip
	opts := options.Aggregate()

	// Aggregation pipeline to sum the "amount" field
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: con.where()}},
		{{Key: "$group", Value: bson.M{"_id": nil, "aggregateValue": bson.M{"$avg": "$" + key}}}},
	}

	cursor, err := con.collection().Aggregate(con.readCtx(), pipeline, opts)
	if err != nil {
		logger.Error("mongodbConnection.average 1", err.Error())
		return 0
	}
	defer cursor.Close(con.writeCtx())

	// Retrieve the result
	var result struct {
		AggregateValue float64 `bson:"aggregateValue"`
	}
	if cursor.Next(con.readCtx()) {
		if err := cursor.Decode(&result); err != nil {
			logger.Error("mongodbConnection.average 2", err.Error())
		}
	} else {
		fmt.Println("No data found")
	}

	return result.AggregateValue
}

func (con *mongodbConnection) graph() *datatype.DataMap {
	return &datatype.DataMap{}
}

func (con *mongodbConnection) create(data datatype.DataMap) (*datatype.DataMap, error) {
	var result *datatype.DataMap

	raw, err := encodeForInsert(data)
	if err != nil {
		return nil, err
	}

	res, err := con.collection().InsertOne(con.writeCtx(), raw)

	if err != nil {
		console.Log("mongodbConnection.create", err.Error())
		return nil, err
	}

	if res.Acknowledged {
		result, err = con.storedDocument(raw)
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (con *mongodbConnection) createMany(data []datatype.DataMap) (*[]datatype.DataMap, error) {
	var result *[]datatype.DataMap

	raws := make([]bson.Raw, 0, len(data))
	for _, item := range data {
		raw, err := encodeForInsert(item)
		if err != nil {
			return nil, err
		}
		raws = append(raws, raw)
	}

	res, err := con.collection().InsertMany(con.writeCtx(), raws)

	if err != nil {
		console.Log("mongodbConnection.createMany", err.Error())
		return nil, err
	}

	if res.Acknowledged {
		created := make([]datatype.DataMap, 0, len(raws))

		for _, raw := range raws {
			stored, err := con.storedDocument(raw)
			if err != nil {
				return nil, err
			}

			created = append(created, *stored)
		}

		result = &created
	}

	return result, nil
}

// encodeForInsert encodes a record once, so the bytes inserted are exactly
// the bytes storedDocument decodes: encoding a map twice can order nested
// fields differently each time.
func encodeForInsert(data datatype.DataMap) (bson.Raw, error) {
	if _, ok := data["_id"]; !ok {
		data["_id"] = bson.NewObjectID()
	}

	return bson.Marshal(data)
}

// storedDocument decodes an inserted record the way a read of it would, plus
// the fields find() adds. It saves re-reading a record just written.
func (con *mongodbConnection) storedDocument(raw bson.Raw) (*datatype.DataMap, error) {
	var stored datatype.DataMap
	if err := bson.Unmarshal(raw, &stored); err != nil {
		return nil, err
	}

	con.addRecordFields(stored)

	return &stored, nil
}

// addRecordFields adds the fields every record read through this backend
// carries.
func (con *mongodbConnection) addRecordFields(record datatype.DataMap) {
	record["id"] = record["_id"]
	record["_collection"] = con.query.Model.Collection
	record["_model"] = con.query.Model.Name
}

// update sets data on the first matching record and returns it as updated,
// in one round trip.
func (con *mongodbConnection) update(data datatype.DataMap) (*datatype.DataMap, error) {
	set, err := bson.Marshal(data)
	if err != nil {
		return nil, err
	}

	_, updated, err := con.findOneAndUpdate(set, options.After)
	return updated, err
}

// updateWithPrevious is update, also returning the record as it was before,
// for the audit trail. It's one round trip instead of a read plus a write.
func (con *mongodbConnection) updateWithPrevious(data datatype.DataMap) (previous *datatype.DataMap, updated *datatype.DataMap, err error) {
	// Encoded once: the bytes sent are the bytes merged below, so nested
	// fields come out in the order they're stored.
	set, err := bson.Marshal(data)
	if err != nil {
		return nil, nil, err
	}

	previous, _, err = con.findOneAndUpdate(set, options.Before)
	if err != nil || previous == nil {
		return previous, nil, err
	}

	var applied datatype.DataMap
	if err := bson.Unmarshal(set, &applied); err != nil {
		return previous, nil, err
	}

	// $set replaces top-level fields, so the updated record is the previous
	// one with data applied.
	merged := make(datatype.DataMap, len(*previous)+len(applied))
	for k, v := range *previous {
		merged[k] = v
	}
	for k, v := range applied {
		merged[k] = v
	}
	delete(merged, "id")
	delete(merged, "_collection")
	delete(merged, "_model")

	raw, err := bson.Marshal(merged)
	if err != nil {
		return previous, nil, err
	}

	updated, err = con.storedDocument(raw)

	return previous, updated, err
}

// findOneAndUpdate applies set ($set) to the first record matching the query
// (in its sort order) and returns the record as it was before or after,
// depending on which. Both results are nil when nothing matched.
func (con *mongodbConnection) findOneAndUpdate(set bson.Raw, which options.ReturnDocument) (before *datatype.DataMap, after *datatype.DataMap, err error) {
	opts := options.FindOneAndUpdate().SetReturnDocument(which)
	if con.hasOrderBy() {
		opts = opts.SetSort(con.orderBy())
	}

	var record datatype.DataMap
	err = con.collection().FindOneAndUpdate(con.writeCtx(), con.where(), bson.D{
		{Key: "$set", Value: set},
	}, opts).Decode(&record)

	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil, nil
	}

	if err != nil {
		console.Log("mongodbConnection.update", err.Error())
		return nil, nil, err
	}

	con.addRecordFields(record)

	if which == options.Before {
		return &record, nil, nil
	}

	return nil, &record, nil
}

// updateMany sets data on every matching record and returns them as
// updated. The matching ids are read first: re-reading with the filter
// afterwards would miss records the update moved out of it (e.g. setting
// status void -> archived where status = void).
func (con *mongodbConnection) updateMany(data datatype.DataMap) (*[]datatype.DataMap, error) {
	ids, err := con.matchingIDs()
	if err != nil {
		console.Log("mongodbConnection.updateMany", err.Error())
		return nil, err
	}

	if len(ids) == 0 {
		return &[]datatype.DataMap{}, nil
	}

	res, err := con.collection().UpdateMany(con.writeCtx(), datatype.DataMap{
		"_id": datatype.DataMap{"$in": ids},
	}, datatype.DataMap{
		"$set": data,
	})

	if err != nil {
		console.Log("mongodbConnection.updateMany", err.Error())
		return nil, err
	}

	if !res.Acknowledged {
		return nil, nil
	}

	return con.recordsByIDs(ids)
}

// matchingIDs returns the _id of every record the query matches, in its
// sort order. It's part of a write, so it isn't cancelled with the request.
func (con *mongodbConnection) matchingIDs() ([]interface{}, error) {
	opts := options.Find().SetProjection(bson.D{{Key: "_id", Value: 1}})
	if con.hasOrderBy() {
		opts = opts.SetSort(con.orderBy())
	}

	cursor, err := con.collection().Find(con.writeCtx(), con.where(), opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(con.writeCtx())

	var ids []interface{}
	for cursor.Next(con.writeCtx()) {
		var record struct {
			ID interface{} `bson:"_id"`
		}
		if err := cursor.Decode(&record); err != nil {
			return nil, err
		}
		ids = append(ids, record.ID)
	}

	return ids, cursor.Err()
}

// recordsByIDs reads records by _id, in the order of ids.
func (con *mongodbConnection) recordsByIDs(ids []interface{}) (*[]datatype.DataMap, error) {
	cursor, err := con.collection().Find(con.writeCtx(), datatype.DataMap{
		"_id": datatype.DataMap{"$in": ids},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(con.writeCtx())

	byID := make(map[string]datatype.DataMap, len(ids))
	for cursor.Next(con.writeCtx()) {
		var record datatype.DataMap
		if err := cursor.Decode(&record); err != nil {
			return nil, err
		}
		con.addRecordFields(record)
		byID[idString(record["_id"])] = record
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}

	records := make([]datatype.DataMap, 0, len(ids))
	for _, id := range ids {
		if record, ok := byID[idString(id)]; ok {
			records = append(records, record)
		}
	}

	return &records, nil
}

func (con *mongodbConnection) delete() (interface{}, error) {
	where := con.where()

	if helper.IsNotEmpty(*where) {
		res, err := con.collection().DeleteMany(con.writeCtx(), where)
		if err != nil {
			console.Log("mongodbConnection.delete", err.Error())
			return nil, err
		}
		return res, nil
	}

	return nil, errors.New("filter is empty, not allowed to delete all document at once")
}

func (con *mongodbConnection) selection() *[]string {
	return &[]string{}
}

// where builds the Mongo filter for the query. It's cached on the connection
// because one operation can need it several times (count + find for
// pagination, update + re-read), and building it may run relation
// sub-queries.
func (con *mongodbConnection) where() *datatype.DataMap {
	if con.filter == nil {
		filters := con.extractWhereObject(con.query.where)
		con.filter = &filters
	}

	return con.filter
}

func isLogicalWhereKey(key string) bool {
	return key == "AND" || key == "OR" || key == "NOR"
}

func isListOfMaps(v interface{}) bool {
	val := reflect.ValueOf(v)
	if val.Kind() != reflect.Slice || val.Len() == 0 {
		return false
	}

	for i := 0; i < val.Len(); i++ {
		if !helper.IsMap(val.Index(i).Interface()) {
			return false
		}
	}

	return true
}

func (con *mongodbConnection) extractWhere(where interface{}) datatype.DataMap {
	var filters = datatype.DataMap{}

	if whr, ok := where.(datatype.DataMap); ok {
		for k, v := range whr {
			switch k {
			case "AND":
				var newValue = []interface{}{}
				var vi = helper.ToDataMapList(v)

				for ii := range vi {
					newValue = append(newValue, con.extractWhereObject(vi[ii]))
				}

				if len(newValue) > 0 {
					addFilterClause(filters, "$and", newValue)
				}
			case "OR":
				var newValue = []interface{}{}
				var vi = helper.ToDataMapList(v)

				for ii := range vi {
					newValue = append(newValue, con.extractWhereObject(vi[ii]))
				}

				if len(newValue) > 0 {
					filters["$or"] = newValue
				}
			case "NOR":
				var newValue = []interface{}{}
				var vi = helper.ToDataMapList(v)

				for ii := range vi {
					newValue = append(newValue, con.extractWhereObject(vi[ii]))
				}

				if len(newValue) > 0 {
					filters["$nor"] = newValue
				}
			default:
				// Any other key holding a list of maps is treated as AND. Lists of
				// plain values are regular filters, handled by extractWhereObject.
				if !isListOfMaps(v) {
					continue
				}

				var newValue = []interface{}{}
				var vi = helper.ToDataMapList(v)

				for ii := range vi {
					newValue = append(newValue, con.extractWhereObject(vi[ii]))
				}

				if len(newValue) > 0 {
					addFilterClause(filters, "$and", newValue)
				}
			}
		}
	}

	return filters
}

// extractWhereObject converts one where map into a Mongo filter. Logical keys
// (AND/OR/NOR) go through extractWhere and operator maps through
// extractWhereItem; both take the whole map, so each runs at most once here
// rather than once per key. Plain values become {"$eq": value}, applied last
// so a key filters the same way whether or not other keys use operators.
func (con *mongodbConnection) extractWhereObject(where interface{}) datatype.DataMap {
	var filters = datatype.DataMap{}

	whr, ok := where.(datatype.DataMap)
	if !ok {
		return filters
	}

	hasLogical := false
	hasOperatorMap := false

	for k, v := range whr {
		if isLogicalWhereKey(k) {
			hasLogical = true
		} else if helper.IsMap(v) {
			hasOperatorMap = true
		}
	}

	if hasLogical {
		for ki, vi := range con.extractWhere(whr) {
			filters[ki] = vi
		}
	}

	var relations datatype.DataMap
	if hasOperatorMap {
		var items datatype.DataMap
		items, relations = con.extractWhereItem(whr)

		for ki, vi := range items {
			filters[ki] = vi
		}
	}

	for k, v := range whr {
		if !isLogicalWhereKey(k) && !helper.IsMap(v) {
			filters[k] = datatype.DataMap{
				"$eq": v,
			}
		}
	}

	// Relation filters land on a linking key (e.g. outletId) that the where
	// may also filter directly, so they're combined rather than overwritten.
	for rk, rv := range relations {
		addFilterClause(filters, rk, rv)
	}

	return filters
}

// addFilterClause adds a condition on key so that it holds together with any
// condition already there. Operator maps on the same field are merged when
// they don't share an operator ({$eq} + {$in}); otherwise both conditions go
// under $and. $and lists are concatenated.
func addFilterClause(filters datatype.DataMap, key string, condition interface{}) {
	existing, exists := filters[key]
	if !exists {
		filters[key] = condition
		return
	}

	if reflect.DeepEqual(existing, condition) {
		return
	}

	if key == "$and" {
		existingList, ok1 := existing.([]interface{})
		conditionList, ok2 := condition.([]interface{})
		if ok1 && ok2 {
			filters[key] = append(append([]interface{}{}, existingList...), conditionList...)
			return
		}
	}

	existingOps, ok1 := existing.(datatype.DataMap)
	conditionOps, ok2 := condition.(datatype.DataMap)
	if ok1 && ok2 && !strings.HasPrefix(key, "$") {
		shared := false
		for op := range conditionOps {
			if _, ok := existingOps[op]; ok {
				shared = true
				break
			}
		}

		if !shared {
			merged := make(datatype.DataMap, len(existingOps)+len(conditionOps))
			for op, v := range existingOps {
				merged[op] = v
			}
			for op, v := range conditionOps {
				merged[op] = v
			}
			filters[key] = merged
			return
		}
	}

	delete(filters, key)
	and, _ := filters["$and"].([]interface{})
	filters["$and"] = append(append([]interface{}{}, and...), datatype.DataMap{key: existing}, datatype.DataMap{key: condition})
}

// relationFilter turns a filter on a related model (e.g. {"outlet": {"name":
// {...}}}) into an "$in" on the linking key, by fetching the matching related
// IDs. The sub-query runs with the parent's request context so it stays
// scoped to the same tenant.
func (con *mongodbConnection) relationFilter(field string, where datatype.DataMap) datatype.DataMap {
	model := con.query.Model

	if relation, ok := model.ParentFields[field]; ok {
		return datatype.DataMap{
			relation.ForeignKey: datatype.DataMap{
				"$in": con.relatedIds(relation.ModelName, where, relation.PrimaryKey),
			},
		}
	}

	if relation, ok := model.ChildrenFields[field]; ok {
		return datatype.DataMap{
			relation.PrimaryKey: datatype.DataMap{
				"$in": con.relatedIds(relation.ModelName, where, relation.ForeignKey),
			},
		}
	}

	return datatype.DataMap{}
}

func (con *mongodbConnection) relatedIds(modelName string, where datatype.DataMap, key string) []bson.ObjectID {
	objectIDs := []bson.ObjectID{}

	related := con.query.Model.App.ModelQuery(modelName)
	if related == nil {
		return objectIDs
	}

	related.SetRequestContext(con.query.RequestContext)
	if con.query.skipTenant {
		related.SkipTenant()
	}

	list := related.WhereAll(where).Find(nil)
	if list == nil {
		return objectIDs
	}

	for _, item := range *list {
		if v, ok := item[key]; ok {
			objectIDs = append(objectIDs, helper.ObjectID(v))
		}
	}

	return objectIDs
}

// extractWhereItem converts operator maps ({"price": {"greaterThan": 5}}) and
// relation filters ({"outlet": {"name": {...}}}). Relation filters come back
// separately, keyed by the linking field, for extractWhereObject to combine.
func (con *mongodbConnection) extractWhereItem(where interface{}) (datatype.DataMap, datatype.DataMap) {
	var filters = make(datatype.DataMap)
	var relations = make(datatype.DataMap)

	if whr, ok := where.(datatype.DataMap); ok {
		for k, v := range whr {

			if helper.IsMap(v) {
				vi := helper.ToDataMap(v)
				var relation datatype.DataMap // fetched once per field, not once per nested key

				for ki, vii := range vi {
					innerFilter := datatype.DataMap{}
					if inf, ok := filters[k].(datatype.DataMap); ok {
						innerFilter = inf
					}

					if k == "id" || k == "_id" {
						k = "_id"
					}

					if helper.Contains(graphqlOperations[:], ki) ||
						helper.Contains(graphqlArrayOperations[:], ki) ||
						helper.Contains(graphqlBooleanOperations[:], ki) ||
						helper.Contains(mongodbSpecialOperations[:], ki) {

						switch vii {
						case string(NULLValue):
							vii = nil
						case string(NullValue):
							vii = nil
						case string(nullValue):
							vii = nil
						}

						if con.query.Model.fieldKindSets().id[k] || k == "id" || k == "_id" {
							if helper.IsNotEmpty(vii) {
								if helper.IsArray(vii) {
									viii := helper.ToList[any](vii)
									count := len(viii)
									for i := 0; i < count; i++ {
										viii[i] = helper.ObjectID(viii[i])
									}

									vii = viii
								} else {
									vii = helper.ObjectID(vii)
								}
							}
						}

						switch ki {
						case "equalTo", "$eq":
							innerFilter["$eq"] = vii
						case "notEqualTo", "$ne":
							innerFilter["$ne"] = vii
						case "lessThan", "$lt":
							viii := helper.ConvertCalculatedValue(vii)
							innerFilter["$lt"] = viii
						case "notLessThan", "$not_lt":
							viii := helper.ConvertCalculatedValue(vii)
							innerFilter["$not"] = datatype.DataMap{
								"$lt": viii,
							}
						case "lessThanOrEqualTo", "$lte":
							viii := helper.ConvertCalculatedValue(vii)
							innerFilter["$lte"] = viii
						case "notLessThanOrEqualTo", "$not_lte":
							viii := helper.ConvertCalculatedValue(vii)
							innerFilter["$not"] = datatype.DataMap{
								"$lte": viii,
							}
						case "greaterThan", "$gt":
							viii := helper.ConvertCalculatedValue(vii)
							innerFilter["$gt"] = viii
						case "notGreaterThan", "$not_gt":
							viii := helper.ConvertCalculatedValue(vii)
							innerFilter["$not"] = datatype.DataMap{
								"$gt": viii,
							}
						case "greaterThanOrEqualTo", "$gte":
							viii := helper.ConvertCalculatedValue(vii)
							innerFilter["$gte"] = viii
						case "notGreaterThanOrEqualTo", "$not_gte":
							viii := helper.ConvertCalculatedValue(vii)
							innerFilter["$not"] = datatype.DataMap{
								"$gte": viii,
							}
						case "in", "$in":
							innerFilter["$in"] = vii
						case "all", "$all":
							innerFilter["$all"] = vii
						case "notIn", "$nin":
							innerFilter["$nin"] = vii
						case "exists":
							if exists, ok := vii.(bool); ok {
								if exists {
									// Present and not null.
									innerFilter["$exists"] = true
									innerFilter["$nin"] = []interface{}{nil}
								} else {
									// Missing or null: {$eq: null} matches both.
									innerFilter["$eq"] = nil
								}
							}
						case "matchesRegex":
							if _v, ok := vii.(string); ok {
								innerFilter["$regex"] = regexp.MustCompile(helper.CreateFuzzyRegex(_v)).String()
								innerFilter["$options"] = "i"
							}
						case "options":
							innerFilter["$eq"] = vii
						case "text":
							innerFilter["$eq"] = vii
						case "inQueryKey":
							innerFilter["$eq"] = vii
						case "notInQueryKey":
							innerFilter["$eq"] = vii
						default:
							innerFilter[ki] = vii
						}

						filters[k] = innerFilter
					} else {
						if relation == nil {
							relation = con.relationFilter(k, vi)
						}
					}
				}

				for rk, rv := range relation {
					addFilterClause(relations, rk, rv)
				}
			} else if !isLogicalWhereKey(k) {
				innerFilter := datatype.DataMap{}

				if inf, ok := filters[k].(datatype.DataMap); ok {
					innerFilter = inf
				}

				if helper.IsArray(v) {
					innerFilter["$in"] = v
				} else {
					innerFilter["$eq"] = v
				}

				filters[k] = innerFilter
			}
		}
	}

	return filters, relations
}

func (con *mongodbConnection) hasGroup() bool {
	if len(con.query.groupBy) > 0 {
		return true
	}

	if len(con.query.groupByRaw) > 0 {
		return true
	}

	return false
}

func (con *mongodbConnection) groupBy() bson.M {
	values := bson.M{}

	if con.hasGroup() {
		subValues := bson.M{}

		for _, v := range con.query.groupBy {
			subValues[v] = "$" + v
		}

		for k, v := range con.query.groupByRaw {
			subValues[k] = v
		}

		if _, ok := subValues["_id"]; !ok {
			values["_id"] = subValues
		} else {
			values = subValues
		}
	}

	return values
}

func (con *mongodbConnection) hasOrderBy() bool {
	if len(con.query.orderBy) > 0 {
		return true
	}

	return false
}

func (con *mongodbConnection) orderBy() interface{} {
	order := make(datatype.DataMap)

	if con.query.orderBy != nil {
		for k, v := range con.query.orderBy {
			order[k] = 1
			if strings.ToLower(v) == "desc" {
				order[k] = -1
			}
		}
	}

	return order
}

func (con *mongodbConnection) hasProjection() bool {
	if len(con.query.selection) > 0 {
		return true
	}

	return true
}

func (con *mongodbConnection) projection() interface{} {
	selectFields := make(datatype.DataMap)

	selectFields["__v"] = 0 // Always include the _id field

	if len(con.query.groupBy) > 0 {
		// selectFields["__v"] = 1
		// for _, k := range con.query.groupBy {
		// 	// selectFields[k] = 1
		// }
	} else if len(con.query.selection) > 0 {
		// selectFields["__v"] = 1
		// for _, k := range con.query.selection {
		// 	selectFields[k] = 1
		// }
	}

	return selectFields
}

func (con *mongodbConnection) limit() int {
	if con.query.limit > 0 {
		return con.query.limit
	}

	return -1
}

func (con *mongodbConnection) skip() int {
	if con.query.skip > 0 {
		return con.query.skip
	}

	return con.query.limit * (con.page() - 1)
}

func (con *mongodbConnection) page() int {
	if con.query.page < 1 {
		return 1
	}

	return con.query.page
}
