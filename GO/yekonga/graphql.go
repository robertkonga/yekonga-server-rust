package yekonga

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/console"
	"github.com/robertkonga/yekonga-server-go/helper/logger"
	"github.com/robertkonga/yekonga-server-go/plugins/graphql"
)

type EmptyKey string

const (
	NULLValue EmptyKey = "NULL"
	nullValue EmptyKey = "null"
	NullValue EmptyKey = "Null"
)

type GraphqlQueryType string

const (
	QueryType    GraphqlQueryType = "Query"
	MutationType GraphqlQueryType = "Mutation"
)

// FilterValue represents filter criteria
type FilterValue struct {
	In                      []interface{} `json:"in"`
	GreaterThan             interface{}   `json:"greaterThan"`
	LessThan                interface{}   `json:"lessThan"`
	GreaterThanOrEqualTo    interface{}   `json:"greaterThanOrEqualTo"`
	LessThanOrEqualTo       interface{}   `json:"lessThanOrEqualTo"`
	EqualTo                 interface{}   `json:"equalTo"`
	NotEqualTo              interface{}   `json:"notEqualTo"`
	NotLessThan             interface{}   `json:"notLessThan"`
	NotLessThanOrEqualTo    interface{}   `json:"notLessThanOrEqualTo"`
	NotGreaterThan          interface{}   `json:"notGreaterThan"`
	NotGreaterThanOrEqualTo interface{}   `json:"notGreaterThanOrEqualTo"`
	MatchesRegex            interface{}   `json:"matchesRegex"`
	Options                 interface{}   `json:"options"`
}

type GraphqlSubscription struct {
	Client *Client
	Model  string
	Body   struct {
		Query         string
		OperationName string
		Variables     map[string]interface{}
	}
	Headers map[string]interface{}
}

type GraphqlActionResult struct {
	Data    interface{} `json:"data"`
	Success bool        `json:"success"`
	Status  bool        `json:"status"`
	Message string      `json:"message"`
}

type GraphqlAutoBuild struct {
	yekonga             *YekongaData
	GraphqlSubscription map[string]map[string]GraphqlSubscription
	Database            map[string]*DataModel
	EnumTypes           map[string]*graphql.Enum
	QueryTypes          map[string]*graphql.Object
	MutationTypes       map[string]*graphql.InputObject
	Schema              graphql.Schema
	AuthSchema          graphql.Schema
	mut                 sync.RWMutex

	// Parsed and validated queries, one cache per schema (see graphql_cache.go).
	documentCache     *lookupCache[*graphqlDocument]
	authDocumentCache *lookupCache[*graphqlDocument]
}

type CustomGraphqlResolver func(p graphql.ResolveParams) (interface{}, error)

type CustomGraphqlQuery struct {
	Name        string
	Model       any
	GraphqlType GraphqlQueryType
	IsList      bool
	Output      map[string]datatype.DataMap
	Args        graphql.FieldConfigArgument
	Resolve     CustomGraphqlResolver
}

func NewGraphqlAutoBuild(yekonga *YekongaData, database map[string]*DataModel) *GraphqlAutoBuild {
	autoBuild := GraphqlAutoBuild{
		yekonga:             yekonga,
		Database:            database,
		GraphqlSubscription: make(map[string]map[string]GraphqlSubscription),
		EnumTypes:           make(map[string]*graphql.Enum),
		QueryTypes:          make(map[string]*graphql.Object),
		MutationTypes:       make(map[string]*graphql.InputObject),
	}

	return &autoBuild
}

func (g *GraphqlAutoBuild) initialize() {
	g.initializeDefaultCustomQuery()

	for k, v := range g.Database {
		g.addModelEnumType(k, v)
	}

	for k, v := range g.Database {
		g.addWhereInputType(k, v)
		g.addOrderByInputType(k, v)
	}

	for k, v := range g.Database {
		g.addQueryType(k, v)
	}

	for k, v := range g.Database {
		g.addInputType(k, v)
	}

	for k, v := range g.Database {
		k = helper.ToCamelCase(helper.Singularize(k))

		// if k == "AuthUserPermission" {
		// 	console.Log(k, v.ParentKeys)
		// 	console.Log(k, helper.ToJson(v.ParentFields))
		// }

		for ki, vi := range v.ParentFields {
			var foreignKey string = vi.ForeignKey
			var targetKey string = vi.PrimaryKey

			if helper.IsNotEmpty(vi.Model) {
				fieldConfig := g.getRelativeQueryField(ki, vi.Model.Name, true, foreignKey, targetKey)
				g.QueryTypes[k].AddFieldConfig(ki, fieldConfig)

				whereInputA := helper.ToCamelCase("where_" + k + "_input")
				whereInputB := helper.ToCamelCase("where_" + vi.ModelName + "_input")
				dimensionWhereInput := helper.ToCamelCase("dimension_where_" + k + "_input")

				if f, ok := g.MutationTypes[whereInputA]; ok {
					f.AddFieldConfig(ki, &graphql.InputObjectFieldConfig{
						Type: g.MutationTypes[whereInputB],
					})
				} else {
					console.Error("ParentFields: whereInputA", whereInputA)
				}

				if f, ok := g.MutationTypes[dimensionWhereInput]; ok {
					f.AddFieldConfig(ki, &graphql.InputObjectFieldConfig{
						Type: g.MutationTypes[whereInputB],
					})
				} else {
					console.Error("ParentFields: dimensionWhereInput", dimensionWhereInput)
				}

			}
		}

		for ki, vi := range v.ChildrenFields {
			var foreignKey string = vi.ForeignKey
			var targetKey string = vi.PrimaryKey

			if helper.IsNotEmpty(vi.Model) {
				g.QueryTypes[k].AddFieldConfig(ki, g.getRelativeQueryField(ki, vi.Model.Name, false, foreignKey, targetKey))
				g.QueryTypes[k].AddFieldConfig(helper.ToVariable(helper.Singularize(ki)+"_paginate"), g.getQueryPaginationField(vi.Model.Name, foreignKey, targetKey))
				g.QueryTypes[k].AddFieldConfig(helper.ToVariable(helper.Singularize(ki)+"_summary"), g.getQuerySummaryField(vi.Model.Name, foreignKey, targetKey))

				g.MutationTypes[helper.ToCamelCase(k+"_input")].AddFieldConfig(helper.ToVariable(helper.Pluralize(ki)), &graphql.InputObjectFieldConfig{
					Type: graphql.NewList(g.MutationTypes[helper.ToCamelCase(vi.ModelName+"_input")]),
				})

				g.MutationTypes[helper.ToCamelCase("where_"+k+"_input")].AddFieldConfig(ki, &graphql.InputObjectFieldConfig{
					Type: g.MutationTypes[helper.ToCamelCase("where_"+vi.ModelName+"_input")],
				})

				g.MutationTypes[helper.ToCamelCase("dimension_where_"+k+"_input")].AddFieldConfig(ki, &graphql.InputObjectFieldConfig{
					Type: g.MutationTypes[helper.ToCamelCase("where_"+vi.ModelName+"_input")],
				})
			}
		}

		g.QueryTypes[k].AddFieldConfig(helper.ToVariable(helper.Singularize(k)+"_summary"), g.getQuerySummaryField(v.Name, "id", "id"))
	}

	s, err := graphql.NewSchema(graphql.SchemaConfig{
		Query:    g.GetQuery(),
		Mutation: g.GetMutation(),
	})

	if err != nil {
		logger.Error("GraphqlAutoBuild.initialize", err)
	}

	sa, err := graphql.NewSchema(graphql.SchemaConfig{
		Query:    g.GetAuthQuery(),
		Mutation: g.GetAuthMutation(),
	})

	if err != nil {
		logger.Error("GraphqlAutoBuild.initialize", err)
	}

	g.Schema = s
	g.AuthSchema = sa

	// Documents validated against the previous schemas may no longer be valid.
	g.documentCache = newGraphqlDocumentCache()
	g.authDocumentCache = newGraphqlDocumentCache()
}

func (g *GraphqlAutoBuild) initializeDefaultCustomQuery() {
	g.yekonga.SetCustomGraphql("CustomForm.result", false, false, make(map[string]datatype.DataMap), nil, func(params graphql.ResolveParams) (interface{}, error) {
		data := params.Source
		ctx, _ := params.Context.Value(RequestContextKey).(*RequestContext)
		inputType := helper.GetValueOfString(data, "type")
		options := helper.GetValueOfList(data, "options")
		advancedOptions := helper.GetValueOfBoolean(data, "options")

		console.Log("params.source", data)
		resultQuery := `
		query{result:customFormValues{
			userId,
			value,
			isAnonymous,
			correct,
			time,
			createdAt,
			option:customFormOption{id,label,name,value,correctValue},
		}}`

		if inputType == "ranking" || inputType == "multiple" || inputType == "choice" {
			resultQuery = `
		query{result:customFormOptions{
			label,
			name,
			value,
			summary:customFormValueSummary{count},
		}}`
		} else if (len(options) > 0 && !advancedOptions) || inputType == "rating" || inputType == "words" {
			resultQuery = `
		query{result:customFormValues(groupBy:[value]){
			label: value,
			summary:customFormValueSummary{count},
		}}`
		}
		result := g.yekonga.GraphQL(resultQuery, map[string]interface{}{}, ctx.Request, ctx.Response)
		outputData := helper.GetValueOf(result.Data, "result")
		console.Error("result.Data", outputData)
		// console.Error("result.Errors", result.Errors)
		// console.Error("result.AdvancedOptions", advancedOptions)

		var err error
		if helper.IsNotEmpty(result.Errors) {
			err = errors.New(helper.ToString(result.Errors))
		}

		return outputData, err
	})
}

func (g *GraphqlAutoBuild) GetQuery() *graphql.Object {
	var fields = make(graphql.Fields)

	for k := range g.Database {
		var foreignKey string
		var targetKey string
		k = helper.ToVariable(helper.Singularize(k))

		fields[k] = g.getQuerySingleField(k, foreignKey, targetKey)
		fields[helper.Pluralize(k)] = g.getQueryMultipleField(k, foreignKey, targetKey)
		fields[helper.ToVariable(k+"_paginate")] = g.getQueryPaginationField(k, foreignKey, targetKey)
		fields[helper.ToVariable(k+"_summary")] = g.getQuerySummaryField(k, foreignKey, targetKey)
		fields[helper.ToVariable("download_"+helper.Pluralize(k))] = g.getQueryDownloadField(k, foreignKey, targetKey)
	}

	g.addCustomQuery(&fields, QueryType)

	var queryType = graphql.NewObject(
		graphql.ObjectConfig{
			Name:   "Query",
			Fields: fields,
		})

	return queryType
}

func (g *GraphqlAutoBuild) GetMutation() *graphql.Object {
	var fields = make(graphql.Fields)

	for k := range g.Database {
		var foreignKey string
		var targetKey string
		k = helper.ToVariable(helper.Singularize(k))

		fields[helper.ToVariable("import_"+helper.Pluralize(k))] = g.getMutationImportField(k, foreignKey, targetKey)
		fields[helper.ToVariable("create_"+k)] = g.getMutationCreateField(k, foreignKey, targetKey)
		fields[helper.ToVariable("update_"+k)] = g.getMutationUpdateField(k, foreignKey, targetKey)
		fields[helper.ToVariable("delete_"+k)] = g.getMutationDeleteField(k, foreignKey, targetKey)
		fields[helper.ToVariable(k+"_action")] = g.getMutationActionField(k, foreignKey, targetKey)
	}

	g.addCustomQuery(&fields, MutationType)

	var mutationType = graphql.NewObject(
		graphql.ObjectConfig{
			Name:   "Mutation",
			Fields: fields,
		})

	return mutationType
}

func (g *GraphqlAutoBuild) addCustomQuery(fields *graphql.Fields, kind GraphqlQueryType) {
	// console.Log("g.yekonga.graphqlCustomQuery", g.yekonga.graphqlCustomQuery)

	for _, v := range g.yekonga.graphqlCustomQuery {
		insideModel := helper.IsNotEmpty(v.Model)

		if _, ok := (*fields)[v.Name]; ok && !insideModel {
			console.Error("Custom GraphqlQL " + v.Name + " already exists")
			return
		} else if insideModel {
			return
		}

		if v.GraphqlType == kind {
			var queryKind graphql.Output
			if len(v.Output) != 0 {
				output := graphql.Fields{}

				for k, o := range v.Output {
					obj, _ := helper.ConvertTo[CollectionFieldConfig](o)
					output[k] = g.getQueryField(k, getDataModelField(k, databaseCollectionFieldConfigFromMap(obj)))
				}

				queryKind = graphql.NewObject(graphql.ObjectConfig{
					Name:   v.Name,
					Fields: output,
				})
			} else {
				queryKind = ScalarAnyType
			}

			if v.IsList {
				(*fields)[v.Name] = &graphql.Field{
					Type:    graphql.NewList(queryKind),
					Args:    v.Args,
					Resolve: graphql.FieldResolveFn(v.Resolve),
				}
			} else {
				(*fields)[v.Name] = &graphql.Field{
					Type:    queryKind,
					Args:    v.Args,
					Resolve: graphql.FieldResolveFn(v.Resolve),
				}
			}
		}
	}
}

func (g *GraphqlAutoBuild) addCustomModelFieldQuery(modelName string, fields *graphql.Object, kind GraphqlQueryType) {
	for _, v := range g.yekonga.graphqlCustomQuery {
		insideModel := helper.IsNotEmpty(v.Model)

		if insideModel && modelName == v.Model {
			var output graphql.Output
			var queryKind graphql.Output
			if len(v.Output) != 0 {
				output := graphql.Fields{}

				for k, o := range v.Output {
					obj, _ := helper.ConvertTo[CollectionFieldConfig](o)
					output[k] = g.getQueryField(k, getDataModelField(k, databaseCollectionFieldConfigFromMap(obj)))
				}

				queryKind = graphql.NewObject(graphql.ObjectConfig{
					Name:   v.Name,
					Fields: output,
				})
			} else {
				queryKind = ScalarAnyType
			}

			if v.IsList {
				output = graphql.NewList(queryKind)
			} else {
				output = queryKind
			}
			if helper.IsNotEmpty(fields) {
				(*fields).AddFieldConfig(v.Name, &graphql.Field{
					Type:    output,
					Args:    v.Args,
					Resolve: graphql.FieldResolveFn(v.Resolve),
				})
			}
		}
	}
}

func (g *GraphqlAutoBuild) getQuerySingleField(collection string, foreignKey string, targetKey string) *graphql.Field {
	name := helper.ToCamelCase(helper.Singularize(collection))

	queryKind := g.QueryTypes[name]
	whereKind := g.MutationTypes[helper.ToCamelCase("where_"+name+"_input")]
	orderByKind := g.MutationTypes[helper.ToCamelCase("order_by_"+name+"_input")]
	groupByKind := graphql.NewList(g.EnumTypes[helper.ToCamelCase(""+name+"_enum_fields")])

	return &graphql.Field{
		Type:        queryKind,
		Description: fmt.Sprintf("Get %v", strings.ToTitle(name)),
		Args: graphql.FieldConfigArgument{
			"where": &graphql.ArgumentConfig{
				Type: whereKind,
			},
			"orderBy": &graphql.ArgumentConfig{
				Type: orderByKind,
			},
			"groupBy": &graphql.ArgumentConfig{
				Type: groupByKind,
			},
			"accessRole": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"route": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var model = g.yekonga.ModelQuery(name)
			status := g.setModelParams(model, &p, foreignKey, targetKey, true)

			if status == false {
				return nil, nil
			}

			if isRelationField(&p, foreignKey) {
				return g.resolveRelation(model, &p, foreignKey, targetKey, true)
			}

			data := model.FindOne(nil)

			if helper.IsNotEmpty(data) {
				dataMap := g.formateOutputData(model, *data, foreignKey, targetKey)
				dataMap["_params"] = p.Args

				return dataMap, nil
			}

			return nil, nil
		},
	}
}

func (g *GraphqlAutoBuild) getQueryMultipleField(collection string, foreignKey string, targetKey string) *graphql.Field {
	name := helper.ToCamelCase(helper.Singularize(collection))

	queryKind := g.QueryTypes[name]
	whereKind := g.MutationTypes[helper.ToCamelCase("where_"+name+"_input")]
	orderByKind := g.MutationTypes[helper.ToCamelCase("order_by_"+name+"_input")]
	groupByKind := graphql.NewList(g.EnumTypes[helper.ToCamelCase(""+name+"_enum_fields")])
	enumKind := g.EnumTypes[helper.ToCamelCase(""+name+"_enum_fields")]

	return &graphql.Field{
		Type:        graphql.NewList(queryKind),
		Description: fmt.Sprintf("List of %v", strings.ToTitle(helper.Pluralize(name))),
		Args: graphql.FieldConfigArgument{
			"where": &graphql.ArgumentConfig{
				Type: whereKind,
			},
			"orderBy": &graphql.ArgumentConfig{
				Type: orderByKind,
			},
			"groupBy": &graphql.ArgumentConfig{
				Type: groupByKind,
			},
			"limit": &graphql.ArgumentConfig{
				Type: graphql.Int,
			},
			"page": &graphql.ArgumentConfig{
				Type: graphql.Int,
			},
			"distinct": &graphql.ArgumentConfig{
				Type: graphql.NewList(enumKind),
			},
			"accessRole": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"route": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var model = g.yekonga.ModelQuery(name)
			g.setModelParams(model, &p, foreignKey, targetKey, false)

			if isRelationField(&p, foreignKey) {
				return g.resolveRelation(model, &p, foreignKey, targetKey, false)
			}

			data := model.Find(nil)

			g.loadRelatedData(data, model, &p, foreignKey, targetKey)

			return *data, nil
		},
	}
}

func (g *GraphqlAutoBuild) getQueryPaginationField(collection string, foreignKey string, targetKey string) *graphql.Field {
	name := helper.ToCamelCase(helper.Singularize(collection))

	queryKind := g.QueryTypes[helper.ToCamelCase(name+"_paginate")]
	whereKind := g.MutationTypes[helper.ToCamelCase("where_"+name+"_input")]
	orderByKind := g.MutationTypes[helper.ToCamelCase("order_by_"+name+"_input")]
	groupByKind := graphql.NewList(g.EnumTypes[helper.ToCamelCase(""+name+"_enum_fields")])
	enumKind := g.EnumTypes[helper.ToCamelCase(""+name+"_enum_fields")]

	return &graphql.Field{
		Type:        queryKind,
		Description: fmt.Sprintf("Get %v", name),
		Args: graphql.FieldConfigArgument{
			"where": &graphql.ArgumentConfig{
				Type: whereKind,
			},
			"orderBy": &graphql.ArgumentConfig{
				Type: orderByKind,
			},
			"groupBy": &graphql.ArgumentConfig{
				Type: groupByKind,
			},
			"distinct": &graphql.ArgumentConfig{
				Type: graphql.NewList(enumKind),
			},
			"limit": &graphql.ArgumentConfig{
				Type: graphql.Int,
			},
			"page": &graphql.ArgumentConfig{
				Type: graphql.Int,
			},
			"accessRole": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"route": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var model = g.yekonga.ModelQuery(name)
			g.setModelParams(model, &p, foreignKey, targetKey, false)

			data := model.Paginate(nil)
			if d, ok := (*data)["data"]; ok {
				if di, oki := d.(*[]datatype.DataMap); oki {
					g.loadRelatedData(di, model, &p, foreignKey, targetKey)
				}
			}

			return *data, nil
		},
	}
}

func (g *GraphqlAutoBuild) getQueryDownloadField(collection string, foreignKey string, targetKey string) *graphql.Field {
	name := helper.ToCamelCase(helper.Singularize(collection))

	queryKind := g.QueryTypes[helper.ToCamelCase("download_"+helper.Pluralize(name))]

	return &graphql.Field{
		Type:        queryKind,
		Description: fmt.Sprintf("Download %v", name),
		Args: graphql.FieldConfigArgument{
			"download": &graphql.ArgumentConfig{
				Type: GeneralDownloadTypeInput,
			},
			"downloadType": &graphql.ArgumentConfig{
				Type: DownloadTypeOptionsEnum,
			},
			"orientation": &graphql.ArgumentConfig{
				Type: OrientationOptionsEnum,
			},
			"accessRole": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"route": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"flatKeys": &graphql.ArgumentConfig{
				Type: graphql.NewList(graphql.String),
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			ctx, _ := p.Context.Value(RequestContextKey).(*RequestContext)
			data := make(map[string]interface{})

			flatKeys := helper.ToList[string](helper.GetValueOf(p.Args, "flatKeys"))
			orientation := helper.GetValueOfString(p.Args, "orientation")
			downloadType := helper.GetValueOfString(p.Args, "downloadType")
			downloadQuery := helper.GetValueOfString(p.Args, "download.query")
			downloadVariables := helper.GetValueOf(p.Args, "download.variables")

			if helper.IsNotEmpty(downloadQuery) {
				result := helper.ToMap[interface{}](g.yekonga.GraphQL(downloadQuery, map[string]interface{}{}, ctx.Request, ctx.Response))
				// console.Success("download", result)
				// console.Success("download", downloadQuery)
				listData := helper.GetMapValue(result, "data."+helper.Pluralize(collection))

				if listData == nil {
					listData = helper.GetMapValue(result, "data.list")
				}

				keysOrder := []string{}
				// keysOrder, _ := helper.ExtractKeysInOrder(downloadQuery)
				// console.Success("flatKeys", flatKeys)

				if listData != nil {
					filename := ""
					if downloadType == "EXCEL" {
						filename = "tmp/" + helper.GetHexString(24) + ".xlsx"

						helper.ConvertJSONArrayToExcel(listData, keysOrder, filename, flatKeys)
					} else {
						filename = "tmp/" + helper.GetHexString(24) + ".csv"

						helper.ConvertJSONArrayToCSV(listData, keysOrder, filename, flatKeys)
					}

					data["filename"] = path.Base(filename)
					data["url"] = helper.GetBaseUrl("download/"+path.Base(filename), ctx.Client.OriginDomain())
					data["type"] = downloadType
					data["size"] = 0
				}
			} else {
				console.Info("download", orientation, downloadType, downloadQuery, downloadVariables)
				data["error"] = "No download query provided"
			}

			return data, nil
		},
	}
}

func (g *GraphqlAutoBuild) getQuerySummaryField(collection string, foreignKey string, targetKey string) *graphql.Field {
	name := helper.ToCamelCase(helper.Singularize(collection))

	queryKind := g.QueryTypes[helper.ToCamelCase(name+"_summary")]
	whereKind := g.MutationTypes[helper.ToCamelCase("where_"+name+"_input")]
	orderByKind := g.MutationTypes[helper.ToCamelCase("order_by_"+name+"_input")]
	groupByKind := graphql.NewList(g.EnumTypes[helper.ToCamelCase(""+name+"_enum_fields")])
	enumKind := g.EnumTypes[helper.ToCamelCase(""+name+"_enum_fields")]

	return &graphql.Field{
		Type:        queryKind,
		Description: fmt.Sprintf("Get %v", name),
		Args: graphql.FieldConfigArgument{
			"where": &graphql.ArgumentConfig{
				Type: whereKind,
			},
			"orderBy": &graphql.ArgumentConfig{
				Type: orderByKind,
			},
			"groupBy": &graphql.ArgumentConfig{
				Type: groupByKind,
			},
			"distinct": &graphql.ArgumentConfig{
				Type: graphql.NewList(enumKind),
			},
			"accessRole": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"route": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			p.Args["relationForeignKey"] = foreignKey
			p.Args["relationTargetKey"] = targetKey
			var model = g.yekonga.ModelQuery(name)
			g.setModelParams(model, &p, foreignKey, targetKey, false)

			return p, nil
		},
	}
}

func (g *GraphqlAutoBuild) getQueryCountField(collection string, foreignKey string, targetKey string) *graphql.Field {
	name := helper.ToCamelCase(helper.Singularize(collection))

	whereKind := g.MutationTypes[helper.ToCamelCase("where_"+name+"_input")]
	enumKind := g.EnumTypes[helper.ToCamelCase(""+name+"_enum_fields")]

	return &graphql.Field{
		Type:        graphql.Float,
		Description: fmt.Sprintf("Get %v", name),
		Args: graphql.FieldConfigArgument{
			"where": &graphql.ArgumentConfig{
				Type: whereKind,
			},
			"distinct": &graphql.ArgumentConfig{
				Type: graphql.NewList(enumKind),
			},
			"accessRole": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"route": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			// Per-call copies: the relation keys below are set per request, and
			// writing the captured variables would leak them into other requests.
			foreignKey, targetKey := foreignKey, targetKey

			var parent datatype.DataMap
			if pp, ok := p.Source.(graphql.ResolveParams); ok {
				if helper.IsMap(pp.Source) {
					parent = helper.ToDataMap(pp.Source)
				}

				if pp.Args != nil {
					foreignKey = helper.GetValueOfString(pp.Args, "relationForeignKey")
					targetKey = helper.GetValueOfString(pp.Args, "relationTargetKey")
				}
			}

			model := g.yekonga.ModelQuery(name)
			if helper.IsNotEmpty(parent) {
				if id, ok := parent["id"]; ok {
					if helper.IsMap(id) {
						idMap := helper.ToDataMap(id)
						for k, v := range idMap {
							model.Where(helper.ToVariable(k), v)
						}
					}
				}
			}

			g.setModelParams(model, &p, foreignKey, targetKey, false)

			return model.Count(nil), nil
		},
	}
}

func (g *GraphqlAutoBuild) getQuerySumField(collection string, foreignKey string, targetKey string) *graphql.Field {
	name := helper.ToCamelCase(helper.Singularize(collection))

	enumKind := g.EnumTypes[helper.ToCamelCase(""+name+"_enum_fields")]
	whereKind := g.MutationTypes[helper.ToCamelCase("where_"+name+"_input")]

	return &graphql.Field{
		Type:        graphql.Float,
		Description: fmt.Sprintf("Get %v", name),
		Args: graphql.FieldConfigArgument{
			"targetKey": &graphql.ArgumentConfig{
				Type: enumKind,
			},
			"distinct": &graphql.ArgumentConfig{
				Type: graphql.NewList(enumKind),
			},
			"productOf": &graphql.ArgumentConfig{
				Type: graphql.NewList(enumKind),
			},
			"formula": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"where": &graphql.ArgumentConfig{
				Type: whereKind,
			},
			"accessRole": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"route": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			// Per-call copies: the relation keys below are set per request, and
			// writing the captured variables would leak them into other requests.
			foreignKey, targetKey := foreignKey, targetKey

			var parent datatype.DataMap
			if pp, ok := p.Source.(graphql.ResolveParams); ok {
				if helper.IsMap(pp.Source) {
					parent = helper.ToDataMap(pp.Source)
				}

				if pp.Args != nil {
					foreignKey = helper.GetValueOfString(pp.Args, "relationForeignKey")
					targetKey = helper.GetValueOfString(pp.Args, "relationTargetKey")
				}
			}

			model := g.yekonga.ModelQuery(name)
			if helper.IsNotEmpty(parent) {
				if id, ok := parent["id"]; ok {
					if helper.IsMap(id) {
						idMap := helper.ToDataMap(id)
						for k, v := range idMap {
							model.Where(helper.ToVariable(k), v)
						}
					}
				}
			}

			g.setModelParams(model, &p, foreignKey, targetKey, false)
			targetField := g.getTargetField(p.Args)

			return model.Sum(targetField, nil), nil
		},
	}
}

func (g *GraphqlAutoBuild) getQueryMaxField(collection string, foreignKey string, targetKey string) *graphql.Field {
	name := helper.ToCamelCase(helper.Singularize(collection))

	enumKind := g.EnumTypes[helper.ToCamelCase(""+name+"_enum_fields")]
	whereKind := g.MutationTypes[helper.ToCamelCase("where_"+name+"_input")]

	return &graphql.Field{
		Type:        ScalarAnyType,
		Description: fmt.Sprintf("Get %v", name),
		Args: graphql.FieldConfigArgument{
			"targetKey": &graphql.ArgumentConfig{
				Type: enumKind,
			},
			"distinct": &graphql.ArgumentConfig{
				Type: graphql.NewList(enumKind),
			},
			"where": &graphql.ArgumentConfig{
				Type: whereKind,
			},
			"accessRole": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"route": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			// Per-call copies: the relation keys below are set per request, and
			// writing the captured variables would leak them into other requests.
			foreignKey, targetKey := foreignKey, targetKey

			var parent datatype.DataMap
			if pp, ok := p.Source.(graphql.ResolveParams); ok {
				if helper.IsMap(pp.Source) {
					parent = helper.ToDataMap(pp.Source)
				}

				if pp.Args != nil {
					foreignKey = helper.GetValueOfString(pp.Args, "relationForeignKey")
					targetKey = helper.GetValueOfString(pp.Args, "relationTargetKey")
				}
			}

			model := g.yekonga.ModelQuery(name)
			if helper.IsNotEmpty(parent) {
				if id, ok := parent["id"]; ok {
					if helper.IsMap(id) {
						idMap := helper.ToDataMap(id)
						for k, v := range idMap {
							model.Where(helper.ToVariable(k), v)
						}
					}
				}
			}
			g.setModelParams(model, &p, foreignKey, targetKey, false)
			targetField := g.getTargetField(p.Args)

			return model.Max(targetField, nil), nil
		},
	}
}

func (g *GraphqlAutoBuild) getQueryMinField(collection string, foreignKey string, targetKey string) *graphql.Field {
	name := helper.ToCamelCase(helper.Singularize(collection))

	enumKind := g.EnumTypes[helper.ToCamelCase(""+name+"_enum_fields")]
	whereKind := g.MutationTypes[helper.ToCamelCase("where_"+name+"_input")]

	return &graphql.Field{
		Type:        ScalarAnyType,
		Description: fmt.Sprintf("Get %v", name),
		Args: graphql.FieldConfigArgument{
			"targetKey": &graphql.ArgumentConfig{
				Type: enumKind,
			},
			"distinct": &graphql.ArgumentConfig{
				Type: graphql.NewList(enumKind),
			},
			"where": &graphql.ArgumentConfig{
				Type: whereKind,
			},
			"accessRole": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"route": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			// Per-call copies: the relation keys below are set per request, and
			// writing the captured variables would leak them into other requests.
			foreignKey, targetKey := foreignKey, targetKey

			var parent datatype.DataMap
			if pp, ok := p.Source.(graphql.ResolveParams); ok {
				if helper.IsMap(pp.Source) {
					parent = helper.ToDataMap(pp.Source)
				}

				if pp.Args != nil {
					foreignKey = helper.GetValueOfString(pp.Args, "relationForeignKey")
					targetKey = helper.GetValueOfString(pp.Args, "relationTargetKey")
				}
			}

			model := g.yekonga.ModelQuery(name)
			if helper.IsNotEmpty(parent) {
				if id, ok := parent["id"]; ok {
					if helper.IsMap(id) {
						idMap := helper.ToDataMap(id)
						for k, v := range idMap {
							model.Where(helper.ToVariable(k), v)
						}
					}
				}
			}
			g.setModelParams(model, &p, foreignKey, targetKey, false)
			targetField := g.getTargetField(p.Args)

			return model.Min(targetField, nil), nil
		},
	}
}

func (g *GraphqlAutoBuild) getQueryAverageField(collection string, foreignKey string, targetKey string) *graphql.Field {
	name := helper.ToCamelCase(helper.Singularize(collection))

	enumKind := g.EnumTypes[helper.ToCamelCase(""+name+"_enum_fields")]
	whereKind := g.MutationTypes[helper.ToCamelCase("where_"+name+"_input")]

	return &graphql.Field{
		Type:        graphql.Float,
		Description: fmt.Sprintf("Get %v", name),
		Args: graphql.FieldConfigArgument{
			"targetKey": &graphql.ArgumentConfig{
				Type: enumKind,
			},
			"distinct": &graphql.ArgumentConfig{
				Type: graphql.NewList(enumKind),
			},
			"where": &graphql.ArgumentConfig{
				Type: whereKind,
			},
			"accessRole": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"route": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			// Per-call copies: the relation keys below are set per request, and
			// writing the captured variables would leak them into other requests.
			foreignKey, targetKey := foreignKey, targetKey

			var parent datatype.DataMap
			if pp, ok := p.Source.(graphql.ResolveParams); ok {
				if helper.IsMap(pp.Source) {
					parent = helper.ToDataMap(pp.Source)
				}

				if pp.Args != nil {
					foreignKey = helper.GetValueOfString(pp.Args, "relationForeignKey")
					targetKey = helper.GetValueOfString(pp.Args, "relationTargetKey")
				}
			}

			model := g.yekonga.ModelQuery(name)
			if helper.IsNotEmpty(parent) {
				if id, ok := parent["id"]; ok {
					if helper.IsMap(id) {
						idMap := helper.ToDataMap(id)
						for k, v := range idMap {
							model.Where(helper.ToVariable(k), v)
						}
					}
				}
			}
			g.setModelParams(model, &p, foreignKey, targetKey, false)
			targetField := g.getTargetField(p.Args)

			return model.Average(targetField, nil), nil
		},
	}
}

func (g *GraphqlAutoBuild) getQueryGraphField(collection string, foreignKey string, targetKey string) *graphql.Field {
	name := helper.ToCamelCase(helper.Singularize(collection))

	enumKind := g.EnumTypes[helper.ToCamelCase(""+name+"_enum_fields")]
	structureEnumKind := g.EnumTypes[helper.ToCamelCase(""+name+"_structured_enum_fields")]

	whereKind := g.MutationTypes[helper.ToCamelCase("where_"+name+"_input")]
	orderByKind := g.MutationTypes[helper.ToCamelCase("order_by_"+name+"_input")]
	argsParams := graphql.FieldConfigArgument{
		"where": &graphql.ArgumentConfig{
			Type: whereKind,
		},
		"orderBy": &graphql.ArgumentConfig{
			Type: orderByKind,
		},
		"targetKey": &graphql.ArgumentConfig{
			Type: enumKind,
		},
		"type": &graphql.ArgumentConfig{
			Type: GeneralGraphOptionsEnum,
		},
		"total": &graphql.ArgumentConfig{
			Type: GeneralTotalOptionsEnum,
		},
		"runningCalculation": &graphql.ArgumentConfig{
			Type: GeneralTotalOptionsEnum,
		},
		"periodicity": &graphql.ArgumentConfig{
			Type: GeneralPeriodicityEnum,
		},
		"dimension": &graphql.ArgumentConfig{
			Type: graphql.NewNonNull(structureEnumKind),
		},
		"dimensionSort": &graphql.ArgumentConfig{
			Type: graphql.NewList(orderByKind),
		},
		"dimensionBreakdown": &graphql.ArgumentConfig{
			Type: structureEnumKind,
		},
		"dimensionBreakdownSort": &graphql.ArgumentConfig{
			Type: graphql.NewList(orderByKind),
		},
		"dimensionBreakdownPeriodicity": &graphql.ArgumentConfig{
			Type: GeneralPeriodicityEnum,
		},
		"dimensionPeriodicity": &graphql.ArgumentConfig{
			Type: GeneralPeriodicityEnum,
		},
		"metric": &graphql.ArgumentConfig{
			Type: structureEnumKind,
		},
		"metrics": &graphql.ArgumentConfig{
			Type: graphql.NewList(structureEnumKind),
		},
		"from": &graphql.ArgumentConfig{
			Type: ScalarDateType,
		},
		"to": &graphql.ArgumentConfig{
			Type: ScalarDateType,
		},
		"accessRole": &graphql.ArgumentConfig{
			Type: graphql.String,
		},
		"route": &graphql.ArgumentConfig{
			Type: graphql.String,
		},
	}

	if model, ok := Server.models[name]; ok {
		if len(model.ParentFields) > 0 || len(model.ChildrenFields) > 0 {
			valid := false

			for _, v := range model.ParentFields {
				if helper.IsNotEmpty(v.Model) {
					valid = true
					break
				}
			}

			for _, v := range model.ChildrenFields {
				if helper.IsNotEmpty(v.Model) {
					valid = true
					break
				}
			}

			if valid {
				dimensionWhereKind := g.MutationTypes[helper.ToCamelCase("dimension_where_"+name+"_input")]
				argsParams["dimensionWhere"] = &graphql.ArgumentConfig{
					Type: dimensionWhereKind,
				}

				argsParams["dimensionBreakdownWhere"] = &graphql.ArgumentConfig{
					Type: dimensionWhereKind,
				}
			}
		}
	}

	return &graphql.Field{
		Type:        GraphDataType,
		Description: fmt.Sprintf("Get %v", name),
		Args:        argsParams,
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			// Per-call copies: the relation keys below are set per request, and
			// writing the captured variables would leak them into other requests.
			foreignKey, targetKey := foreignKey, targetKey

			var parent datatype.DataMap
			if pp, ok := p.Source.(graphql.ResolveParams); ok {
				if helper.IsMap(pp.Source) {
					parent = helper.ToDataMap(pp.Source)
				}

				if pp.Args != nil {
					foreignKey = helper.GetValueOfString(pp.Args, "relationForeignKey")
					targetKey = helper.GetValueOfString(pp.Args, "relationTargetKey")
				}
			}

			model := g.yekonga.ModelQuery(name)
			if helper.IsNotEmpty(parent) {
				if id, ok := parent["id"]; ok {
					if helper.IsMap(id) {
						idMap := helper.ToDataMap(id)
						for k, v := range idMap {
							model.Where(helper.ToVariable(k), v)
						}
					}
				}
			}
			g.setModelParams(model, &p, foreignKey, targetKey, false)

			localWhere := helper.ToMap[interface{}](p.Args["where"])

			result := model.Graph(localWhere, &p)
			if err, ok := result.(error); ok {
				return nil, err
			}

			return result, nil
		},
	}
}

func (g *GraphqlAutoBuild) getMutationCreateField(collection string, foreignKey string, targetKey string) *graphql.Field {
	name := helper.ToCamelCase(helper.Singularize(collection))

	inputKind := g.MutationTypes[helper.ToCamelCase(name+"_input")]
	resultKind := g.QueryTypes[helper.ToCamelCase("create_"+name+"_input_result_output")]

	return &graphql.Field{
		Type: resultKind,
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: inputKind,
			},
			"accessRole": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"route": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			ctx, _ := p.Context.Value(RequestContextKey).(*RequestContext)
			var data = helper.ToDataMap(g.getInputData(p.Args))
			var model = g.yekonga.ModelQuery(name)
			var result datatype.DataMap = make(datatype.DataMap)

			result["success"] = false
			result["status"] = false
			result["message"] = "Fail"
			result["data"] = nil

			g.setModelParams(model, &p, foreignKey, targetKey, false)
			created := model.Create(data)

			for ki, vi := range model.Model.ChildrenFields {
				var foreignKey string = vi.ForeignKey
				var targetKey string = vi.PrimaryKey
				childData := helper.GetValueOf(data, helper.ToVariable(ki))

				if helper.IsNotEmpty(childData) {
					if helper.IsArray(childData) {
						childList := helper.ToList[interface{}](childData)

						for i, cd := range childList {
							if helper.IsMap(cd) {
								cdMap := helper.ToDataMap(cd)
								cdMap[foreignKey] = helper.GetValueOf(created, targetKey)
								childList[i] = cdMap
							}
						}

						// console.Info("childList", childList)
						childModel := vi.Model.Query()
						if ctx != nil {
							childModel.SetRequestContext(ctx)
						}
						childModel.Import(childList, []string{})
					}
				}
			}

			id := helper.GetValueOf(created, "_id")
			if id != nil {
				result["success"] = true
				result["status"] = true
				result["message"] = "Success"
				result["data"] = helper.ToMap[interface{}](created)
			}
			// console.Success("create", result)

			return result, nil
		},
	}
}

func (g *GraphqlAutoBuild) getMutationImportField(collection string, foreignKey string, targetKey string) *graphql.Field {
	name := helper.ToCamelCase(helper.Singularize(collection))

	enumKind := g.EnumTypes[helper.ToCamelCase(""+name+"_enum_fields")]
	inputKind := g.MutationTypes[helper.ToCamelCase(name+"_input")]
	resultKind := g.QueryTypes[helper.ToCamelCase("import_"+name+"_input_result_output")]

	return &graphql.Field{
		Type: resultKind,
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: graphql.NewList(inputKind),
			},
			"uniqueKeys": &graphql.ArgumentConfig{
				Type: graphql.NewList(enumKind),
			},
			"accessRole": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"route": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			ctx, _ := p.Context.Value(RequestContextKey).(*RequestContext)
			var data = helper.ToList[interface{}](g.getInputData(p.Args))
			var uniqueKeys []string = []string{}
			var model = g.yekonga.ModelQuery(name)

			if p.Args["uniqueKeys"] != nil && helper.IsArray(p.Args["uniqueKeys"]) {
				uniqueKeys = helper.ToList[string](p.Args["uniqueKeys"])
			}

			g.setModelParams(model, &p, foreignKey, targetKey, false)
			imported := model.Import(data, uniqueKeys)
			savedData := helper.GetValueOf(imported, "data")

			if helper.IsArray(savedData) {
				savedDataList := helper.ToList[interface{}](savedData)

				for _, sd := range savedDataList {
					if helper.IsMap(sd) {
						var childData interface{}
						var childDataRaw interface{}
						sdMap := helper.ToDataMap(sd)

						for _, v := range data {
							if helper.IsMap(v) {
								vMap := helper.ToDataMap(v)

								if importInputMatches(sdMap, vMap, uniqueKeys) {
									childDataRaw = vMap
									break
								}
							}

						}

						for ki, vi := range model.Model.ChildrenFields {
							var foreignKey string = vi.ForeignKey
							var targetKey string = vi.PrimaryKey
							childData = helper.GetValueOf(childDataRaw, helper.ToVariable(ki))

							if helper.IsNotEmpty(childData) {
								if helper.IsArray(childData) {
									childList := helper.ToList[interface{}](childData)

									for j, cd := range childList {
										if helper.IsMap(cd) {
											cdMap := helper.ToDataMap(cd)
											cdMap[foreignKey] = helper.GetValueOf(sdMap, targetKey)
											childList[j] = cdMap
										}
									}

									// console.Info("Import.childList", childList)
									childModel := vi.Model.Query()
									if ctx != nil {
										childModel.SetRequestContext(ctx)
									}
									childModel.Import(childList, []string{})
								}
							}
						}
					}
				}
			}

			console.Success("imported", imported)

			return imported, nil
		},
	}
}

// importInputMatches reports whether saved (a record Import returned) came
// from input, by comparing uniqueKeys, or the record id when there are none.
// Without any key to compare there's no match: before, every record matched
// the first input, which would attach its children to every imported parent.
func importInputMatches(saved datatype.DataMap, input datatype.DataMap, uniqueKeys []string) bool {
	keys := uniqueKeys
	if len(keys) == 0 {
		keys = []string{"id"}
	}

	for _, key := range keys {
		want := helper.GetValueOf(input, key)
		if want == nil && (key == "id" || key == "_id") {
			want = helper.GetValueOf(input, "_id")
			if want == nil {
				want = helper.GetValueOf(input, "id")
			}
		}

		if want == nil {
			return false
		}

		// An ObjectID and its hex string are the same id.
		if idString(helper.GetValueOf(saved, key)) != idString(want) {
			return false
		}
	}

	return true
}

func (g *GraphqlAutoBuild) getMutationUpdateField(collection string, foreignKey string, targetKey string) *graphql.Field {
	name := helper.ToCamelCase(helper.Singularize(collection))

	inputKind := g.MutationTypes[helper.ToCamelCase(name+"_input")]
	resultKind := g.QueryTypes[helper.ToCamelCase("update_"+name+"_input_result_output")]
	whereKind := g.MutationTypes[helper.ToCamelCase("where_"+name+"_input")]

	return &graphql.Field{
		Type: resultKind,
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: inputKind,
			},
			"where": &graphql.ArgumentConfig{
				Type: whereKind,
			},
			"accessRole": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"route": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			ctx, _ := p.Context.Value(RequestContextKey).(*RequestContext)
			var data = helper.ToDataMap(g.getInputData(p.Args))
			var model = g.yekonga.ModelQuery(name)
			var result datatype.DataMap = make(datatype.DataMap)
			g.setModelParams(model, &p, foreignKey, targetKey, false)

			result["success"] = false
			result["status"] = false
			result["message"] = "Fail"
			result["data"] = nil

			updated := model.Update(data, nil)
			// console.Log("updated", model.where)
			// console.Log("updated", updated)
			id := helper.GetValueOf(updated, "_id")

			for ki, vi := range model.Model.ChildrenFields {
				var foreignKey string = vi.ForeignKey
				var targetKey string = vi.PrimaryKey
				childData := helper.GetValueOf(data, helper.ToVariable(ki))

				if helper.IsNotEmpty(childData) {
					if helper.IsArray(childData) {
						childList := helper.ToList[interface{}](childData)

						for i, cd := range childList {
							if helper.IsMap(cd) {
								cdMap := helper.ToDataMap(cd)
								cdMap[foreignKey] = helper.GetValueOf(updated, targetKey)
								childList[i] = cdMap
							}
						}

						childModel := vi.Model.Query()
						if ctx != nil {
							childModel.SetRequestContext(ctx)
						}
						childModel.Import(childList, []string{})
					}
				}
			}

			if id != nil {
				result["success"] = true
				result["status"] = true
				result["message"] = "Success"
				result["data"] = helper.ToMap[interface{}](updated)
			}

			// console.Success("updated", result)

			return result, nil
		},
	}
}

func (g *GraphqlAutoBuild) getMutationDeleteField(collection string, foreignKey string, targetKey string) *graphql.Field {
	name := helper.ToCamelCase(helper.Singularize(collection))

	whereKind := g.MutationTypes[helper.ToCamelCase("where_"+name+"_input")]
	resultKind := g.QueryTypes[helper.ToCamelCase("delete_"+name+"_input_result_output")]

	return &graphql.Field{
		Type: resultKind,
		Args: graphql.FieldConfigArgument{
			"where": &graphql.ArgumentConfig{
				Type: whereKind,
			},
			"accessRole": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"route": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var result = make(datatype.DataMap)
			var model = g.yekonga.ModelQuery(name)
			g.setModelParams(model, &p, foreignKey, targetKey, false)

			result["success"] = false
			result["status"] = false
			result["message"] = "Fail"
			result["data"] = nil
			deleted := helper.ToMap[interface{}](model.Delete(nil))
			deletedCount := helper.ToFloat(helper.GetValueOf(deleted, "DeletedCount"))

			// console.Log("deletedCount", deletedCount)

			if deletedCount > 0 {
				result["success"] = true
				result["status"] = true
				result["message"] = "Success"
				result["data"] = nil
			}

			return result, nil
		},
	}
}

func (g *GraphqlAutoBuild) getMutationActionField(collection string, foreignKey string, targetKey string) *graphql.Field {
	name := helper.ToCamelCase(helper.Singularize(collection))

	inputKind := g.MutationTypes[helper.ToCamelCase(name+"_input")]
	whereKind := g.MutationTypes[helper.ToCamelCase("where_"+name+"_input")]
	resultKind := g.QueryTypes[helper.ToCamelCase("action_"+name+"_input_result_output")]

	return &graphql.Field{
		Type: resultKind,
		Args: graphql.FieldConfigArgument{
			"where": &graphql.ArgumentConfig{
				Type: whereKind,
			},
			"input": &graphql.ArgumentConfig{
				Type: inputKind,
			},
			"inputData": &graphql.ArgumentConfig{
				Type: ScalarAnyType,
			},
			"inputRaw": &graphql.ArgumentConfig{
				Type: ScalarAnyType,
			},
			"accessRole": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"route": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"action": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var model = g.yekonga.ModelQuery(name)
			var data interface{} = g.getInputData(p.Args)

			ctx, _ := p.Context.Value(RequestContextKey).(*RequestContext)

			var action, accessRole, route string
			var parent datatype.DataMap
			var filters datatype.DataMap
			if v, ok := g.getParamValue(p.Args, "action").(string); ok {
				action = v
			}
			if v, ok := g.getParamValue(p.Args, "accessRole").(string); ok {
				accessRole = v
			}
			if v, ok := g.getParamValue(p.Args, "route").(string); ok {
				route = v
			}

			localWhere := helper.ToMap[interface{}](p.Args["where"])
			if localWhere != nil {
				filters = localWhere
			}

			localSource := helper.ToMap[interface{}](p.Source)
			if localSource != nil {
				parent = localSource
			}

			if ctx != nil {
				return ctx.App.actionCallback(model.Model.Name, action, ctx, &QueryContext{
					AccessRole: accessRole,
					Route:      route,
					Data:       p.Args,
					Parent:     &parent,
					Input:      &data,
					Filters:    &filters,
					Params:     p.Args,
				})
			}

			return datatype.DataMap{}, nil
		},
	}
}

func (g *GraphqlAutoBuild) addQueryType(collection string, model *DataModel) {
	// Ensure QueryTypes is initialized
	g.mut.Lock()
	defer g.mut.Unlock()
	if g.QueryTypes == nil {
		g.QueryTypes = make(map[string]*graphql.Object)
	}

	/// Type
	var name = helper.ToCamelCase(helper.Singularize(model.Name))
	var fields = make(graphql.Fields)

	for k, v := range model.Fields {
		fields[k] = g.getQueryField(k, &v)
	}

	modelFields := graphql.NewObject(graphql.ObjectConfig{
		Name:   name,
		Fields: fields,
	})

	g.addCustomModelFieldQuery(name, modelFields, QueryType)

	g.QueryTypes[name] = modelFields

	/// Summary
	var summaryName = helper.ToCamelCase(name + "_summary")
	var summaryFields = make(graphql.Fields)
	summaryFields["count"] = g.getQueryCountField(collection, "", "")
	summaryFields["sum"] = g.getQuerySumField(collection, "", "")
	summaryFields["max"] = g.getQueryMaxField(collection, "", "")
	summaryFields["min"] = g.getQueryMinField(collection, "", "")
	summaryFields["average"] = g.getQueryAverageField(collection, "", "")
	summaryFields["graph"] = g.getQueryGraphField(collection, "", "")
	modelSummary := graphql.NewObject(graphql.ObjectConfig{
		Name:   summaryName,
		Fields: summaryFields,
	})
	g.QueryTypes[summaryName] = modelSummary

	var paginateName = helper.ToCamelCase(name + "_paginate")
	var paginateFields = graphql.Fields{
		"total": &graphql.Field{
			Type: graphql.Int,
		},
		"perPage": &graphql.Field{
			Type: graphql.Int,
		},
		"currentPage": &graphql.Field{
			Type: graphql.Int,
		},
		"lastPage": &graphql.Field{
			Type: graphql.Int,
		},
		"from": &graphql.Field{
			Type: graphql.Int,
		},
		"to": &graphql.Field{
			Type: graphql.Int,
		},
		"data": &graphql.Field{
			Type: graphql.NewList(modelFields),
		},
	}
	modelPaginate := graphql.NewObject(graphql.ObjectConfig{
		Name:   paginateName,
		Fields: paginateFields,
	})
	g.QueryTypes[paginateName] = modelPaginate

	var downloadName = helper.ToCamelCase("download_" + helper.Pluralize(name))
	var downloadFields = graphql.Fields{
		"filename": &graphql.Field{
			Type: graphql.String,
		},
		"url": &graphql.Field{
			Type: graphql.String,
		},
		"type": &graphql.Field{
			Type: graphql.String,
		},
		"size": &graphql.Field{
			Type: graphql.Float,
		},
	}
	var modelDownload = graphql.NewObject(graphql.ObjectConfig{
		Name:   downloadName,
		Fields: downloadFields,
	})
	g.QueryTypes[downloadName] = modelDownload
}

func (g *GraphqlAutoBuild) addInputType(collection string, model *DataModel) {
	g.mut.Lock()
	defer g.mut.Unlock()

	if g.MutationTypes == nil {
		g.MutationTypes = make(map[string]*graphql.InputObject)
	}
	if g.QueryTypes == nil {
		g.QueryTypes = make(map[string]*graphql.Object)
	}

	var queryName = helper.ToCamelCase(model.VariableSingle)
	var inputName = helper.ToCamelCase(model.VariableSingle + "_input")
	// var inputResultName = helper.ToCamelCase(model.VariableSingle + "_input_output")
	var createResultName = helper.ToCamelCase("create_" + model.VariableSingle + "_input_result_output")
	var updateResultName = helper.ToCamelCase("update_" + model.VariableSingle + "_input_result_output")
	var deleteResultName = helper.ToCamelCase("delete_" + model.VariableSingle + "_input_result_output")
	var actionResultName = helper.ToCamelCase("action_" + model.VariableSingle + "_input_result_output")
	var importResultName = helper.ToCamelCase("import_" + model.VariableSingle + "_input_result_output")

	var fields = make(graphql.Fields)
	var inputFields = make(graphql.InputObjectConfigFieldMap)

	for k, v := range model.Fields {
		fields[k] = g.getQueryField(k, &v)
		inputFields[k] = g.getInputField(k, &v)
	}

	object := graphql.NewInputObject(graphql.InputObjectConfig{
		Name:   inputName,
		Fields: inputFields,
	})

	modelFields := g.QueryTypes[queryName]
	// modelFields := graphql.NewObject(graphql.ObjectConfig{
	// 	Name:   inputResultName,
	// 	Fields: fields,
	// })

	g.MutationTypes[inputName] = object
	g.QueryTypes[createResultName] = graphql.NewObject(graphql.ObjectConfig{
		Name: createResultName,
		Fields: graphql.Fields{
			"status": &graphql.Field{
				Type: graphql.Boolean,
			},
			"success": &graphql.Field{
				Type: graphql.Boolean,
			},
			"message": &graphql.Field{
				Type: graphql.String,
			},
			"data": &graphql.Field{
				Type: modelFields,
			},
		},
	})
	g.QueryTypes[updateResultName] = graphql.NewObject(graphql.ObjectConfig{
		Name: updateResultName,
		Fields: graphql.Fields{
			"status": &graphql.Field{
				Type: graphql.Boolean,
			},
			"success": &graphql.Field{
				Type: graphql.Boolean,
			},
			"message": &graphql.Field{
				Type: graphql.String,
			},
			"data": &graphql.Field{
				Type: modelFields,
			},
		},
	})
	g.QueryTypes[deleteResultName] = graphql.NewObject(graphql.ObjectConfig{
		Name: deleteResultName,
		Fields: graphql.Fields{
			"status": &graphql.Field{
				Type: graphql.Boolean,
			},
			"success": &graphql.Field{
				Type: graphql.Boolean,
			},
			"message": &graphql.Field{
				Type: graphql.String,
			},
			"data": &graphql.Field{
				Type: ScalarAnyType,
			},
		},
	})
	g.QueryTypes[actionResultName] = graphql.NewObject(graphql.ObjectConfig{
		Name: actionResultName,
		Fields: graphql.Fields{
			"status": &graphql.Field{
				Type: graphql.Boolean,
			},
			"success": &graphql.Field{
				Type: graphql.Boolean,
			},
			"message": &graphql.Field{
				Type: graphql.String,
			},
			"data": &graphql.Field{
				Type: ScalarAnyType,
			},
		},
	})
	g.QueryTypes[importResultName] = graphql.NewObject(graphql.ObjectConfig{
		Name: importResultName,
		Fields: graphql.Fields{
			"status": &graphql.Field{
				Type: graphql.Boolean,
			},
			"success": &graphql.Field{
				Type: graphql.Boolean,
			},
			"message": &graphql.Field{
				Type: graphql.String,
			},
			"imported": &graphql.Field{
				Type: graphql.Int,
			},
			"updated": &graphql.Field{
				Type: graphql.Int,
			},
			"deleted": &graphql.Field{
				Type: graphql.Int,
			},
			"ignored": &graphql.Field{
				Type: graphql.Int,
			},
			"errors": &graphql.Field{
				Type: graphql.Int,
			},
		},
	})
}

func (g *GraphqlAutoBuild) addModelEnumType(collection string, model *DataModel) {
	g.mut.Lock()
	defer g.mut.Unlock()

	if g.EnumTypes == nil {
		g.EnumTypes = make(map[string]*graphql.Enum)
	}

	var name = helper.ToCamelCase("" + model.VariableSingle + "_enum_fields")
	var nameStructured = helper.ToCamelCase("" + model.VariableSingle + "_structured_enum_fields")
	var fields = make(graphql.EnumValueConfigMap)
	var structuredFields = make(graphql.EnumValueConfigMap)

	for k, v := range model.Fields {
		fields[k] = &graphql.EnumValueConfig{
			Value: v.Name,
		}

		if v.Name == "id" || len(v.Options) > 0 || v.Kind == DataModelDate || helper.Contains(model.ParentKeys, v.Name) {
			structuredFields[k] = &graphql.EnumValueConfig{
				Value: v.Name,
			}
		}
	}

	var object = graphql.NewEnum(graphql.EnumConfig{
		Name:   name,
		Values: fields,
	})
	g.EnumTypes[name] = object

	if len(structuredFields) == 0 {
		structuredFields = fields
	}

	var structuredObject = graphql.NewEnum(graphql.EnumConfig{
		Name:   nameStructured,
		Values: structuredFields,
	})

	g.EnumTypes[nameStructured] = structuredObject
}

func (g *GraphqlAutoBuild) addWhereInputType(collection string, model *DataModel) {
	g.mut.Lock()
	defer g.mut.Unlock()

	if g.MutationTypes == nil {
		g.MutationTypes = make(map[string]*graphql.InputObject)
	}

	name := helper.ToCamelCase("where_" + model.VariableSingle + "_input")
	fields := make(graphql.InputObjectConfigFieldMap)

	for k, v := range model.Fields {
		fields[k] = g.getWhereInputField(collection, k, &v)
	}

	object := graphql.NewInputObject(graphql.InputObjectConfig{
		Name:   name,
		Fields: fields,
	})

	fields["AND"] = &graphql.InputObjectFieldConfig{
		Type: graphql.NewList(object),
	}
	fields["OR"] = &graphql.InputObjectFieldConfig{
		Type: graphql.NewList(object),
	}
	fields["NOR"] = &graphql.InputObjectFieldConfig{
		Type: graphql.NewList(object),
	}

	g.MutationTypes[name] = object

	if len(model.ParentFields) > 0 || len(model.ChildrenFields) > 0 {
		valid := false

		for _, v := range model.ParentFields {
			if helper.IsNotEmpty(v.Model) {
				valid = true
				break
			}
		}

		for _, v := range model.ChildrenFields {
			if helper.IsNotEmpty(v.Model) {
				valid = true
				break
			}
		}

		if valid {
			dimensionName := helper.ToCamelCase("dimension_where_" + model.VariableSingle + "_input")
			dimensionFields := make(graphql.InputObjectConfigFieldMap)
			dimensionObject := graphql.NewInputObject(graphql.InputObjectConfig{
				Name:   dimensionName,
				Fields: dimensionFields,
			})
			g.MutationTypes[dimensionName] = dimensionObject
		}

	}
}

func (g *GraphqlAutoBuild) addOrderByInputType(collection string, model *DataModel) {
	g.mut.Lock()
	defer g.mut.Unlock()

	if g.MutationTypes == nil {
		g.MutationTypes = make(map[string]*graphql.InputObject)
	}

	var name = helper.ToCamelCase("order_by_" + model.VariableSingle + "_input")
	var fields = make(graphql.InputObjectConfigFieldMap)

	for k := range model.Fields {
		// fields[k] = g.getInputField(k, &v)

		fields[k] = &graphql.InputObjectFieldConfig{
			Type: GeneralOrderOptionsEnum,
		}
	}

	var object = graphql.NewInputObject(graphql.InputObjectConfig{
		Name:   name,
		Fields: fields,
	})

	g.MutationTypes[name] = object
}

func (g *GraphqlAutoBuild) getQueryField(name string, field *DataModelField) *graphql.Field {

	scalar := graphql.String

	_v := field.Kind

	switch _v {
	case DataModelBool:
		scalar = graphql.Boolean
	case DataModelID:
		scalar = ScalarIDType
	case DataModelDate:
		scalar = ScalarDateType
	case DataModelFloat:
		scalar = graphql.Float
	case DataModelNumber:
		scalar = graphql.Int
	case DataModelString:
		scalar = graphql.String
	case DataModelObject:
		scalar = ScalarAnyType
	case DataModelAny:
		scalar = ScalarAnyType
	case DataModelArray:
		scalar = ScalarArrayType
	}

	f := &graphql.Field{
		Name: name,
		Type: scalar,
	}

	return f
}

func (g *GraphqlAutoBuild) getRelativeQueryField(fieldName string, modelName string, isParent bool, foreignKey string, targetKey string) *graphql.Field {
	var f *graphql.Field
	if isParent {
		f = g.getQuerySingleField(modelName, foreignKey, targetKey)
	} else {
		f = g.getQueryMultipleField(modelName, foreignKey, targetKey)
	}

	return f

}

func (g *GraphqlAutoBuild) getInputField(name string, field *DataModelField) *graphql.InputObjectFieldConfig {

	scalar := graphql.String

	_v := field.Kind

	switch _v {
	case DataModelBool:
		scalar = graphql.Boolean
	case DataModelID:
		scalar = ScalarIDType
	case DataModelDate:
		scalar = ScalarDateType
	case DataModelFloat:
		scalar = graphql.Float
	case DataModelNumber:
		scalar = graphql.Int
	case DataModelString:
		scalar = graphql.String
	case DataModelObject:
		scalar = ScalarAnyType
	case DataModelAny:
		scalar = ScalarAnyType
	case DataModelArray:
		scalar = ScalarArrayType
	}

	var f *graphql.InputObjectFieldConfig

	if field.Required {
		f = &graphql.InputObjectFieldConfig{
			Type: graphql.NewNonNull(scalar),
		}
	} else {
		f = &graphql.InputObjectFieldConfig{
			Type: scalar,
		}
	}

	return f
}

func (g *GraphqlAutoBuild) getTargetField(params map[string]interface{}) string {
	var value string

	if v, ok := params["targetKey"]; ok {
		if vi, oki := v.(string); oki {
			value = vi
		}
	}

	return value
}

func (g *GraphqlAutoBuild) getProductField(params map[string]interface{}) []string {
	var value []string

	if v, ok := params["targetKey"]; ok {
		if vi, oki := v.([]string); oki {
			value = vi
		}
	}

	return value
}

func (g *GraphqlAutoBuild) getActionField(params map[string]interface{}) string {
	var value string

	if v, ok := params["action"]; ok {
		if vi, oki := v.(string); oki {
			value = vi
		}
	}

	return value
}

func (g *GraphqlAutoBuild) getFormulaField(params map[string]interface{}) string {
	var value string

	if v, ok := params["formula"]; ok {
		if vi, oki := v.(string); oki {
			value = vi
		}
	}

	return value
}

func (g *GraphqlAutoBuild) getAccessRoleField(params map[string]interface{}, defaultValue string) string {
	var value string = defaultValue

	if v, ok := params["accessRole"]; ok {
		if vi, oki := v.(string); oki {
			value = vi
		}
	}

	return value
}

func (g *GraphqlAutoBuild) getRouteField(params map[string]interface{}, defaultValue string) string {
	var value string = defaultValue

	if v, ok := params["route"]; ok {
		if vi, oki := v.(string); oki {
			value = vi
		}
	}

	return value
}

func (g *GraphqlAutoBuild) getWhereField(params map[string]interface{}) datatype.DataMap {
	var value datatype.DataMap

	if v, ok := params["where"]; ok {
		vi := helper.ToMap[interface{}](v)
		if vi != nil {
			value = vi
		}
	}

	return value
}

func (g *GraphqlAutoBuild) getParamValue(params map[string]interface{}, key string) interface{} {
	var value interface{}

	if v, ok := params[key]; ok {
		value = v
	}

	return value
}

func (g *GraphqlAutoBuild) getInputData(params map[string]interface{}) interface{} {
	var input interface{}

	if d, ok := params["input"]; ok {
		input = d
	} else if d, ok := params["inputData"]; ok {
		input = d
	} else if d, ok := params["inputRaw"]; ok {
		input = d
	}

	return input
}

func (g *GraphqlAutoBuild) getWhereInputField(collection string, name string, field *DataModelField) *graphql.InputObjectFieldConfig {

	scalar := graphql.String
	fields := make(graphql.InputObjectConfigFieldMap)

	_v := field.Kind

	switch _v {
	case DataModelBool:
		scalar = graphql.Boolean
	case DataModelID:
		scalar = ScalarStringType
	case DataModelDate:
		scalar = ScalarDateType
	case DataModelFloat:
		scalar = graphql.Float
	case DataModelNumber:
		scalar = graphql.Int
	case DataModelString:
		scalar = ScalarStringType
	case DataModelObject:
		scalar = ScalarAnyType
	case DataModelAny:
		scalar = ScalarAnyType
	case DataModelArray:
		scalar = ScalarArrayType
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

	for _, key := range operations {
		fields[key] = &graphql.InputObjectFieldConfig{
			Type: scalar,
		}
	}

	for _, key := range arrayOperations {
		fields[key] = &graphql.InputObjectFieldConfig{
			Type: graphql.NewList(scalar),
		}
	}

	for _, key := range booleanOperations {
		fields[key] = &graphql.InputObjectFieldConfig{
			Type: graphql.Boolean,
		}
	}

	whereInput := graphql.InputObjectConfig{
		Name:   helper.ToCamelCase("where_" + collection + "_input_" + name + "_field"),
		Fields: fields,
	}

	f := &graphql.InputObjectFieldConfig{
		Type: graphql.NewInputObject(whereInput),
	}

	return f
}

func (g *GraphqlAutoBuild) setModelParams(model *DataModelQuery, p *graphql.ResolveParams, foreignKey string, targetKey string, isParent bool) bool {
	ctx, _ := p.Context.Value(RequestContextKey).(*RequestContext)
	parent := p.Source
	var accessRole string
	var route string

	if model.RequestContext == nil {
		model.RequestContext = ctx
	}

	if model.QueryContext.Params == nil {
		model.QueryContext.Params = make(map[string]interface{})
	}

	if pp, ok := p.Source.(graphql.ResolveParams); ok {
		localWhere := helper.ToMap[interface{}](pp.Args["where"])
		if localWhere != nil {
			model.WhereAll(localWhere)
		}

		accessRole = g.getAccessRoleField(pp.Args, accessRole)
		route = g.getRouteField(pp.Args, route)

		if pp.Args != nil {
			for k, v := range pp.Args {
				model.QueryContext.Params[k] = v
			}
		}

		if ppp, ok := pp.Source.(datatype.DataMap); ok {
			model.QueryContext.Parent = &ppp
			localParent := helper.ToDataMap(ppp)

			if helper.Contains(model.Model.ParentKeys, foreignKey) {
				model.Where(foreignKey, localParent[targetKey])
				if isParent {
					model.Where(targetKey, localParent[foreignKey])
				} else {
					model.Where(foreignKey, localParent[targetKey])
				}
			} else {
				if helper.IsEmpty(localParent[foreignKey]) {
					// The parent row has nothing to link on, so the relation is empty.
					// Callers that don't check the result must not get unfiltered records.
					model.Where("_id", nil)
					return false
				}
				model.Where(targetKey, localParent[foreignKey])
			}
		}
	} else if helper.IsMap(parent) {
		parentParams := helper.GetValueOf(parent, "_params")

		if helper.IsMap(parentParams) {
			accessRole = helper.GetValueOfString(parentParams, "accessRole")
			route = helper.GetValueOfString(parentParams, "route")

			if _, ok := p.Args["accessRole"]; !ok && helper.IsNotEmpty(accessRole) {
				(*p).Args["accessRole"] = accessRole
			}
			if _, ok := p.Args["route"]; !ok && helper.IsNotEmpty(route) {
				(*p).Args["route"] = route
			}
		}
	}

	if p.Args != nil {
		for k, v := range p.Args {
			model.QueryContext.Params[k] = v
		}
	}

	filters := g.getWhereField(p.Args)

	model.QueryContext.AccessRole = g.getAccessRoleField(p.Args, accessRole)
	model.QueryContext.Route = g.getRouteField(p.Args, route)
	model.QueryContext.Filters = &filters

	if ctx != nil {
		model.SetRequestContext(ctx)
	}

	localWhere := helper.ToMap[interface{}](p.Args["where"])
	if localWhere != nil {
		model.WhereAll(localWhere)
	}

	if helper.IsMap(parent) {
		localParent := helper.ToDataMap(parent)

		model.QueryContext.Parent = &p
		if helper.IsMap(localParent) && helper.IsNotEmpty(foreignKey) {
			if helper.Contains(model.Model.ParentKeys, foreignKey) {
				if isParent {
					model.Where(targetKey, localParent[foreignKey])
				} else {
					model.Where(foreignKey, localParent[targetKey])
				}
			} else {
				if helper.IsEmpty(localParent[foreignKey]) {
					// The parent row has nothing to link on, so the relation is empty.
					// Callers that don't check the result must not get unfiltered records.
					model.Where("_id", nil)
					return false
				}

				model.Where(targetKey, localParent[foreignKey])
			}
		}
	}

	localOrderBy := helper.ToMap[string](p.Args["orderBy"])
	for k, v := range localOrderBy {
		model.OrderBy(k, v)
	}

	if v, ok := p.Args["groupBy"].([]interface{}); ok {
		for _, n := range v {
			if nn, ok := n.(string); ok {
				model.GroupBy(nn)
			}
		}
	} else if v, ok := p.Args["groupBy"].([]string); ok {
		for _, n := range v {
			model.GroupBy(n)
		}
	} else if v, ok := p.Args["groupBy"].(string); ok {
		model.GroupBy(v)
	}

	if v, ok := p.Args["distinct"].([]interface{}); ok {
		for _, n := range v {
			if nn, ok := n.(string); ok {
				model.Distinct(nn)
			}
		}
	} else if v, ok := p.Args["distinct"].([]string); ok {
		model.DistinctAll(v)
	} else if v, ok := p.Args["distinct"].(string); ok {
		model.Distinct(v)
	}

	if v, ok := p.Args["limit"].(int); ok {
		model.Take(v)
	}

	if v, ok := p.Args["page"].(int); ok {
		model.Page(v)
	}

	if v, ok := p.Args["skip"].(int); ok {
		model.Skip(v)
	}

	return true
}

func (g *GraphqlAutoBuild) loadRelatedData(data *[]datatype.DataMap, model *DataModelQuery, p *graphql.ResolveParams, foreignKey string, targetKey string) {
	for i, d := range *data {
		dataMap := g.formateOutputData(model, d, foreignKey, targetKey)
		dataMap["_params"] = p.Args
		(*data)[i] = dataMap
	}
}

func (g *GraphqlAutoBuild) formateOutputData(model *DataModelQuery, data interface{}, foreignKey string, targetKey string) map[string]interface{} {
	output := map[string]interface{}{}

	var localData map[string]interface{}
	switch d := data.(type) {
	case datatype.DataMap:
		localData = d
	case map[string]interface{}:
		localData = d
	default:
		if helper.IsNotEmpty(data) && helper.IsMap(data) {
			localData = helper.ToDataMap(data)
		}
	}

	// Worked out on first use rather than per file field: it parses a URL.
	domain, domainKnown := "", false
	originDomain := func() string {
		if !domainKnown {
			if model.RequestContext != nil && model.RequestContext.Client != nil {
				domain = model.RequestContext.Client.OriginDomain()
			}
			domainKnown = true
		}
		return domain
	}

	for k, v := range localData {
		if helper.Contains(model.Model.Protected, k) {
			output[k] = "--protected--"
		} else {
			output[k] = v
		}

		if helper.Contains(model.Model.FileFields, k) {
			if vi, ok := v.(string); ok && vi != "" {
				output[k] = helper.GetBaseUrl(vi, originDomain())
			} else {
				output[k] = helper.GetBaseUrl("placeholder.jpg", originDomain())
			}
		}
	}

	return output
}
