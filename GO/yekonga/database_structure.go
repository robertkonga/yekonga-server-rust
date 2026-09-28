package yekonga

import (
	"strings"

	"github.com/robertkonga/yekonga-server-go/helper"
)

type DatabaseStructure = map[string]map[string]CollectionFieldConfig

type CollectionFieldConfig struct {
	PrimaryKey   bool                            `json:"primaryKey,omitempty"`
	Name         string                          `json:"name,omitempty"`
	Kind         string                          `json:"type"`
	DefaultValue interface{}                     `json:"default,omitempty"`
	Required     bool                            `json:"required,omitempty"`
	Protected    bool                            `json:"protected,omitempty"`
	Index        bool                            `json:"index,omitempty"`  // create a database index on this field
	Unique       bool                            `json:"unique,omitempty"` // create a unique index on this field
	Options      []string                        `json:"options,omitempty"`
	ForeignKey   CollectionFieldConfigForeignKey `json:"foreignKey,omitempty"`
}

type CollectionFieldConfigForeignKey struct {
	Model string `json:"model,omitempty"`
	Key   string `json:"key,omitempty"`
}

// databaseCollectionFieldConfigFromMap builds a CollectionFieldConfig from the raw
// map[string]interface{} shape used by the external database structure JSON file
// (e.g. {"type": "String", "default": nil, "required": false, "foreignKey": "Tenant.id"}).
func databaseCollectionFieldConfigFromMap(fieldData interface{}) CollectionFieldConfig {
	result := CollectionFieldConfig{}
	field := map[string]interface{}{}

	var relation interface{}
	var relationOk bool

	if v, ok := fieldData.(CollectionFieldConfig); ok {
		result = v
	} else if helper.IsMap(fieldData) {
		field = helper.ToMap[interface{}](fieldData)
		if v, ok := field["type"]; ok {
			if vi, oki := v.(string); oki {
				result.Kind = vi
			}
		}

		if v, ok := field["default"]; ok {
			result.DefaultValue = v
		} else if v, ok := field["defaultValue"]; ok {
			result.DefaultValue = v
		}

		if v, ok := field["required"]; ok {
			if vi, oki := v.(bool); oki {
				result.Required = vi
			}
		}

		if v, ok := field["protected"]; ok {
			if vi, oki := v.(bool); oki {
				result.Protected = vi
			}
		}

		if v, ok := field["index"]; ok {
			if vi, oki := v.(bool); oki {
				result.Index = vi
			}
		}

		if v, ok := field["unique"]; ok {
			if vi, oki := v.(bool); oki {
				result.Unique = vi
			}
		}

		if v, ok := field["primaryKey"]; ok {
			if vi, oki := v.(bool); oki {
				result.PrimaryKey = vi
			}
		}

		if v, ok := field["options"]; ok && helper.IsArray(v) {
			result.Options = helper.ToList[string](v)
		}

		relation, relationOk = field["foreignKey"]
		if !relationOk {
			relation, relationOk = field["relation"]
		}
		if !relationOk {
			relation, relationOk = field["source"]
		}

		if relationOk {
			if vi, oki := relation.(string); oki && helper.IsNotEmpty(vi) {
				ks := strings.SplitN(vi, ".", 2)
				foreignKey := CollectionFieldConfigForeignKey{Model: ks[0]}

				if len(ks) == 2 {
					foreignKey.Key = ks[1]
				} else {
					foreignKey.Key = "id"
				}

				result.ForeignKey = foreignKey
			}
		}
	}

	return result
}
