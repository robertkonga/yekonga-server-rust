package yekonga

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper/logger"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/mongo"
)

// sqlConnection is the backend for config.database.kind "mysql" and "sql",
// on MySQL-compatible servers (MySQL 5.7+, MariaDB 10.2+).
//
// It behaves like the MongoDB backend, so the rest of the framework doesn't
// need to know which one is in use:
//   - Filters come from the MongoDB filter builder (the one place where-
//     semantics live: operators, null handling, relations, tenants) and are
//     translated to SQL with the same meaning. See dbconnect_sql_filter.go.
//   - Records keep the MongoDB shape: the primary key column is _id, stored
//     as hex and read back as an ObjectID, with id, _collection and _model
//     added to every record.
//   - Tables and columns are created from database.json at startup. See
//     dbconnect_sql_schema.go.
type sqlConnection struct {
	query  *DataModelQuery
	ctx    *context.Context
	client *sql.DB
	where  *sqlClause // built once; a connection serves a single query
}

// sqlClause is a SQL fragment and the arguments for its placeholders.
type sqlClause struct {
	text string
	args []interface{}
	err  error
}

var errSQLNotConnected = errors.New("the SQL database is not connected")

func quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func (con *sqlConnection) table() string {
	return quoteIdent(con.query.Model.Collection)
}

func (con *sqlConnection) readCtx() context.Context {
	if con.ctx == nil || *con.ctx == nil {
		return context.Background()
	}

	return *con.ctx
}

// opCtx is the context for one statement. Reads stop if the request's client
// disconnects; writes don't (see mongodbConnection.writeCtx). Both are bound
// by database.queryTimeoutSeconds.
func (con *sqlConnection) opCtx(write bool) (context.Context, context.CancelFunc) {
	ctx := con.readCtx()
	if write {
		ctx = context.WithoutCancel(ctx)
	}

	if cfg := con.query.Model.Config; cfg != nil && cfg.Database.QueryTimeoutSeconds > 0 {
		return context.WithTimeout(ctx, time.Duration(cfg.Database.QueryTimeoutSeconds)*time.Second)
	}

	return ctx, func() {}
}

// whereClause returns " WHERE ..." for the query's filter, or "".
func (con *sqlConnection) whereClause() (string, []interface{}, error) {
	if con.where == nil {
		filter := (&mongodbConnection{query: con.query}).where()
		builder := &sqlFilterBuilder{model: con.query.Model}
		text, err := builder.build(*filter)
		con.where = &sqlClause{text: text, args: builder.args, err: err}
	}

	if con.where.err != nil || con.where.text == "" {
		return "", nil, con.where.err
	}

	return " WHERE " + con.where.text, con.where.args, nil
}

func (con *sqlConnection) hasOrderBy() bool {
	return len(con.query.orderBy) > 0
}

// orderClause returns " ORDER BY ..." over the given expressions, or "".
// Keys are sorted so the same query always produces the same SQL.
func (con *sqlConnection) orderClause(expr func(key string) string) string {
	keys := make([]string, 0, len(con.query.orderBy))
	for k := range con.query.orderBy {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		e := expr(k)
		if e == "" {
			continue
		}

		if strings.ToLower(con.query.orderBy[k]) == "desc" {
			parts = append(parts, e+" DESC")
		} else {
			parts = append(parts, e+" ASC")
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return " ORDER BY " + strings.Join(parts, ", ")
}

func (con *sqlConnection) columnOrder(key string) string {
	expr, _, _ := sqlColumnExpr(con.query.Model, key)
	if expr == "NULL" {
		return ""
	}
	return expr
}

func (con *sqlConnection) limitClause() string {
	limit, skip := con.limit(), con.skip()

	switch {
	case limit > 0 && skip > 0:
		return fmt.Sprintf(" LIMIT %d OFFSET %d", limit, skip)
	case limit > 0:
		return fmt.Sprintf(" LIMIT %d", limit)
	case skip > 0:
		// MySQL has no OFFSET without LIMIT; this is its documented "all rows".
		return fmt.Sprintf(" LIMIT 18446744073709551615 OFFSET %d", skip)
	}

	return ""
}

// limit, skip and page match mongodbConnection's.
func (con *sqlConnection) limit() int {
	if con.query.limit > 0 {
		return con.query.limit
	}

	return -1
}

func (con *sqlConnection) skip() int {
	if con.query.skip > 0 {
		return con.query.skip
	}

	return con.query.limit * (con.page() - 1)
}

func (con *sqlConnection) page() int {
	if con.query.page < 1 {
		return 1
	}

	return con.query.page
}

func (con *sqlConnection) hasGroup() bool {
	return len(con.query.groupBy) > 0 || len(con.query.groupByRaw) > 0
}

func (con *sqlConnection) addRecordFields(record datatype.DataMap) {
	record["id"] = record["_id"]
	record["_collection"] = con.query.Model.Collection
	record["_model"] = con.query.Model.Name
}

// selectRecords runs a query returning whole records.
func (con *sqlConnection) selectRecords(ctx context.Context, runner sqlRunner, query string, args []interface{}) ([]datatype.DataMap, error) {
	rows, err := runner.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records, err := scanSQLRows(rows, func(column string) (DataModelFieldType, bool) {
		return sqlFieldKind(con.query.Model, column)
	})
	if err != nil {
		return nil, err
	}

	for _, record := range records {
		con.addRecordFields(record)
	}

	return records, nil
}

// sqlRunner is a *sql.DB or a *sql.Tx.
type sqlRunner interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

func (con *sqlConnection) findOne() *datatype.DataMap {
	var result datatype.DataMap

	if con.client == nil {
		logger.Error("sqlConnection.findOne", errSQLNotConnected.Error())
		return &result
	}

	where, args, err := con.whereClause()
	if err != nil {
		logger.Error("sqlConnection.findOne", err.Error())
		return &result
	}

	ctx, cancel := con.opCtx(false)
	defer cancel()

	query := "SELECT * FROM " + con.table() + where + con.orderClause(con.columnOrder) + " LIMIT 1"
	records, err := con.selectRecords(ctx, con.client, query, args)
	if err != nil {
		logger.Error("sqlConnection.findOne", err.Error())
		return &result
	}

	if len(records) > 0 {
		result = records[0]
	}

	return &result
}

func (con *sqlConnection) findAll() *[]datatype.DataMap {
	return con.find()
}

func (con *sqlConnection) find() *[]datatype.DataMap {
	empty := &[]datatype.DataMap{}

	if con.client == nil {
		logger.Error("sqlConnection.find", errSQLNotConnected.Error())
		return empty
	}

	if con.hasGroup() {
		return con.findGroups()
	}

	where, args, err := con.whereClause()
	if err != nil {
		logger.Error("sqlConnection.find", err.Error())
		return empty
	}

	ctx, cancel := con.opCtx(false)
	defer cancel()

	query := "SELECT * FROM " + con.table() + where + con.orderClause(con.columnOrder) + con.limitClause()
	records, err := con.selectRecords(ctx, con.client, query, args)
	if err != nil {
		logger.Error("sqlConnection.find", err.Error())
		return empty
	}

	return &records
}

// findGroups is find with groupBy/groupByRaw: one record per group, with the
// group's fields at the top level and together under _id, as the MongoDB
// backend returns them.
func (con *sqlConnection) findGroups() *[]datatype.DataMap {
	empty := &[]datatype.DataMap{}

	group, err := buildSQLGroup(con.query)
	if err != nil {
		logger.Error("sqlConnection.find group", err.Error())
		return empty
	}

	where, args, err := con.whereClause()
	if err != nil {
		logger.Error("sqlConnection.find group", err.Error())
		return empty
	}

	query := "SELECT " + strings.Join(group.selects, ", ") + " FROM " + con.table() + where
	if len(group.keys) > 0 {
		quoted := make([]string, len(group.keys))
		for i, key := range group.keys {
			quoted[i] = quoteIdent(key)
		}
		query += " GROUP BY " + strings.Join(quoted, ", ")
	}

	query += con.orderClause(func(key string) string {
		if _, ok := group.kinds[key]; ok {
			return quoteIdent(key)
		}
		return "" // only grouped values can be ordered by
	})
	query += con.limitClause()

	ctx, cancel := con.opCtx(false)
	defer cancel()

	rows, err := con.client.QueryContext(ctx, query, args...)
	if err != nil {
		logger.Error("sqlConnection.find group", err.Error())
		return empty
	}
	defer rows.Close()

	records, err := scanSQLRows(rows, func(column string) (DataModelFieldType, bool) {
		kind, ok := group.kinds[column]
		return kind, ok
	})
	if err != nil {
		logger.Error("sqlConnection.find group", err.Error())
		return empty
	}

	for _, record := range records {
		id := make(datatype.DataMap, len(group.keys))
		for _, key := range group.keys {
			id[key] = record[key]
		}

		record["_id"] = id
		con.addRecordFields(record)
	}

	return &records
}

func (con *sqlConnection) pagination() *datatype.DataMap {
	var lastPage int64
	perPage := con.limit()
	currentPage := con.page()
	from := (perPage * (currentPage - 1)) + 1
	to := perPage * (currentPage)

	con.query.Take(perPage)

	total := con.count()
	data := con.find()

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

func (con *sqlConnection) summary() *datatype.DataMap {
	result := datatype.DataMap{
		"count": 0,
		"sum":   0,
		"max":   0,
		"min":   0,
		"graph": datatype.DataMap{},
	}

	return &result
}

func (con *sqlConnection) graph() *datatype.DataMap {
	return &datatype.DataMap{}
}

func (con *sqlConnection) count() int64 {
	if con.client == nil {
		logger.Error("sqlConnection.count", errSQLNotConnected.Error())
		return 0
	}

	where, args, err := con.whereClause()
	if err != nil {
		logger.Error("sqlConnection.count", err.Error())
		return 0
	}

	query := "SELECT COUNT(*) FROM " + con.table() + where

	if len(con.query.distinct) > 0 {
		// Distinct combinations, missing values included, as MongoDB groups them.
		columns := make([]string, len(con.query.distinct))
		for i, field := range con.query.distinct {
			columns[i], _, _ = sqlColumnExpr(con.query.Model, field)
		}
		query = "SELECT COUNT(*) FROM (SELECT DISTINCT " + strings.Join(columns, ", ") + " FROM " + con.table() + where + ") AS distinct_values"
	}

	ctx, cancel := con.opCtx(false)
	defer cancel()

	var total int64
	if err := con.client.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		logger.Error("sqlConnection.count", err.Error())
		return 0
	}

	return total
}

// aggregate runs one aggregate function over a field.
func (con *sqlConnection) aggregate(function string, key string) (interface{}, DataModelFieldType, bool) {
	if con.client == nil {
		logger.Error("sqlConnection."+function, errSQLNotConnected.Error())
		return nil, DataModelString, false
	}

	where, args, err := con.whereClause()
	if err != nil {
		logger.Error("sqlConnection."+function, err.Error())
		return nil, DataModelString, false
	}

	expr, kind, _ := sqlColumnExpr(con.query.Model, key)

	ctx, cancel := con.opCtx(false)
	defer cancel()

	selected := function + "(" + expr + ")"
	if function == "AVG" {
		selected = sqlAverage(expr)
	}

	var value interface{}
	query := "SELECT " + selected + " FROM " + con.table() + where
	if err := con.client.QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
		logger.Error("sqlConnection."+function, err.Error())
		return nil, kind, false
	}

	return value, kind, true
}

func (con *sqlConnection) sum(key string) float64 {
	value, _, _ := con.aggregate("SUM", key)
	number, _ := decodeSQLValue(DataModelFloat, value).(float64)
	return number
}

func (con *sqlConnection) average(key string) float64 {
	value, _, _ := con.aggregate("AVG", key)
	number, _ := decodeSQLValue(DataModelFloat, value).(float64)
	return number
}

func (con *sqlConnection) max(key string) interface{} {
	value, kind, _ := con.aggregate("MAX", key)
	return extremeValue(decodeSQLValue(kind, value))
}

func (con *sqlConnection) min(key string) interface{} {
	value, kind, _ := con.aggregate("MIN", key)
	return extremeValue(decodeSQLValue(kind, value))
}

// extremeValue matches what the MongoDB backend's max/min return: dates as
// time.Time rather than bson.DateTime.
func extremeValue(value interface{}) interface{} {
	if date, ok := value.(bson.DateTime); ok {
		return date.Time()
	}
	return value
}

// insertRecords inserts records with one multi-row INSERT per chunk.
func (con *sqlConnection) insertRecords(ctx context.Context, runner sqlRunner, records []datatype.DataMap) ([]string, error) {
	model := con.query.Model

	// Columns present in any record; a record without one gets NULL.
	columnSet := map[string]bool{}
	for _, record := range records {
		if _, ok := record["_id"]; !ok {
			record["_id"] = bson.NewObjectID()
		}
		for key := range record {
			column := sqlColumnName(key)
			if _, known := sqlFieldKind(model, column); known {
				columnSet[column] = true
			}
		}
	}

	columns := make([]string, 0, len(columnSet))
	for column := range columnSet {
		columns = append(columns, column)
	}
	sort.Strings(columns)

	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = quoteIdent(column)
	}
	rowPlaceholder := "(" + strings.TrimSuffix(strings.Repeat("?, ", len(columns)), ", ") + ")"

	ids := make([]string, 0, len(records))

	// Keep each statement well under the server's placeholder limit.
	chunk := 60000 / len(columns)
	if chunk > 500 {
		chunk = 500
	}

	for start := 0; start < len(records); start += chunk {
		end := start + chunk
		if end > len(records) {
			end = len(records)
		}

		placeholders := make([]string, 0, end-start)
		args := make([]interface{}, 0, (end-start)*len(columns))

		for _, record := range records[start:end] {
			for _, column := range columns {
				value := record[column]
				if column == "_id" {
					ids = append(ids, idString(value))
				}

				kind, _ := sqlFieldKind(model, column)
				arg, err := encodeSQLValue(kind, value)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", column, err)
				}
				args = append(args, arg)
			}
			placeholders = append(placeholders, rowPlaceholder)
		}

		query := "INSERT INTO " + con.table() + " (" + strings.Join(quoted, ", ") + ") VALUES " + strings.Join(placeholders, ", ")
		if _, err := runner.ExecContext(ctx, query, args...); err != nil {
			return nil, err
		}
	}

	return ids, nil
}

// recordsByID reads records by primary key, in the order of ids.
func (con *sqlConnection) recordsByID(ctx context.Context, runner sqlRunner, ids []string) ([]datatype.DataMap, error) {
	if len(ids) == 0 {
		return []datatype.DataMap{}, nil
	}

	byID := make(map[string]datatype.DataMap, len(ids))
	for _, chunk := range chunkStrings(ids, sqlInChunk) {
		query := "SELECT * FROM " + con.table() + " WHERE `_id` IN (" + sqlPlaceholders(len(chunk)) + ")"
		records, err := con.selectRecords(ctx, runner, query, stringsToArgs(chunk))
		if err != nil {
			return nil, err
		}

		for _, record := range records {
			byID[idString(record["_id"])] = record
		}
	}

	ordered := make([]datatype.DataMap, 0, len(ids))
	for _, id := range ids {
		if record, ok := byID[id]; ok {
			ordered = append(ordered, record)
		}
	}

	return ordered, nil
}

// inTransaction runs fn in a transaction, committing if it returns nil.
func (con *sqlConnection) inTransaction(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := con.client.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

func (con *sqlConnection) create(data datatype.DataMap) (*datatype.DataMap, error) {
	created, err := con.createMany([]datatype.DataMap{data})
	if err != nil || created == nil || len(*created) == 0 {
		return nil, err
	}

	return &(*created)[0], nil
}

func (con *sqlConnection) createMany(data []datatype.DataMap) (*[]datatype.DataMap, error) {
	if con.client == nil {
		return nil, errSQLNotConnected
	}

	if len(data) == 0 {
		return &[]datatype.DataMap{}, nil
	}

	ctx, cancel := con.opCtx(true)
	defer cancel()

	var created []datatype.DataMap
	err := con.inTransaction(ctx, func(tx *sql.Tx) error {
		ids, err := con.insertRecords(ctx, tx, data)
		if err != nil {
			return err
		}

		// Read back, so values have exactly the types and precision stored.
		created, err = con.recordsByID(ctx, tx, ids)
		return err
	})

	if err != nil {
		logger.Error("sqlConnection.create", err.Error())
		return nil, err
	}

	return &created, nil
}

// setClause builds "`a` = ?, `b` = ?" for the fields of data that are columns.
func (con *sqlConnection) setClause(data datatype.DataMap) (string, []interface{}, error) {
	model := con.query.Model

	keys := make([]string, 0, len(data))
	for key := range data {
		if key == "_id" || key == "id" {
			continue // the primary key doesn't change
		}
		if _, known := sqlFieldKind(model, key); known {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	sets := make([]string, len(keys))
	args := make([]interface{}, len(keys))
	for i, key := range keys {
		kind, _ := sqlFieldKind(model, key)
		arg, err := encodeSQLValue(kind, data[key])
		if err != nil {
			return "", nil, fmt.Errorf("%s: %w", key, err)
		}

		sets[i] = quoteIdent(key) + " = ?"
		args[i] = arg
	}

	return strings.Join(sets, ", "), args, nil
}

// update sets data on the first matching record (in the query's order) and
// returns it as updated, like the MongoDB backend's FindOneAndUpdate.
func (con *sqlConnection) update(data datatype.DataMap) (*datatype.DataMap, error) {
	if con.client == nil {
		return nil, errSQLNotConnected
	}

	where, whereArgs, err := con.whereClause()
	if err != nil {
		return nil, err
	}

	set, setArgs, err := con.setClause(data)
	if err != nil {
		return nil, err
	}

	ctx, cancel := con.opCtx(true)
	defer cancel()

	var updated *datatype.DataMap
	err = con.inTransaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT `_id` FROM "+con.table()+where+con.orderClause(con.columnOrder)+" LIMIT 1 FOR UPDATE", whereArgs...)
		if err != nil {
			return err
		}

		var id string
		found := rows.Next()
		if found {
			err = rows.Scan(&id)
		}
		rows.Close()
		if err != nil || !found {
			return err
		}

		if set != "" {
			args := append(append([]interface{}{}, setArgs...), id)
			if _, err := tx.ExecContext(ctx, "UPDATE "+con.table()+" SET "+set+" WHERE `_id` = ?", args...); err != nil {
				return err
			}
		}

		records, err := con.recordsByID(ctx, tx, []string{id})
		if err == nil && len(records) > 0 {
			updated = &records[0]
		}
		return err
	})

	if err != nil {
		logger.Error("sqlConnection.update", err.Error())
		return nil, err
	}

	return updated, nil
}

// updateMany sets data on every matching record and returns them as
// updated, like the MongoDB backend: the matching ids are read (and locked)
// first, so records the update moves out of the filter are still returned.
func (con *sqlConnection) updateMany(data datatype.DataMap) (*[]datatype.DataMap, error) {
	if con.client == nil {
		return nil, errSQLNotConnected
	}

	where, whereArgs, err := con.whereClause()
	if err != nil {
		return nil, err
	}

	set, setArgs, err := con.setClause(data)
	if err != nil {
		return nil, err
	}

	ctx, cancel := con.opCtx(true)
	defer cancel()

	updated := []datatype.DataMap{}
	err = con.inTransaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT `_id` FROM "+con.table()+where+con.orderClause(con.columnOrder)+" FOR UPDATE", whereArgs...)
		if err != nil {
			return err
		}

		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		if len(ids) == 0 {
			return nil
		}

		if set != "" {
			for _, chunk := range chunkStrings(ids, sqlInChunk) {
				args := append(append([]interface{}{}, setArgs...), stringsToArgs(chunk)...)
				if _, err := tx.ExecContext(ctx, "UPDATE "+con.table()+" SET "+set+" WHERE `_id` IN ("+sqlPlaceholders(len(chunk))+")", args...); err != nil {
					return err
				}
			}
		}

		updated, err = con.recordsByID(ctx, tx, ids)
		return err
	})

	if err != nil {
		logger.Error("sqlConnection.updateMany", err.Error())
		return nil, err
	}

	return &updated, nil
}

// sqlInChunk bounds the ids in one IN (...) list.
const sqlInChunk = 1000

func chunkStrings(values []string, size int) [][]string {
	var chunks [][]string
	for start := 0; start < len(values); start += size {
		end := start + size
		if end > len(values) {
			end = len(values)
		}
		chunks = append(chunks, values[start:end])
	}
	return chunks
}

func stringsToArgs(values []string) []interface{} {
	args := make([]interface{}, len(values))
	for i, v := range values {
		args[i] = v
	}
	return args
}

func sqlPlaceholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func (con *sqlConnection) delete() (interface{}, error) {
	if con.client == nil {
		return nil, errSQLNotConnected
	}

	where, args, err := con.whereClause()
	if err != nil {
		return nil, err
	}

	if where == "" {
		return nil, errors.New("filter is empty, not allowed to delete all document at once")
	}

	ctx, cancel := con.opCtx(true)
	defer cancel()

	result, err := con.client.ExecContext(ctx, "DELETE FROM "+con.table()+where, args...)
	if err != nil {
		logger.Error("sqlConnection.delete", err.Error())
		return nil, err
	}

	deleted, _ := result.RowsAffected()

	// Same shape as the MongoDB backend's result (callers read DeletedCount).
	return &mongo.DeleteResult{DeletedCount: deleted, Acknowledged: true}, nil
}
