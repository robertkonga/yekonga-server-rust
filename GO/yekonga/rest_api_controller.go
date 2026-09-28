package yekonga

import (
	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/helper"
)

type RestApiController struct {
	app   *YekongaData
	model *DataModel
}

func (y *YekongaData) initializeRestApi() {
	if y.Config.RestApiEnabled {

		// Register REST API routes for each model
		y.Post("/api/:moduleName/create", func(req *Request, res *Response) {
			moduleName := helper.GetValueOfString(req.Params, "moduleName")
			gqlName := helper.Pluralize(helper.ToVariable(moduleName))
			input := helper.ToDataMap(req.Body())
			fields := ""
			model := y.Model(helper.ToCamelCase(helper.Singularize(gqlName)))

			for key := range model.Fields {
				fields += key + ","
			}

			result := y.GraphQL("query{"+helper.ToVariable("create_"+gqlName)+"{"+fields+"}}", map[string]interface{}{
				"input": input,
			}, req, res)

			res.Json(result.Data)
		})

		y.Post("/api/:moduleName/update/:id", func(req *Request, res *Response) {
			id := helper.GetValueOfString(req.Params, "id")
			moduleName := helper.GetValueOfString(req.Params, "moduleName")
			gqlName := helper.Pluralize(helper.ToVariable(moduleName))
			input := helper.ToDataMap(req.Body())
			fields := ""
			model := y.Model(helper.ToCamelCase(helper.Singularize(gqlName)))

			for key := range model.Fields {
				fields += key + ","
			}

			result := y.GraphQL("query{"+helper.ToVariable("update_"+gqlName)+"(where:{id:{equalTo:\""+id+"\"}}){"+fields+"}}", map[string]interface{}{
				"input": input,
			}, req, res)

			res.Json(result.Data)
		})

		y.Post("/api/:moduleName/delete/:id", func(req *Request, res *Response) {
			id := helper.GetValueOfString(req.Params, "id")
			moduleName := helper.GetValueOfString(req.Params, "moduleName")
			gqlName := helper.Pluralize(helper.ToVariable(moduleName))
			fields := ""
			model := y.Model(helper.ToCamelCase(helper.Singularize(gqlName)))

			for key := range model.Fields {
				fields += key + ","
			}

			result := y.GraphQL("query{"+helper.ToVariable("delete_"+gqlName)+"(where:{id:{equalTo:\""+id+"\"}}){"+fields+"}}", map[string]interface{}{}, req, res)

			res.Json(result.Data)
		})

		y.Get("/api/:moduleName", func(req *Request, res *Response) {
			moduleName := helper.GetValueOfString(req.Params, "moduleName")
			gqlName := helper.ToVariable(helper.Pluralize(moduleName))
			fields := ""
			model := y.Model(helper.ToCamelCase(helper.Singularize(gqlName)))

			for key := range model.Fields {
				fields += key + ","
			}

			query := "query{" + gqlName + "{" + fields + "}}"
			result := y.GraphQL(query, map[string]interface{}{}, req, res)

			res.Json(helper.GetValueOf(result.Data, gqlName))
		})

		y.Get("/api/:moduleName/:id", func(req *Request, res *Response) {
			id := helper.GetValueOfString(req.Params, "id")
			moduleName := helper.GetValueOfString(req.Params, "moduleName")
			gqlName := helper.ToVariable(helper.Singularize(moduleName))
			fields := ""
			model := y.Model(helper.ToCamelCase(helper.Singularize(gqlName)))

			for key := range model.Fields {
				fields += key + ","
			}
			query := "query{" + gqlName + "(where:{id:{equalTo:\"" + id + "\"}}){" + fields + "}}"
			result := y.GraphQL(query, map[string]interface{}{}, req, res)

			res.Json(helper.GetValueOf(result.Data, gqlName))
		})

		// Register REST API routes for each model
		y.Put("/api/:moduleName", func(req *Request, res *Response) {
			moduleName := helper.GetValueOfString(req.Params, "moduleName")
			gqlName := helper.Pluralize(helper.ToVariable(moduleName))
			input := helper.ToDataMap(req.Body())
			fields := ""
			model := y.Model(helper.ToCamelCase(helper.Singularize(gqlName)))

			for key := range model.Fields {
				fields += key + ","
			}

			result := y.GraphQL("query{"+helper.ToVariable("create_"+gqlName)+"{"+fields+"}}", map[string]interface{}{
				"input": input,
			}, req, res)

			res.Json(result.Data)
		})

		y.Patch("/api/:moduleName/:id", func(req *Request, res *Response) {
			id := helper.GetValueOfString(req.Params, "id")
			moduleName := helper.GetValueOfString(req.Params, "moduleName")
			gqlName := helper.Pluralize(helper.ToVariable(moduleName))
			input := helper.ToDataMap(req.Body())
			fields := ""
			model := y.Model(helper.ToCamelCase(helper.Singularize(gqlName)))

			for key := range model.Fields {
				fields += key + ","
			}

			result := y.GraphQL("query{"+helper.ToVariable("update_"+gqlName)+"(where:{id:{equalTo:\""+id+"\"}}){"+fields+"}}", map[string]interface{}{
				"input": input,
			}, req, res)

			res.Json(result)
		})

		y.Delete("/api/:moduleName/:id", func(req *Request, res *Response) {
			id := helper.GetValueOfString(req.Params, "id")
			moduleName := helper.GetValueOfString(req.Params, "moduleName")
			gqlName := helper.Pluralize(helper.ToVariable(moduleName))
			input := helper.ToDataMap(req.Body())
			fields := ""
			model := y.Model(helper.ToCamelCase(helper.Singularize(gqlName)))

			for key := range model.Fields {
				fields += key + ","
			}

			result := y.GraphQL("query{"+helper.ToVariable("delete_"+gqlName)+"(where:{id:{equalTo:\""+id+"\"}}){"+fields+"}}", map[string]interface{}{
				"input": input,
			}, req, res)

			res.Json(result.Data)
		})
	}
}

func NewRestApiController(app *YekongaData) []*RestApiController {
	var controllers []*RestApiController = make([]*RestApiController, 0)

	for key, model := range app.models {
		key := helper.ToSlug(key)

		controller := &RestApiController{
			app:   app,
			model: model,
		}

		singleRoute := "/api/" + helper.Singularize(key) + "/:id"
		listRoute := "/api/" + helper.Pluralize(key)
		createRoute := "/api/" + helper.Singularize(key)
		updateRoute := "/api/" + helper.Singularize(key) + "/:id"
		deleteRoute := "/api/" + helper.Singularize(key) + "/:id"

		controller.app.Get(singleRoute, func(req *Request, res *Response) {
			data := req.Body()
			result := controller.Create(helper.ToMap[interface{}](data))

			res.Json(result)
		})

		controller.app.Get(listRoute, func(req *Request, res *Response) {
			result := controller.GetList()

			res.Json(result)
		})

		controller.app.Post(createRoute, func(req *Request, res *Response) {
			data := req.Body()
			result := controller.Create(helper.ToMap[interface{}](data))

			res.Json(result)
		})

		controller.app.Put(updateRoute, func(req *Request, res *Response) {
			data := req.Body()
			result := controller.Update(req.Params["id"], helper.ToMap[interface{}](data))

			res.Json(result)
		})

		controller.app.Delete(deleteRoute, func(req *Request, res *Response) {
			result := controller.Delete(req.Params["id"])

			res.Json(result)
		})

		controllers = append(controllers, controller)
	}

	return controllers
}

func (c *RestApiController) GetApp() *YekongaData {
	return c.app
}

func (c *RestApiController) GetSetting() *config.YekongaConfig {
	return c.app.Config
}

func (c *RestApiController) GetList() []map[string]interface{} {
	data := make([]map[string]interface{}, 0)

	return data
}

func (c *RestApiController) GetOne(id string) map[string]interface{} {
	data := make(map[string]interface{})

	return data
}

func (c *RestApiController) Create(data map[string]interface{}) map[string]interface{} {
	return data
}

func (c *RestApiController) Update(id string, data map[string]interface{}) map[string]interface{} {
	return data
}

func (c *RestApiController) Delete(id string) bool {
	return true
}
