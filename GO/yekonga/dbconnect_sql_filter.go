package yekonga

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
)

// sqlColumnName is the column a model field is stored in: the "id" field is
// the _id primary key, as in MongoDB.
func sqlColumnName(field string) string {
	if field == "id" {
		return "_id"
	}
	return field
}

// sqlFieldKind is the field type of a column, and whether the model has it.
func sqlFieldKind(model *DataModel, column string) (DataModelFieldType, bool) {
	if column == "_id" || column == "id" {
		return DataModelID, true
	}

	field, ok := model.Fields[column]
	return field.Kind, ok
}

// isJSONKind reports whether a field type is stored as a JSON column.
func isJSONKind(kind DataModelFieldType) bool {
	return kind == DataModelObject || kind == DataModelAny || kind == DataModelArray
}

var sqlJSONPathPart = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// sqlColumnExpr is the SQL expression for a filter or sort key: a column, a
// path inside a JSON column ("address.city"), or NULL for a field the table
// doesn't have, which then behaves like a missing field does in MongoDB.
func sqlColumnExpr(model *DataModel, key string) (expr string, kind DataModelFieldType, jsonColumn bool) {
	column := sqlColumnName(key)
	if kind, ok := sqlFieldKind(model, column); ok {
		return quoteIdent(column), kind, isJSONKind(kind)
	}

	if base, path, nested := strings.Cut(key, "."); nested {
		if kind, ok := sqlFieldKind(model, base); ok && isJSONKind(kind) {
			parts := strings.Split(path, ".")
			jsonPath := "$"
			for _, part := range parts {
				if !sqlJSONPathPart.MatchString(part) {
					return "NULL", DataModelString, false
				}
				if _, err := strconv.Atoi(part); err == nil {
					jsonPath += "[" + part + "]"
				} else {
					jsonPath += `."` + part + `"`
				}
			}

			return "JSON_UNQUOTE(JSON_EXTRACT(" + quoteIdent(base) + ", '" + jsonPath + "'))", DataModelString, false
		}
	}

	return "NULL", DataModelString, false
}

// encodeSQLValue converts a value for a column of the given type.
func encodeSQLValue(kind DataModelFieldType, value interface{}) (interface{}, error) {
	if value == nil {
		return nil, nil
	}

	if isJSONKind(kind) {
		encoded, err := json.Marshal(value)
		return string(encoded), err
	}

	switch v := value.(type) {
	case bson.ObjectID:
		return v.Hex(), nil
	case bson.DateTime:
		return v.Time().UTC(), nil
	case time.Time:
		return v.UTC(), nil
	case *time.Time:
		if v == nil {
			return nil, nil
		}
		return v.UTC(), nil
	}

	if driver.IsValue(value) {
		return value, nil
	}

	switch v := value.(type) {
	case int, int8, int16, int32, uint, uint8, uint16, uint32, uint64, float32:
		return v, nil // database/sql converts these
	}

	// Anything else in a plain column (e.g. a map) is stored as JSON text.
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

// decodeSQLValue converts a scanned value to what the MongoDB backend would
// return for a field of that type: ObjectIDs for IDs, int64/float64 for
// numbers, time.Time for dates, maps and slices for JSON.
func decodeSQLValue(kind DataModelFieldType, raw interface{}) interface{} {
	if raw == nil {
		return nil
	}

	if b, ok := raw.([]byte); ok {
		raw = string(b)
	}

	switch kind {
	case DataModelID:
		if s, ok := raw.(string); ok {
			if id, err := bson.ObjectIDFromHex(s); err == nil {
				return id
			}
		}

	case DataModelNumber:
		switch v := raw.(type) {
		case string:
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return n
			}
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return f
			}
		case float64:
			return v
		case int64:
			return v
		}

	case DataModelFloat:
		switch v := raw.(type) {
		case string:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return f
			}
		case int64:
			return float64(v)
		case float32:
			return float64(v)
		}

	case DataModelBool:
		switch v := raw.(type) {
		case int64:
			return v != 0
		case string:
			return v == "1" || strings.EqualFold(v, "true")
		}

	case DataModelDate:
		// bson.DateTime, as the MongoDB driver returns dates.
		switch v := raw.(type) {
		case time.Time:
			return bson.NewDateTimeFromTime(v)
		case string:
			for _, layout := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02", time.RFC3339Nano} {
				if t, err := time.ParseInLocation(layout, v, time.UTC); err == nil {
					return bson.NewDateTimeFromTime(t)
				}
			}
		}

	case DataModelObject, DataModelAny, DataModelArray:
		if s, ok := raw.(string); ok {
			var decoded interface{}
			if err := json.Unmarshal([]byte(s), &decoded); err == nil {
				return decoded
			}
		}
	}

	return raw
}

// scanSQLRows reads rows into records, decoding each column by its type.
func scanSQLRows(rows *sql.Rows, kindOf func(column string) (DataModelFieldType, bool)) ([]datatype.DataMap, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	records := make([]datatype.DataMap, 0)
	values := make([]interface{}, len(columns))
	pointers := make([]interface{}, len(columns))

	for rows.Next() {
		for i := range values {
			values[i] = nil
			pointers[i] = &values[i]
		}

		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}

		record := make(datatype.DataMap, len(columns)+3)
		for i, column := range columns {
			kind, known := kindOf(column)
			if !known {
				kind = DataModelString
			}
			record[column] = decodeSQLValue(kind, values[i])
		}

		records = append(records, record)
	}

	return records, rows.Err()
}

// sqlFilterBuilder translates a MongoDB filter, as mongodbConnection.where
// builds it, into a SQL condition with the same meaning. Where the two
// differ on missing values, MongoDB's meaning is kept: e.g. {$ne: x} also
// matches records where the field is NULL.
type sqlFilterBuilder struct {
	model *DataModel
	args  []interface{}
}

// build returns the condition for a filter, or "" for an empty one.
func (b *sqlFilterBuilder) build(filter datatype.DataMap) (string, error) {
	if len(filter) == 0 {
		return "", nil
	}

	return b.conjunction(filter)
}

func (b *sqlFilterBuilder) conjunction(filter datatype.DataMap) (string, error) {
	keys := make([]string, 0, len(filter))
	for k := range filter {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		var part string
		var err error

		switch key {
		case "$and", "$or", "$nor":
			part, err = b.logical(key, filter[key])
		default:
			part, err = b.field(key, filter[key])
		}

		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}

	if len(parts) == 0 {
		return "1=1", nil
	}

	return "(" + strings.Join(parts, " AND ") + ")", nil
}

func (b *sqlFilterBuilder) logical(op string, value interface{}) (string, error) {
	var subs []string
	for _, item := range reflectList(value) {
		sub, ok := asDataMap(item)
		if !ok {
			return "", fmt.Errorf("%s expects a list of filters", op)
		}

		part, err := b.conjunction(sub)
		if err != nil {
			return "", err
		}
		subs = append(subs, part)
	}

	switch op {
	case "$and":
		if len(subs) == 0 {
			return "1=1", nil
		}
		return "(" + strings.Join(subs, " AND ") + ")", nil
	case "$or":
		if len(subs) == 0 {
			return "1=0", nil
		}
		return "(" + strings.Join(subs, " OR ") + ")", nil
	default: // $nor
		if len(subs) == 0 {
			return "1=1", nil
		}
		return "NOT (" + strings.Join(subs, " OR ") + ")", nil
	}
}

func (b *sqlFilterBuilder) field(key string, condition interface{}) (string, error) {
	expr, kind, jsonColumn := sqlColumnExpr(b.model, key)

	ops, ok := asDataMap(condition)
	if !ok {
		ops = datatype.DataMap{"$eq": condition}
	}

	names := make([]string, 0, len(ops))
	for op := range ops {
		names = append(names, op)
	}
	sort.Strings(names)

	parts := make([]string, 0, len(names))
	for _, op := range names {
		if op == "$options" {
			continue // read with $regex
		}

		part, err := b.operator(expr, kind, jsonColumn, op, ops[op], ops)
		if err != nil {
			return "", fmt.Errorf("%s: %w", key, err)
		}
		parts = append(parts, part)
	}

	if len(parts) == 0 {
		return "1=1", nil
	}

	// SQL comparisons with NULL are unknown, not false, so NOT would drop
	// those rows. IS TRUE makes each condition true or false, as in MongoDB,
	// where e.g. {$nor: [{status: "paid"}]} matches a record without status.
	return "((" + strings.Join(parts, " AND ") + ") IS TRUE)", nil
}

func (b *sqlFilterBuilder) arg(kind DataModelFieldType, value interface{}) (string, error) {
	encoded, err := encodeSQLValue(kind, value)
	if err != nil {
		return "", err
	}

	b.args = append(b.args, encoded)
	return "?", nil
}

// jsonArg adds a value as JSON text, for JSON_CONTAINS.
func (b *sqlFilterBuilder) jsonArg(value interface{}) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}

	b.args = append(b.args, string(encoded))
	return "?", nil
}

func (b *sqlFilterBuilder) operator(expr string, kind DataModelFieldType, jsonColumn bool, op string, value interface{}, ops datatype.DataMap) (string, error) {
	switch op {
	case "$eq":
		return b.equal(expr, kind, jsonColumn, value)

	case "$ne":
		if value == nil {
			return expr + " IS NOT NULL", nil
		}
		equal, err := b.equal(expr, kind, jsonColumn, value)
		if err != nil {
			return "", err
		}
		return "(NOT (" + equal + ") OR " + expr + " IS NULL)", nil

	case "$gt", "$gte", "$lt", "$lte":
		comparison := map[string]string{"$gt": ">", "$gte": ">=", "$lt": "<", "$lte": "<="}[op]
		placeholder, err := b.arg(kind, value)
		if err != nil {
			return "", err
		}
		return expr + " " + comparison + " " + placeholder, nil

	case "$in":
		return b.in(expr, kind, jsonColumn, reflectList(value))

	case "$nin":
		in, err := b.in(expr, kind, jsonColumn, reflectList(value))
		if err != nil {
			return "", err
		}
		// MongoDB's $nin matches missing values, unless null is in the list.
		if containsNil(reflectList(value)) {
			return "NOT " + in, nil
		}
		return "(NOT " + in + " OR " + expr + " IS NULL)", nil

	case "$all":
		values := reflectList(value)
		if len(values) == 0 {
			return "1=0", nil
		}
		parts := make([]string, 0, len(values))
		for _, v := range values {
			part, err := b.equal(expr, kind, jsonColumn, v)
			if err != nil {
				return "", err
			}
			parts = append(parts, part)
		}
		return "(" + strings.Join(parts, " AND ") + ")", nil

	case "$exists":
		if exists, _ := value.(bool); exists {
			return expr + " IS NOT NULL", nil
		}
		return expr + " IS NULL", nil

	case "$not":
		sub, ok := asDataMap(value)
		if !ok {
			return "", fmt.Errorf("$not expects operators")
		}
		names := make([]string, 0, len(sub))
		for name := range sub {
			names = append(names, name)
		}
		sort.Strings(names)

		parts := make([]string, 0, len(names))
		for _, name := range names {
			if name == "$options" {
				continue
			}
			part, err := b.operator(expr, kind, jsonColumn, name, sub[name], sub)
			if err != nil {
				return "", err
			}
			parts = append(parts, part)
		}
		if len(parts) == 0 {
			return "1=1", nil
		}
		// A missing field doesn't match the condition, so it matches $not.
		return "(NOT (" + strings.Join(parts, " AND ") + ") OR " + expr + " IS NULL)", nil

	case "$regex":
		pattern, _ := value.(string)
		// Case-insensitive matching comes from the column's collation (the
		// default *_ci ones); MySQL 5.7 doesn't understand inline flags.
		pattern = strings.TrimPrefix(pattern, "(?i)")
		b.args = append(b.args, pattern)
		return expr + " REGEXP ?", nil
	}

	return "", fmt.Errorf("operator %s isn't supported on SQL databases", op)
}

// equal matches a value. On a JSON column it matches like MongoDB does for
// arrays: an element equal to the value, or the whole value when it's a list
// or object.
func (b *sqlFilterBuilder) equal(expr string, kind DataModelFieldType, jsonColumn bool, value interface{}) (string, error) {
	if value == nil {
		return expr + " IS NULL", nil
	}

	composite := isComposite(value)

	if jsonColumn {
		placeholder, err := b.jsonArg(value)
		if err != nil {
			return "", err
		}
		if !composite {
			return "JSON_CONTAINS(" + expr + ", " + placeholder + ")", nil
		}

		reverse, _ := b.jsonArg(value)
		return "(JSON_CONTAINS(" + expr + ", " + placeholder + ") AND JSON_CONTAINS(" + reverse + ", " + expr + "))", nil
	}

	if composite {
		return "1=0", nil // a plain column never holds a list or object
	}

	placeholder, err := b.arg(kind, value)
	if err != nil {
		return "", err
	}
	return expr + " = " + placeholder, nil
}

func (b *sqlFilterBuilder) in(expr string, kind DataModelFieldType, jsonColumn bool, values []interface{}) (string, error) {
	if len(values) == 0 {
		return "1=0", nil
	}

	if jsonColumn {
		parts := make([]string, 0, len(values))
		for _, v := range values {
			part, err := b.equal(expr, kind, jsonColumn, v)
			if err != nil {
				return "", err
			}
			parts = append(parts, part)
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	}

	var placeholders []string
	hasNil := false
	for _, v := range values {
		if v == nil {
			hasNil = true
			continue
		}
		placeholder, err := b.arg(kind, v)
		if err != nil {
			return "", err
		}
		placeholders = append(placeholders, placeholder)
	}

	var parts []string
	if len(placeholders) > 0 {
		parts = append(parts, expr+" IN ("+strings.Join(placeholders, ", ")+")")
	}
	if hasNil {
		parts = append(parts, expr+" IS NULL")
	}

	return "(" + strings.Join(parts, " OR ") + ")", nil
}

func asDataMap(value interface{}) (datatype.DataMap, bool) {
	switch v := value.(type) {
	case datatype.DataMap:
		return v, true
	case map[string]interface{}:
		return v, true
	case bson.M:
		return datatype.DataMap(v), true
	}

	if helper.IsMap(value) {
		return helper.ToDataMap(value), true
	}

	return nil, false
}

// reflectList returns the elements of any slice, or the value on its own.
func reflectList(value interface{}) []interface{} {
	if value == nil {
		return nil
	}

	if list, ok := value.([]interface{}); ok {
		return list
	}

	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array || isObjectID(value) {
		return []interface{}{value}
	}

	list := make([]interface{}, rv.Len())
	for i := range list {
		list[i] = rv.Index(i).Interface()
	}
	return list
}

func containsNil(values []interface{}) bool {
	for _, v := range values {
		if v == nil {
			return true
		}
	}
	return false
}

func isObjectID(value interface{}) bool {
	_, ok := value.(bson.ObjectID)
	return ok
}

// isComposite reports whether a value is a list or object, as opposed to a
// scalar. ObjectIDs are arrays in Go but scalars in the database.
func isComposite(value interface{}) bool {
	if value == nil || isObjectID(value) {
		return false
	}

	switch reflect.ValueOf(value).Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		_, isBytes := value.([]byte)
		return !isBytes
	}

	return false
}

// sqlGroup is the SELECT list of a grouped query, translated from the
// MongoDB $group the query builder (and the chart builder) describe.
type sqlGroup struct {
	selects []string
	keys    []string // group key aliases, in GROUP BY order
	kinds   map[string]DataModelFieldType
}

var mongoDateFormat = strings.NewReplacer("%V", "%v", "%G", "%x", "%L", "%f", "%M", "%i", "%S", "%s")

func buildSQLGroup(query *DataModelQuery) (*sqlGroup, error) {
	model := query.Model
	group := &sqlGroup{kinds: map[string]DataModelFieldType{}}

	addKey := func(alias string, value interface{}) error {
		expr, kind, err := sqlGroupExpr(model, value)
		if err != nil {
			return fmt.Errorf("group %s: %w", alias, err)
		}
		group.selects = append(group.selects, expr+" AS "+quoteIdent(alias))
		group.keys = append(group.keys, alias)
		group.kinds[alias] = kind
		return nil
	}

	for _, field := range query.groupBy {
		if err := addKey(field, "$"+field); err != nil {
			return nil, err
		}
	}

	aliases := make([]string, 0, len(query.groupByRaw))
	for alias := range query.groupByRaw {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	for _, alias := range aliases {
		value := query.groupByRaw[alias]

		if alias == "_id" {
			// {"_id": {"period": ..., "group": "$field"}}: each entry is a key.
			if keys, ok := asDataMap(value); ok {
				names := make([]string, 0, len(keys))
				for name := range keys {
					names = append(names, name)
				}
				sort.Strings(names)

				for _, name := range names {
					if err := addKey(name, keys[name]); err != nil {
						return nil, err
					}
				}
				continue
			}

			if err := addKey("_id", value); err != nil {
				return nil, err
			}
			continue
		}

		// {"total": {"$sum": "$amount"}}: an accumulator.
		accumulator, ok := asDataMap(value)
		if !ok || len(accumulator) != 1 {
			return nil, fmt.Errorf("group %s: expected one accumulator like {$sum: ...}", alias)
		}

		for op, operand := range accumulator {
			expr, kind, err := sqlAccumulator(model, op, operand)
			if err != nil {
				return nil, fmt.Errorf("group %s: %w", alias, err)
			}
			group.selects = append(group.selects, expr+" AS "+quoteIdent(alias))
			group.kinds[alias] = kind
		}
	}

	if len(group.selects) == 0 {
		return nil, fmt.Errorf("nothing to group by")
	}

	return group, nil
}

// sqlGroupExpr translates a MongoDB group key: "$field", or
// {"$dateToString": {"format": ..., "date": "$field"}}.
func sqlGroupExpr(model *DataModel, value interface{}) (string, DataModelFieldType, error) {
	if s, ok := value.(string); ok && strings.HasPrefix(s, "$") {
		expr, kind, _ := sqlColumnExpr(model, s[1:])
		return expr, kind, nil
	}

	if m, ok := asDataMap(value); ok {
		if spec, ok := asDataMap(m["$dateToString"]); ok {
			format, _ := spec["format"].(string)
			date, _ := spec["date"].(string)
			if !strings.HasPrefix(date, "$") {
				return "", "", fmt.Errorf("$dateToString needs a field")
			}

			expr, _, _ := sqlColumnExpr(model, date[1:])
			return "DATE_FORMAT(" + expr + ", '" + strings.ReplaceAll(mongoDateFormat.Replace(format), "'", "''") + "')", DataModelString, nil
		}
	}

	return "", "", fmt.Errorf("unsupported group key %v", value)
}

func sqlAccumulator(model *DataModel, op string, operand interface{}) (string, DataModelFieldType, error) {
	functions := map[string]string{"$sum": "SUM", "$avg": "AVG", "$min": "MIN", "$max": "MAX"}
	function, ok := functions[op]
	if !ok {
		return "", "", fmt.Errorf("unsupported accumulator %s", op)
	}

	field, isField := operand.(string)
	if !isField || !strings.HasPrefix(field, "$") {
		if op == "$sum" && helper.IsNumeric(operand) {
			// {$sum: 1} counts records.
			return fmt.Sprintf("SUM(%v)", helper.ToFloat(operand)), DataModelFloat, nil
		}
		return "", "", fmt.Errorf("%s needs a field", op)
	}

	expr, kind, _ := sqlColumnExpr(model, field[1:])
	if op == "$sum" || op == "$avg" {
		kind = DataModelFloat
	}

	if op == "$avg" {
		return sqlAverage(expr), kind, nil
	}

	return function + "(" + expr + ")", kind, nil
}

// sqlAverage is AVG in double precision, as MongoDB computes it. MySQL's AVG
// of an integer column is a DECIMAL rounded to 4 places.
func sqlAverage(expr string) string {
	return "(SUM(" + expr + ") * 1e0 / COUNT(" + expr + "))"
}
