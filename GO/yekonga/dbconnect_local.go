package yekonga

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/logger"
	localDB "github.com/robertkonga/yekonga-server-go/plugins/database/db"
)

type localDbConnection struct {
	query  *DataModelQuery
	ctx    *context.Context
	client *localDB.DB
	mut    sync.RWMutex
}

func newLocalDBInstance(con *localDbConnection) localDbConnection {
	return localDbConnection{
		ctx:    con.ctx,
		client: con.client,
		query: &DataModelQuery{
			Model:          con.query.Model,
			RequestContext: con.query.RequestContext,
			QueryContext:   con.query.QueryContext,
		},
	}
}

func (con *localDbConnection) connect() any {
	return con.client
}

func (con *localDbConnection) collection() *localDB.Col {
	if !con.client.ColExists(con.query.Model.Collection) {
		con.client.Create(con.query.Model.Collection)
	}

	collection := con.client.Use(con.query.Model.Collection)
	return collection
}

func (con *localDbConnection) findOne() *datatype.DataMap {
	results := con.find()
	if results != nil && len(*results) > 0 {
		return &(*results)[0]
	}
	return nil
}

func (con *localDbConnection) findAll() *[]datatype.DataMap {
	return con.find()
}

func (con *localDbConnection) find() *[]datatype.DataMap {
	var ids map[int]struct{} = make(map[int]struct{})
	var result []datatype.DataMap = make([]datatype.DataMap, 0, 10)
	var where = con.conditionParams()

	localDB.EvalQuery(where, con.collection(), &ids)

	// Convert ids to slice for ordering
	idSlice := make([]int, 0, len(ids))
	for id := range ids {
		idSlice = append(idSlice, id)
	}

	// Sort if needed
	if con.hasOrderBy() {
		con.sortIDs(&idSlice, &result)
	}

	// Apply limit and skip
	start := con.skip()
	if start < 0 {
		start = 0
	}
	end := start + con.limit()
	if con.limit() <= 0 {
		end = len(idSlice)
	}

	if start > len(idSlice) {
		start = len(idSlice)
	}
	if end > len(idSlice) {
		end = len(idSlice)
	}

	for i := start; i < end; i++ {
		data, err := con.collection().Read(idSlice[i])
		if err == nil {
			data["id"] = idSlice[i]
			data["_collection"] = con.query.Model.Collection
			data["_model"] = con.query.Model.Name
			result = append(result, data)
		}
	}

	return &result
}

func (con *localDbConnection) pagination() *datatype.DataMap {
	var lastPage int64 = 1
	total := con.count()
	perPage := int64(con.limit())
	if perPage <= 0 {
		perPage = 10
	}
	currentPage := int64(con.page())
	from := (perPage * (currentPage - 1)) + 1
	to := perPage * (currentPage)
	remainder := total % perPage

	if remainder == 0 {
		lastPage = (total) / perPage
	} else {
		lastPage = (total + (perPage - total%perPage)) / perPage
	}

	con.query.Take(int(perPage))

	result := datatype.DataMap{
		"total":       total,
		"perPage":     perPage,
		"currentPage": currentPage,
		"lastPage":    lastPage,
		"from":        from,
		"to":          to,
		"data":        con.find(),
	}

	return &result
}

func (con *localDbConnection) summary() *datatype.DataMap {
	result := datatype.DataMap{
		"count": con.count(),
		"sum":   con.sum(""),
		"max":   con.max(""),
		"min":   con.min(""),
		"graph": datatype.DataMap{},
	}

	return &result
}

func (con *localDbConnection) count() int64 {
	var count int64 = 0

	con.collection().ForEachDoc(func(id int, data []byte) bool {
		count++
		return true
	})

	return count
}

func (con *localDbConnection) max(key string) interface{} {
	var maxVal interface{}
	var hasValue bool = false

	con.collection().ForEachDoc(func(id int, data []byte) bool {
		var doc datatype.DataMap
		json.Unmarshal(data, &doc)

		if val, ok := doc[key]; ok {
			if !hasValue {
				maxVal = val
				hasValue = true
			} else {
				valFloat := helper.ToFloat64(val)
				maxValFloat := helper.ToFloat64(maxVal)
				if valFloat > maxValFloat {
					maxVal = val
				}
			}
		}
		return true
	})

	if hasValue {
		return maxVal
	}
	return nil
}

func (con *localDbConnection) min(key string) interface{} {
	var minVal interface{}
	var hasValue bool = false

	con.collection().ForEachDoc(func(id int, data []byte) bool {
		var doc datatype.DataMap
		json.Unmarshal(data, &doc)

		if val, ok := doc[key]; ok {
			if !hasValue {
				minVal = val
				hasValue = true
			} else {
				valFloat := helper.ToFloat64(val)
				minValFloat := helper.ToFloat64(minVal)
				if valFloat < minValFloat {
					minVal = val
				}
			}
		}
		return true
	})

	if hasValue {
		return minVal
	}
	return nil
}

func (con *localDbConnection) sum(key string) float64 {
	var sum float64 = 0

	con.collection().ForEachDoc(func(id int, data []byte) bool {
		var doc datatype.DataMap
		json.Unmarshal(data, &doc)

		if val, ok := doc[key]; ok {
			sum += helper.ToFloat64(val)
		}
		return true
	})

	return sum
}

func (con *localDbConnection) average(key string) float64 {
	var sum float64 = 0
	var count int64 = 0

	con.collection().ForEachDoc(func(id int, data []byte) bool {
		var doc datatype.DataMap
		json.Unmarshal(data, &doc)

		if val, ok := doc[key]; ok {
			sum += helper.ToFloat64(val)
			count++
		}
		return true
	})

	if count > 0 {
		return sum / float64(count)
	}
	return 0
}

func (con *localDbConnection) graph() *datatype.DataMap {
	return &datatype.DataMap{}
}

func (con *localDbConnection) create(data datatype.DataMap) (*datatype.DataMap, error) {
	id, err := con.collection().Insert(data)
	if err != nil {
		return nil, err
	}

	createdRecord := newLocalDBInstance(con).query.Where("id", id).FindOne(nil)
	return createdRecord, nil
}

func (con *localDbConnection) createMany(data []datatype.DataMap) (*[]datatype.DataMap, error) {
	var result []datatype.DataMap = []datatype.DataMap{}

	for _, d := range data {
		id, err := con.collection().Insert(d)
		if err == nil {
			record := newLocalDBInstance(con).query.Where("id", id).FindOne(nil)
			if record != nil {
				result = append(result, *record)
			}
		}
	}

	return &result, nil
}

func (con *localDbConnection) update(data datatype.DataMap) (*datatype.DataMap, error) {
	// Get the first matching record to get its ID
	results := con.find()
	if results == nil || len(*results) == 0 {
		return nil, errors.New("no records found to update")
	}

	id := (*results)[0]["id"]
	idInt, ok := id.(int)
	if !ok {
		return nil, errors.New("invalid id type")
	}

	// Merge existing data with new data
	doc, err := con.collection().Read(idInt)
	if err != nil {
		return nil, err
	}

	for k, v := range data {
		doc[k] = v
	}

	err = con.collection().Update(idInt, doc)
	if err != nil {
		return nil, err
	}

	updatedRecord := newLocalDBInstance(con).query.Where("id", idInt).FindOne(nil)
	return updatedRecord, nil
}

func (con *localDbConnection) updateMany(data datatype.DataMap) (*[]datatype.DataMap, error) {
	// Get all matching records
	results := con.find()
	if results == nil || len(*results) == 0 {
		return nil, errors.New("no records found to update")
	}

	for _, result := range *results {
		id := result["id"]
		idInt, ok := id.(int)
		if !ok {
			continue
		}

		doc, err := con.collection().Read(idInt)
		if err != nil {
			continue
		}

		for k, v := range data {
			doc[k] = v
		}

		con.collection().Update(idInt, doc)
	}

	updatedRecords := newLocalDBInstance(con).query.collection().find()
	return updatedRecords, nil
}

func (con *localDbConnection) delete() (interface{}, error) {
	results := con.find()
	if results == nil || len(*results) == 0 {
		return nil, errors.New("no records found to delete")
	}

	deletedCount := 0
	for _, result := range *results {
		id := result["id"]
		idInt, ok := id.(int)
		if !ok {
			continue
		}

		err := con.collection().Delete(idInt)
		if err == nil {
			deletedCount++
		}
	}

	return deletedCount, nil
}

func (con *localDbConnection) selection() *[]string {
	return &[]string{}
}

func (con *localDbConnection) hasOrderBy() bool {
	return len(con.query.orderBy) > 0
}

func (con *localDbConnection) sortIDs(ids *[]int, result *[]datatype.DataMap) {
	if !con.hasOrderBy() {
		return
	}

	// Load data for all IDs first
	dataMap := make(map[int]datatype.DataMap)
	for _, id := range *ids {
		data, err := con.collection().Read(id)
		if err == nil {
			dataMap[id] = data
		}
	}

	// Sort by order by fields
	// Note: This is a simplified sort, may need enhancement for complex sorting
	for k, v := range con.query.orderBy {
		isDesc := v == "desc"
		_ = k
		_ = isDesc
		// Sort logic would go here based on field k and direction
	}
}

func (con *localDbConnection) conditionParams() interface{} {
	var params = datatype.DataMap{}
	var where = con.where()

	if w, ok := where.(map[string][]datatype.DataMap); ok {
		for k, v := range w {
			params[k] = v
		}
	}

	if con.limit() > 0 {
		params["limit"] = con.limit()
	}

	return params
}

func (con *localDbConnection) where() interface{} {
	var filters = map[string][]datatype.DataMap{
		"and": []datatype.DataMap{},
	}
	operations := [...]string{
		"equalTo",
		"notEqualTo",
		"lessThan",
		"notLessThan",
		"lessThanOrEqualTo",
		"notLessThanOrEqualTo",
		"greaterThan",
		"notGreaterThan",
		"greaterThanOrEqualTo",
		"notGreaterThanOrEqualTo",
		"matchesRegex",
		"options",
	}

	arrayOperations := [...]string{
		"in",
		"all",
		"notIn",
	}

	booleanOperations := [...]string{
		"exists",
	}

	// logger.Info("where", 1)
	if con.query.where != nil {
		// logger.Info("where", 2)
		for k, v := range con.query.where {
			// logger.Info("where", 3)

			if vi, ok := v.(datatype.DataMap); ok {
				// logger.Info("where", 4)
				for ki, vii := range vi {
					// logger.Info("where", 5)
					if helper.Contains(operations[:], ki) ||
						helper.Contains(arrayOperations[:], ki) ||
						helper.Contains(booleanOperations[:], ki) {
						// logger.Info("where", 6)
						switch ki {
						case "equalTo":
							filters["and"] = append(filters["and"], datatype.DataMap{
								"eq": vii,
								"in": []interface{}{k},
							})
						case "notEqualTo":
							filters["and"] = append(filters["and"], datatype.DataMap{
								"not": datatype.DataMap{
									"eq": vii,
									"in": []interface{}{k},
								},
							})
						case "lessThan":
							viii := helper.ConvertCalculatedValue(vii)

							filters["and"] = append(filters["and"], datatype.DataMap{
								"lt": viii,
								"in": []interface{}{k},
							})
						case "notLessThan":
							viii := helper.ConvertCalculatedValue(vii)

							filters["and"] = append(filters["and"], datatype.DataMap{
								"not": datatype.DataMap{
									"lt": viii,
									"in": []interface{}{k},
								},
							})
						case "lessThanOrEqualTo":
							viii := helper.ConvertCalculatedValue(vii)
							filters["and"] = append(filters["and"], datatype.DataMap{
								"lte": viii,
								"in":  []interface{}{k},
							})
						case "notLessThanOrEqualTo":
							viii := helper.ConvertCalculatedValue(vii)
							filters["and"] = append(filters["and"], datatype.DataMap{
								"not": datatype.DataMap{
									"lte": viii,
									"in":  []interface{}{k},
								},
							})
						case "greaterThan":
							viii := helper.ConvertCalculatedValue(vii)
							filters["and"] = append(filters["and"], datatype.DataMap{
								"gt": viii,
								"in": []interface{}{k},
							})
						case "notGreaterThan":
							viii := helper.ConvertCalculatedValue(vii)
							filters["and"] = append(filters["and"], datatype.DataMap{
								"not": datatype.DataMap{
									"gt": viii,
									"in": []interface{}{k},
								},
							})
						case "greaterThanOrEqualTo":
							viii := helper.ConvertCalculatedValue(vii)
							filters["and"] = append(filters["and"], datatype.DataMap{
								"gte": viii,
								"in":  []interface{}{k},
							})
						case "notGreaterThanOrEqualTo":
							viii := helper.ConvertCalculatedValue(vii)
							filters["and"] = append(filters["and"], datatype.DataMap{
								"not": datatype.DataMap{
									"gte": viii,
									"in":  []interface{}{k},
								},
							})
						case "in":
							// filters[k]["$in"] = vii
							filters["and"] = append(filters["and"], datatype.DataMap{
								"$in": vii,
								"in":  []interface{}{k},
							})
						case "all":
							// filters[k]["$all"] = vii
							filters["and"] = append(filters["and"], datatype.DataMap{
								"all": vii,
								"in":  []interface{}{k},
							})
						case "notIn":
							// filters[k]["$nin"] = vii
							filters["and"] = append(filters["and"], datatype.DataMap{
								"not": datatype.DataMap{
									"$nin": vii,
									"in":   []interface{}{k},
								},
							})
						case "exists":
							if exists, ok := vii.(bool); ok {
								if exists {
									// filters[k]["$exists"] = true
									// filters[k]["$nin"] = []interface{}{nil}
								} else {
									// filters["$or"] = []datatype.DataMap{}

									// filters["$or"] = append(filters["$or"], map[string]datatype.DataMap{
									// 	k: datatype.DataMap{
									// 		"$exists": false,
									// 	},
									// })

									// filters["$or"] = append(filters["$or"], map[string]datatype.DataMap{
									// 	k: datatype.DataMap{
									// 		"$nin": []interface{}{nil},
									// 	},
									// })
								}
							}
						case "matchesRegex":
							filters["and"] = append(filters["and"], datatype.DataMap{
								"has": vii,
								"in":  []interface{}{k},
							})
						case "options":
							filters["and"] = append(filters["and"], datatype.DataMap{
								"eq": vii,
								"in": []interface{}{k},
							})
						case "text":
							filters["and"] = append(filters["and"], datatype.DataMap{
								"eq": vii,
								"in": []interface{}{k},
							})
						case "inQueryKey":
							filters["and"] = append(filters["and"], datatype.DataMap{
								"eq": vii,
								"in": []interface{}{k},
							})
						case "notInQueryKey":
							filters["and"] = append(filters["and"], datatype.DataMap{
								"eq": vii,
								"in": []interface{}{k},
							})
						}
					} else {
						logger.Info("where", 9)

					}
				}
			} else {
				filters["and"] = append(filters["and"], datatype.DataMap{
					"eq": v,
					"in": []interface{}{k},
				})
			}
		}
	}

	return filters
}

func (con *localDbConnection) groupBy() *[]interface{} {
	return &[]interface{}{}
}

func (con *localDbConnection) orderBy() *[]datatype.DataMap {
	return &[]datatype.DataMap{}
}

func (con *localDbConnection) limit() int {
	if con.query.limit > 0 {
		return con.query.limit
	}

	return -1
}

func (con *localDbConnection) skip() int {
	if con.query.skip > 0 {
		return con.query.skip
	}

	return con.query.limit * (con.query.page - 1)
}

func (con *localDbConnection) page() int {
	if con.query.page < 1 {
		return 1
	}

	return con.query.page
}
