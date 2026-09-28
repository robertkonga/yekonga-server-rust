package helper

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/robertkonga/yekonga-server-go/datatype"
)

func ConvertJSONArrayToListDataArray(records []datatype.DataMap, headingColumns []string) [][]string {
	result := make([][]string, 0, len(records))

	// --- 4. Write Data Rows ---
	for _, record := range records {
		// Flatten customFormValues (if present) into label -> value pairs
		// so they can be looked up like any other column key.
		flatRecord := record

		var csvRow []string

		// Iterate through the specified columns to maintain order
		for _, key := range headingColumns {
			// Get the value from the record map using dot-notation path
			var value interface{}
			var exists bool

			if v, ok := flatRecord[key]; ok {
				value = v
				exists = true
			} else if strings.Contains(key, ".") {
				// Traverse nested objects
				var current interface{} = flatRecord
				parts := strings.Split(key, ".")
				exists = true
				for _, part := range parts {
					if m, ok := current.(datatype.DataMap); ok {
						if v, ok2 := m[part]; ok2 {
							current = v
						} else {
							exists = false
							break
						}
					} else if m, ok := current.(map[string]interface{}); ok {
						if v, ok2 := m[part]; ok2 {
							current = v
						} else {
							exists = false
							break
						}
					} else {
						exists = false
						break
					}
				}
				if exists {
					value = current
				}
			}

			if !exists {
				// If the key is not present in the record, add an empty string
				csvRow = append(csvRow, "")
				continue
			}

			// Convert the value to a string based on its underlying type
			var valueStr string
			switch v := value.(type) {
			case string:
				valueStr = v
			case float64:
				// JSON numbers are typically unmarshalled as float64
				// Format as a regular string representation
				valueStr = fmt.Sprintf("%v", v)
			case bool:
				valueStr = fmt.Sprintf("%v", v)
			case []interface{}:
				// This handles the JavaScript logic of joining arrays with " / "
				strElements := make([]string, len(v))
				for i, elem := range v {
					// Recursively convert array elements to string
					strElements[i] = fmt.Sprintf("%v", elem)
				}
				valueStr = strings.Join(strElements, " / ")
			case map[string]interface{}:
				// Handle explicitly selected map-type columns by printing as JSON to maintain 1-to-1 column mapping
				jsonBytes, err := json.Marshal(v)
				if err == nil {
					valueStr = string(jsonBytes)
				} else {
					valueStr = fmt.Sprintf("%v", v)
				}
			default:
				// Catch-all for other types (e.g., nested objects, null)
				if value != nil {
					valueStr = fmt.Sprintf("%v", value)
				} else {
					valueStr = ""
				}
			}

			// Note: The csv.Writer handles complex escaping/quoting (like for strings
			// containing commas or quotes) automatically.
			csvRow = append(csvRow, valueStr)
		}

		result = append(result, csvRow)
	}

	return result
}

// FlattenSpecialKeys returns a shallow copy of record with the
// "customFormValues" array expanded into individual entries keyed by
// each item's customForm.label, mapped to its corresponding value.
// This lets headingColumns reference a custom form's label text directly
// as if it were a normal column key.
func FlattenSpecialKeys(record datatype.DataMap, extraOnly bool, flatKeys []string) datatype.DataMap {
	var flat datatype.DataMap

	flat = make(datatype.DataMap, len(record))

	// Copy so we don't mutate the original record
	for k, v := range record {
		items, ok := v.([]interface{})
		isCustomForm := false

		if ok && Contains(flatKeys, k) {
			if len(items) > 0 {
				for _, item := range items {
					label := GetValueOfString(item, "customForm.label")
					value := GetValueOf(item, "value")

					if label == "" {
						continue
					}

					flat[fmt.Sprintf("%s.%s", k, label)] = value
					isCustomForm = true
				}
			}
		}
		if !isCustomForm && !extraOnly {
			flat[k] = v
		}
	}

	// for _, key := range flatKeys {
	// 	raw, ok := record[key]
	// 	if !ok {
	// 		continue
	// 	}

	// 	items, ok := raw.([]interface{})
	// 	if !ok {
	// 		continue
	// 	}

	// 	for _, item := range items {
	// 		label := GetValueOfString(item, "customForm.label")
	// 		value := GetValueOf(item, "value")

	// 		if label == "" {
	// 			continue
	// 		}

	// 		// flat[label] = value
	// 		flat[fmt.Sprintf("%s.%s", key, label)] = value
	// 	}

	// 	delete(flat, key)
	// }

	return flat
}

func FlattenCustomFormValues(record datatype.DataMap, extraOnly bool) datatype.DataMap {
	raw, ok := record["customFormValues"]
	if !ok {
		return record
	}

	items, ok := raw.([]interface{})
	if !ok {
		return record
	}
	var flat datatype.DataMap

	// Copy so we don't mutate the original record
	if extraOnly {
		flat = make(datatype.DataMap, len(items))
	} else {
		flat = make(datatype.DataMap, len(record)+len(items))
		for k, v := range record {
			if k == "customFormValues" {
				continue
			}
			flat[k] = v
		}
	}

	for _, item := range items {
		label := GetValueOfString(item, "customForm.label")
		value := GetValueOf(item, "value")

		if label == "" {
			continue
		}

		flat[label] = value
	}

	return flat
}
