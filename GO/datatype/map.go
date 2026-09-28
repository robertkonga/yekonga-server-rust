package datatype

import "encoding/json"

type Context map[string]interface{}
type ContextObject map[string]map[string]interface{}

type JsonObject map[string]interface{}
type DataMap map[string]interface{}
type Record map[string]interface{}

func (c *Context) ToString() string {
	jsonData, _ := json.MarshalIndent(c.ToMap(), "", " ")

	return string(jsonData)
}

func (c *Context) ToMap() map[string]interface{} {
	return *c
}

func (c *Record) ToString() string {
	jsonData, _ := json.MarshalIndent(c.ToMap(), "", " ")

	return string(jsonData)
}

func (c *Record) ToMap() map[string]interface{} {
	return *c
}

func (c *DataMap) ToString() string {
	jsonData, _ := json.MarshalIndent(c.ToMap(), "", " ")

	return string(jsonData)
}

func (c *DataMap) ToMap() map[string]interface{} {
	return *c
}

func (c *JsonObject) ToString() string {
	jsonData, _ := json.MarshalIndent(c.ToMap(), "", " ")

	return string(jsonData)
}

func (c *JsonObject) ToMap() map[string]interface{} {
	return *c
}
