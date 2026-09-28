package yekonga

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/helper/logger"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/mongo"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/mongo/options"
)

// modelIndex is one single-field index a model needs.
type modelIndex struct {
	field  string
	unique bool
}

// modelIndexes lists the indexes a model's queries rely on: tenantId (added
// to every tenant-scoped query), each foreign key (relation lookups and
// filters), and any field marked "index" or "unique" in database.json.
func modelIndexes(model *DataModel) []modelIndex {
	var indexes []modelIndex

	for name, field := range model.Fields {
		if name == "id" || name == "_id" || field.PrimaryKey {
			continue // _id is always indexed
		}

		switch {
		case field.Unique:
			indexes = append(indexes, modelIndex{field: name, unique: true})
		case field.Index, name == TenantIDKey, field.ForeignKey.ModelName != "", field.Kind == "ID":
			indexes = append(indexes, modelIndex{field: name})
		}
	}

	sort.Slice(indexes, func(i, j int) bool { return indexes[i].field < indexes[j].field })

	return indexes
}

// ensureIndexes creates any missing indexes from modelIndexes. It runs at
// startup in the background: creating an index that already exists is a
// no-op, and MongoDB builds new ones without blocking reads and writes.
// Turn it off with config database.disableAutoIndexes.
func (y *YekongaData) ensureIndexes() {
	if y.Config.Database.Kind != config.DBTypeMongodb || y.Config.Database.DisableAutoIndexes {
		return
	}

	y.createModelIndexes()
}

// createModelIndexes creates the indexes from modelIndexes for every model.
func (y *YekongaData) createModelIndexes() {
	client := y.dbConnect.mongodbClient
	if client == nil {
		return
	}

	db := client.Database(y.Config.Database.DatabaseName)
	created := 0

	for _, model := range y.models {
		collection := db.Collection(model.Collection)

		for _, index := range modelIndexes(model) {
			if ensureIndex(collection, index) {
				created++
			}
		}
	}

	if created > 0 {
		logger.Success("Database indexes checked", created)
	}
}

// ensureIndex creates one index and reports whether it exists afterwards.
// Each index is created on its own, so one that can't be built (e.g. a
// unique index over duplicate values) doesn't stop the rest.
func ensureIndex(collection *mongo.Collection, index modelIndex) bool {
	// Builds on large collections can take a while; this only bounds how
	// long startup keeps waiting, the build itself carries on server-side.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	opts := options.Index()
	if index.unique {
		opts = opts.SetUnique(true)
	}

	_, err := collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: index.field, Value: 1}},
		Options: opts,
	})
	if err == nil {
		return true
	}

	var commandErr mongo.CommandError
	if errors.As(err, &commandErr) && (commandErr.Code == 85 || commandErr.Code == 86) {
		// An index on this field already exists with other options (e.g. a
		// different name, or unique). Leave it as it is.
		return true
	}

	logger.Error("Could not create index", collection.Name()+"."+index.field, err.Error())
	return false
}
