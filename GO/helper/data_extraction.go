package helper

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper/console"
	"github.com/xuri/excelize/v2"
)

// GetSortedUniqueKeys extracts all unique keys (field names) from a slice of Records
// and returns them as an alphabetically sorted slice of strings.
func GetSortedUniqueKeys(records []datatype.DataMap) []string {
	keyMap := make(map[string]struct{})
	for _, record := range records {
		for key, val := range record {
			if v, ok := val.(map[string]interface{}); ok {
				sub_keys := GetSortedUniqueKeys([]datatype.DataMap{ToDataMap(v)})

				for _, k := range sub_keys {
					keyMap[key+"."+k] = struct{}{}
				}
			} else {
				keyMap[key] = struct{}{}
			}
		}
	}

	var keys []string
	for key := range keyMap {
		keys = append(keys, key)
	}

	// Sort the keys alphabetically for a deterministic column order
	sort.Strings(keys)
	return keys
}

// ConvertJSONArrayToCSV takes a byte slice of JSON data (an array of objects)
// and a list of desired column names. It converts this data into a CSV string.
func ConvertJSONArrayToDataArray(jsonData interface{}, headingColumns []string, flatKeys []string) ([][]string, error) {
	var records []datatype.DataMap
	jsonInput := []byte(ToJson(jsonData))

	if err := json.Unmarshal(jsonInput, &records); err != nil {
		log.Fatalf("Error unmarshaling JSON data for key extraction: %v", err)

		return nil, fmt.Errorf("failed to unmarshal JSON data: %w", err)
	}

	if !Contains(flatKeys, "customFormValues") {
		flatKeys = append(flatKeys, "customFormValues")
	}

	// console.Log("=== flatKeys", flatKeys)

	dataSize := len(records)
	headingColumnsNames := []string{}
	result := make([][]string, 0, dataSize)
	flatRecords := make([]datatype.DataMap, 0, dataSize)

	// --- 4. Write Data Rows ---
	for _, record := range records {
		// Flatten customFormValues (if present) into label -> value pairs
		// so they can be looked up like any other column key.
		flatRecords = append(flatRecords, FlattenSpecialKeys(record, false, flatKeys))
	}

	if len(headingColumns) == 0 {
		// Automatically determine the column names and sort them alphabetically
		headingColumns = GetSortedUniqueKeys(flatRecords)
	} else {
		extraRecordsOnly := make([]datatype.DataMap, 0, dataSize)

		// --- Write Data Rows ---
		for _, record := range records {
			// Flatten customFormValues (if present) into label -> value pairs
			// so they can be looked up like any other column key.
			extraRecordsOnly = append(extraRecordsOnly, FlattenSpecialKeys(record, true, flatKeys))
		}

		headingExtractColumns := GetSortedUniqueKeys(extraRecordsOnly)

		for _, k := range headingExtractColumns {
			if !Contains(headingColumns, k) {
				headingColumns = append(headingColumns, k)
			}
		}
	}

	for _, col := range headingColumns {
		cols := strings.Split(col, ".")
		groupName := ""
		colName := cols[0]
		if len(cols) > 1 {
			groupName = strings.ToUpper(ToTitle(cols[0])) + ": "
			colName = strings.Join(cols[1:], ".")
		}
		colName = strings.ReplaceAll(colName, ".", " ")

		headingColumnsNames = append(headingColumnsNames, groupName+ToTitle(colName))
	}

	result = append(result, headingColumnsNames)
	dataList := ConvertJSONArrayToListDataArray(flatRecords, headingColumns)

	for _, v := range dataList {
		result = append(result, v)
	}

	return result, nil
}

// ConvertJSONArrayToCSV takes a byte slice of JSON data (an array of objects)
// and a list of desired column names. It converts this data into a CSV string.
func ConvertJSONArrayToCSV(jsonData interface{}, headingColumns []string, filename string, flatKeys []string) (string, error) {
	var records [][]string

	if list, ok := jsonData.([][]string); ok {
		records = list
	} else {
		records, _ = ConvertJSONArrayToDataArray(jsonData, headingColumns, flatKeys)
	}
	// console.Log("ConvertJSONArrayToCSV", records)

	// 2. Prepare the CSV writer
	// A bytes.Buffer implements io.Writer, which the csv.Writer needs.
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)

	// --- 4. Write Data Rows ---
	for _, csvRow := range records {
		if err := writer.Write(csvRow); err != nil {
			return "", fmt.Errorf("failed to write CSV row: %w", err)
		}
	}

	// Flush the buffer to ensure all data is written
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", fmt.Errorf("failed to flush CSV writer: %w", err)
	}

	if IsNotEmpty(filename) {
		// A bytes.Buffer implements io.Writer, which the csv.Writer needs.
		publicDir, _ := GetPublicPath()
		fileSaved := path.Join(publicDir, filename)

		SaveToFile(buf.String(), fileSaved)
		return fileSaved, nil
	}

	return buf.String(), nil
}

// ConvertJSONArrayToCSV takes a byte slice of JSON data (an array of objects)
// and a list of desired column names. It converts this data into a CSV string.
func ConvertJSONArrayToExcel(jsonData interface{}, headingColumns []string, filename string, flatKeys []string) (string, error) {
	var records [][]string

	if list, ok := jsonData.([][]string); ok {
		records = list
	} else {
		records, _ = ConvertJSONArrayToDataArray(jsonData, headingColumns, flatKeys)
	}

	if IsEmpty(filename) {
		filename = "tmp/" + GetHexString(24) + ".xlsx"
	}

	// A bytes.Buffer implements io.Writer, which the csv.Writer needs.
	publicDir, _ := GetPublicPath()
	fileSaved := path.Join(publicDir, filename)

	CreateDirectory(filepath.Dir(fileSaved))

	f := excelize.NewFile()

	// Create a new sheet
	index, err := f.NewSheet("Report")
	if err != nil {
		panic(err)
	}

	style, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Color: "#FFFFFF", Family: "Calibri"},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#797979ff"}, Pattern: 1},
	})

	if len(records) > 0 {
		columnsCount := uint(len(records[0]))

		// Apply style to header row
		f.SetCellStyle("Report", cellLocation(1, 1), cellLocation(1, columnsCount), style)

		// --- 4. Write Data Rows ---
		for iRow, dataRow := range records {

			for iCol, cellValue := range dataRow {
				// Convert column index to letter (A, B, C, ...)
				cellKey := cellLocation(uint(iRow+1), uint(iCol+1))

				f.SetCellValue("Report", cellKey, cellValue)
			}

		}
	}

	// Set active sheet
	f.SetActiveSheet(index)

	// Save to file
	if err := f.SaveAs(fileSaved); err != nil {
		console.Error("Error saving Excel file:", err)
		return "", err
	}

	return fileSaved, nil
}

func ConvertExcelToCSV(excelPath string) (string, error) {
	f, err := excelize.OpenFile(excelPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	sheet := f.GetSheetList()[0]

	// Get all rows
	rows, err := f.GetRows(sheet)
	if err != nil {
		return "", err
	}

	csvPath := filepath.Join(
		filepath.Dir(excelPath),
		"output.csv",
	)

	file, err := os.Create(csvPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	for i, row := range rows {
		var record []string

		for j := range row {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+1)

			// ✅ This returns formatted value (not scientific)
			val, err := f.GetCellValue(sheet, cell, excelize.Options{
				RawCellValue: true,
			})

			if err != nil {
				val = row[j]
			}

			// console.Error(val)
			if strings.HasSuffix(val, "E+11") {
				val, _ = ScientificToString(val)
			}

			record = append(record, val)
		}

		writer.Write(record)
	}

	return csvPath, nil
}

// Convert scientific notation to full number string
func ScientificToString(value string) (string, error) {
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return "", err
	}

	// Remove scientific notation
	return strconv.FormatFloat(f, 'f', 0, 64), nil
}

func cellLocation(row uint, col uint) string {
	colLetter, _ := excelize.ColumnNumberToName(int(col))
	return fmt.Sprintf("%s%d", colLetter, row)
}
