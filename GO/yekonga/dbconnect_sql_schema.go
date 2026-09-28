package yekonga

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/helper/logger"
)

// sqlClient is the connection for the configured SQL kind, or nil.
func (y *YekongaData) sqlClient() *sql.DB {
	switch y.Config.Database.Kind {
	case config.DBTypeMysql:
		return y.dbConnect.mysqlClient
	case config.DBTypeSql:
		return y.dbConnect.sqlClient
	}

	return nil
}

// sqlColumnType is the column definition for a field. Strings that are
// indexed get VARCHAR(255): MySQL can't index a whole TEXT column.
func sqlColumnType(field DataModelField, indexed bool) string {
	switch field.Kind {
	case DataModelID:
		return "VARCHAR(64)"
	case DataModelNumber:
		return "BIGINT"
	case DataModelFloat:
		return "DOUBLE"
	case DataModelBool:
		return "BOOLEAN"
	case DataModelDate:
		return "DATETIME(3)"
	case DataModelObject, DataModelAny, DataModelArray:
		return "JSON"
	}

	if indexed {
		return "VARCHAR(255)"
	}

	return "TEXT"
}

// ensureSQLSchema creates missing tables and columns for every model from
// database.json. Existing columns are never changed or dropped. Indexes are
// then created in the background, as for MongoDB. Turn it off with config
// database.disableAutoMigrate.
func (y *YekongaData) ensureSQLSchema() {
	client := y.sqlClient()
	if client == nil || y.Config.Database.DisableAutoMigrate {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	for _, model := range y.sortedModels() {
		if err := ensureSQLTable(ctx, client, model); err != nil {
			logger.Error("Could not create table", model.Collection, err.Error())
		}
	}

	if !y.Config.Database.DisableAutoIndexes {
		go y.ensureSQLIndexes()
	}
}

func (y *YekongaData) sortedModels() []*DataModel {
	models := make([]*DataModel, 0, len(y.models))
	for _, model := range y.models {
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Collection < models[j].Collection })

	return models
}

func ensureSQLTable(ctx context.Context, client *sql.DB, model *DataModel) error {
	indexed := map[string]bool{}
	for _, index := range modelIndexes(model) {
		indexed[index.field] = true
	}

	names := make([]string, 0, len(model.Fields))
	for name := range model.Fields {
		if name != "id" && name != "_id" {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	definitions := []string{"`_id` VARCHAR(64) NOT NULL"}
	for _, name := range names {
		definitions = append(definitions, quoteIdent(name)+" "+sqlColumnType(model.Fields[name], indexed[name])+" NULL")
	}
	definitions = append(definitions, "PRIMARY KEY (`_id`)")

	create := "CREATE TABLE IF NOT EXISTS " + quoteIdent(model.Collection) + " (\n  " +
		strings.Join(definitions, ",\n  ") + "\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
	if _, err := client.ExecContext(ctx, create); err != nil {
		return err
	}

	// The table may already exist from an older database.json.
	existing, err := sqlExistingNames(ctx, client,
		"SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?",
		model.Collection)
	if err != nil {
		return err
	}

	for _, name := range names {
		if existing[strings.ToLower(name)] {
			continue
		}

		add := "ALTER TABLE " + quoteIdent(model.Collection) + " ADD COLUMN " + quoteIdent(name) + " " + sqlColumnType(model.Fields[name], indexed[name]) + " NULL"
		if _, err := client.ExecContext(ctx, add); err != nil {
			return err
		}
		logger.Success("Added column", model.Collection+"."+name)
	}

	return nil
}

// ensureSQLIndexes creates the indexes from modelIndexes that are missing.
func (y *YekongaData) ensureSQLIndexes() {
	client := y.sqlClient()
	if client == nil {
		return
	}

	for _, model := range y.sortedModels() {
		ensureSQLModelIndexes(client, model)
	}
}

func ensureSQLModelIndexes(client *sql.DB, model *DataModel) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	existing, err := sqlExistingNames(ctx, client,
		"SELECT DISTINCT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?",
		model.Collection)
	if err != nil {
		logger.Error("Could not read indexes", model.Collection, err.Error())
		return
	}

	for _, index := range modelIndexes(model) {
		if isJSONKind(model.Fields[index.field].Kind) {
			continue // JSON columns can't be indexed directly
		}

		name := "idx_" + index.field
		create := "CREATE INDEX "
		if index.unique {
			name = "uniq_" + index.field
			create = "CREATE UNIQUE INDEX "
		}
		if len(name) > 64 {
			name = name[:64]
		}

		if existing[strings.ToLower(name)] {
			continue
		}

		statement := create + quoteIdent(name) + " ON " + quoteIdent(model.Collection) + " (" + quoteIdent(index.field) + ")"
		if _, err := client.ExecContext(ctx, statement); err != nil {
			logger.Error("Could not create index", model.Collection+"."+index.field, err.Error())
		}
	}
}

// sqlExistingNames runs a query returning one name per row, lower-cased.
func sqlExistingNames(ctx context.Context, client *sql.DB, query string, args ...interface{}) (map[string]bool, error) {
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names[strings.ToLower(name)] = true
	}

	return names, rows.Err()
}
