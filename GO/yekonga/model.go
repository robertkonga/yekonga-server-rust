package yekonga

import (
	"sort"
	"strings"

	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/helper"
)

const TenantIDKey = "tenantId"

type DataModelFieldType string

const (
	DataModelID     DataModelFieldType = "id"
	DataModelString DataModelFieldType = "string"
	DataModelNumber DataModelFieldType = "number"
	DataModelFloat  DataModelFieldType = "float"
	DataModelDate   DataModelFieldType = "date"
	DataModelBool   DataModelFieldType = "bool"
	DataModelObject DataModelFieldType = "object"
	DataModelAny    DataModelFieldType = "any"
	DataModelArray  DataModelFieldType = "array"
	DataModelFile   DataModelFieldType = "file"
)

// Define custom middleware keys
type InputAction string

const (
	CreateInputAction InputAction = "create"
	UpdateInputAction InputAction = "update"
	ImportInputAction InputAction = "import"
	ActionInputAction InputAction = "action"
)

type DataModelFieldOptions struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type DataModelField struct {
	PrimaryKey   bool
	Name         string
	Kind         DataModelFieldType
	Required     bool
	Protected    bool
	IsArray      bool
	DefaultValue interface{}
	ForeignKey   DataModelFieldForeignKey
	Options      []DataModelFieldOptions
	ID           bool
	Index        bool
	Unique       bool
}

type DataModelFieldForeignKey struct {
	Model      *DataModel
	ModelName  string
	PrimaryKey string
	ForeignKey string
}

type DataModel struct {
	App            *YekongaData
	Config         *config.YekongaConfig
	DBConnect      *DatabaseConnections
	Name           string
	Class          string
	Collection     string
	Variable       string
	VariableSingle string
	VariablePlural string
	ForeignKey     string
	PrimaryKey     string
	PrimaryName    string
	HasTenant      bool
	Required       []string
	Protected      []string
	DateFields     []string
	OptionFields   []string
	FileFields     []string
	ValidFields    []string
	BooleanFields  []string
	NumberFields   []string
	FloatFields    []string
	ParentKeys     []string
	RelativeKeys   []string
	IDKeys         []string
	kinds          *fieldKinds // IDKeys etc. as sets; set by initialize
	Fields         map[string]DataModelField
	ParentFields   map[string]DataModelFieldForeignKey
	ChildrenFields map[string]DataModelFieldForeignKey
	DatabaseType   config.DatabaseType
}

func NewSystemModels(config *config.YekongaConfig, database *DatabaseStructure) map[string]*DataModel {
	var models map[string]*DataModel = map[string]*DataModel{}

	for k, v := range *database {
		model := newDataModel(config, k, v)
		models[model.Name] = model
	}

	for _, m := range models {
		for _, v := range m.ParentKeys {
			parentForeign := m.Fields[v].ForeignKey
			parentName := helper.GetParentRelativeName(
				parentForeign.ModelName,
				parentForeign.PrimaryKey,
				parentForeign.ForeignKey,
			)

			if _, ok := models[parentForeign.ModelName]; ok {
				parentForeign.Model = models[parentForeign.ModelName]
			}

			if _, ok := m.ParentFields[parentName]; !ok {
				m.ParentFields[parentName] = parentForeign
			}

			// --------------------------------

			childForeign := m.Fields[v].ForeignKey
			childName := helper.GetChildRelativeName(
				childForeign.ModelName,
				m.Name,
				childForeign.PrimaryKey,
				childForeign.ForeignKey,
			)
			if _, ok := models[m.Name]; ok {
				childForeign.Model = models[m.Name]
				childForeign.ModelName = childForeign.Model.Name
			}

			if _, ok := models[parentForeign.ModelName]; ok {
				models[parentForeign.ModelName].ChildrenFields[childName] = childForeign
			}
		}
	}

	return models
}

func SetSystemModelDBconnection(app *YekongaData, systemModels *map[string]*DataModel) {
	for _, m := range *systemModels {
		m.App = app
		m.DBConnect = app.dbConnect
	}
}

func SetDataGroups(models map[string]*DataModel) map[string]ResolverChartGroupData {
	values := make(map[string]ResolverChartGroupData)

	for _, v := range models {
		collection := v.Collection
		className := v.Name
		primaryKey := helper.ToVariable(helper.Singularize(collection) + "_id")
		fields := make([]string, 0, len(v.Fields))

		for k := range v.Fields {
			fields = append(fields, k)
		}

		values[primaryKey] = ResolverChartGroupData{
			Collection:  collection,
			ClassName:   className,
			PrimaryKey:  primaryKey,
			PrimaryName: v.PrimaryName,
			Fields:      fields,
		}
	}

	return values
}

func newDataModel(config *config.YekongaConfig, collection string, fields map[string]CollectionFieldConfig) *DataModel {
	model := DataModel{
		Config:       config,
		DatabaseType: config.Database.Kind,
	}

	model.initialize(collection, fields)

	return &model
}

func (m *DataModel) initialize(collection string, fields map[string]CollectionFieldConfig) {
	count := len(fields)

	m.Name = helper.ToCamelCase(helper.Singularize(collection))
	m.Class = helper.ToCamelCase(collection)
	m.Collection = helper.ToUnderscore(helper.Pluralize(collection))
	m.Variable = helper.ToVariable(collection)
	m.PrimaryKey = "_id"
	m.PrimaryName = ""
	m.VariableSingle = helper.ToVariable(helper.Singularize(collection))
	m.VariablePlural = helper.ToVariable(helper.Pluralize(collection))
	m.Fields = make(map[string]DataModelField)
	m.DateFields = make([]string, 0, count)
	m.OptionFields = make([]string, 0, count)
	m.FileFields = make([]string, 0, count)
	m.ValidFields = make([]string, 0, count)
	m.ParentKeys = make([]string, 0, count)
	m.RelativeKeys = make([]string, 0, count)
	m.IDKeys = make([]string, 0, count)
	m.Required = make([]string, 0, count)
	m.Protected = make([]string, 0, count)
	m.ParentFields = make(map[string]DataModelFieldForeignKey)
	m.ChildrenFields = make(map[string]DataModelFieldForeignKey)

	hasPrimaryName := false

	for k, v := range fields {
		if k == "id" {
			continue
		}

		if k == TenantIDKey {
			m.HasTenant = true
		}

		field := *m.getDataModelField(k, v)
		keyNames := []string{"name", "title", "label"}

		if helper.Contains(keyNames, field.Name) {
			m.PrimaryName = field.Name
			hasPrimaryName = true
		} else if !hasPrimaryName &&
			(strings.Contains(helper.ToUnderscore(field.Name), "name") ||
				strings.Contains(helper.ToUnderscore(field.Name), "title")) {
			m.PrimaryName = field.Name
			hasPrimaryName = true
		} else if helper.IsEmpty(m.PrimaryName) && field.Name != "_id" {
			m.PrimaryName = field.Name
		}

		m.Fields[k] = field
		m.ValidFields = append(m.ValidFields, k)

		if field.Required {
			m.Required = append(m.Required, k)
		}
		if field.Protected {
			m.Protected = append(m.Protected, k)
		}

		if field.Kind == DataModelDate {
			m.DateFields = append(m.DateFields, k)
		}
		if field.Kind == DataModelFile {
			m.FileFields = append(m.FileFields, k)
		}
		if field.Kind == DataModelBool {
			m.BooleanFields = append(m.BooleanFields, k)
		}
		if field.Kind == DataModelNumber {
			m.NumberFields = append(m.NumberFields, k)
		}
		if field.Kind == DataModelFloat {
			m.FloatFields = append(m.FloatFields, k)
		}
		if len(field.Options) > 0 {
			m.OptionFields = append(m.OptionFields, k)
		}

		if field.ID {
			m.IDKeys = append(m.IDKeys, k)
		}

		if helper.IsNotEmpty(field.ForeignKey.ModelName) {
			m.ParentKeys = append(m.ParentKeys, k)
			m.RelativeKeys = append(m.RelativeKeys, k)
		}
	}

	if !helper.Contains(m.ValidFields, "id") {
		k := "id"
		field := *m.getDataModelField(k, CollectionFieldConfig{Kind: "ID", DefaultValue: nil, Required: false})

		m.Fields[k] = field
		m.ValidFields = append(m.ValidFields, k)
	}

	sort.Strings(m.ValidFields)

	m.kinds = buildFieldKinds(m)
}

// fieldKinds holds the model's field-type lists as sets, for lookups done
// per field on every write.
type fieldKinds struct {
	id, date, number, float map[string]bool
}

func buildFieldKinds(m *DataModel) *fieldKinds {
	toSet := func(names []string) map[string]bool {
		set := make(map[string]bool, len(names))
		for _, name := range names {
			set[name] = true
		}
		return set
	}

	return &fieldKinds{
		id:     toSet(m.IDKeys),
		date:   toSet(m.DateFields),
		number: toSet(m.NumberFields),
		float:  toSet(m.FloatFields),
	}
}

// fieldKindSets returns the sets built with the model, or builds them for a
// model that wasn't made by initialize.
func (m *DataModel) fieldKindSets() *fieldKinds {
	if m.kinds != nil {
		return m.kinds
	}

	return buildFieldKinds(m)
}

func (m *DataModel) getDataModelField(name string, field CollectionFieldConfig) *DataModelField {
	return getDataModelField(name, field)
}

func (m *DataModel) Query() *DataModelQuery {
	return &DataModelQuery{
		Model: m,
		QueryContext: QueryContext{
			Params: make(map[string]interface{}),
		},
	}
}

func getDataModelField(name string, field CollectionFieldConfig) *DataModelField {
	var kind DataModelFieldType = DataModelString
	var isArray bool = false
	var defaultValue interface{} = field.DefaultValue
	var foreignKey DataModelFieldForeignKey
	var options = make([]DataModelFieldOptions, 0, len(field.Options))

	vi := strings.ToLower(strings.TrimSpace(field.Kind))
	// logger.Error("vi", name, "->", vi)
	if strings.Contains(vi, "[") && strings.Contains(vi, "]") {
		isArray = true
		vi = strings.ReplaceAll(vi, "[", "")
		vi = strings.ReplaceAll(vi, "]", "")
		vi = strings.TrimSpace(vi)
	}

	switch vi {
	case "id":
		kind = DataModelID
	case "date", "time", "datetime", "timestamp":
		kind = DataModelDate
	case "bool", "boolean":
		kind = DataModelBool
	case "float", "double", "decimal":
		kind = DataModelFloat
	case "int", "number", "integer", "digit":
		kind = DataModelNumber
	case "text", "string":
		kind = DataModelString
	case "array":
		isArray = true
		kind = DataModelArray
	case "any":
		kind = DataModelAny
	case "object":
		kind = DataModelObject
	case "url":
		kind = DataModelFile
	case "file":
		kind = DataModelFile
	}

	if isArray {
		if helper.IsArray(defaultValue) {
			defaultValue = helper.ToList[interface{}](defaultValue)
		} else {
			defaultValue = []interface{}{}
		}
	}

	if len(field.Options) > 0 {
		for _, opt := range field.Options {
			options = append(options, DataModelFieldOptions{
				Value: opt,
				Label: helper.ToTitle(opt),
			})
		}
	}

	if helper.IsNotEmpty(field.ForeignKey.Model) {
		parentCollection := helper.Singularize(field.ForeignKey.Model)
		parentKey := field.ForeignKey.Key

		if helper.IsEmpty(parentKey) {
			parentKey = "_id"
		}
		if parentKey == "id" {
			parentKey = "_id"
		}

		if helper.IsNotEmpty(parentCollection) {
			foreignKey = DataModelFieldForeignKey{
				ModelName:  helper.ToCamelCase(parentCollection),
				PrimaryKey: parentKey,
				ForeignKey: name,
			}
		}
	}

	return &DataModelField{
		PrimaryKey:   field.PrimaryKey,
		Name:         name,
		Kind:         kind,
		Required:     field.Required,
		Protected:    field.Protected,
		DefaultValue: defaultValue,
		ForeignKey:   foreignKey,
		Options:      options,
		IsArray:      isArray,
		ID:           kind == DataModelID,
		Index:        field.Index,
		Unique:       field.Unique,
	}
}
