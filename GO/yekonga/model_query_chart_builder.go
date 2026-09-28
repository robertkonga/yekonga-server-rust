package yekonga

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robertkonga/yekonga-server-go/helper"
)

// ChartType represents the type of chart
type ChartType string

const (
	ChartTypePie    ChartType = "PIE"
	ChartTypeLinear ChartType = "LINEAR"
)

// TotalType represents the calculation type
type TotalType string

const (
	TotalTypeCount   TotalType = "COUNT"
	TotalTypeSum     TotalType = "SUM"
	TotalTypeMax     TotalType = "MAX"
	TotalTypeMin     TotalType = "MIN"
	TotalTypeAvg     TotalType = "AVG"
	TotalTypeAverage TotalType = "AVERAGE"
)

// Periodicity represents time grouping periods
type Periodicity string

const (
	PeriodicityNone      Periodicity = "NONE"
	PeriodicityHourly    Periodicity = "HOURLY"
	PeriodicityDaily     Periodicity = "DAILY"
	PeriodicityDayHours  Periodicity = "DAY_HOURS"
	PeriodicityWeekly    Periodicity = "WEEKLY"
	PeriodicityWeekDays  Periodicity = "WEEK_DAYS"
	PeriodicityMonthly   Periodicity = "MONTHLY"
	PeriodicityMonthDays Periodicity = "MONTH_DAYS"
	PeriodicityQuarterly Periodicity = "QUARTERLY"
	PeriodicityYearly    Periodicity = "YEARLY"
	PeriodicityYearMonth Periodicity = "YEAR_MONTH"
)

type ResolverChartGroupData struct {
	Collection  string   `json:"collection"`
	ClassName   string   `json:"className"`
	PrimaryKey  string   `json:"primaryKey"`
	PrimaryName string   `json:"primaryName"`
	Fields      []string `json:"field"`
}

// Dataset represents chart dataset
type ChartDataset struct {
	Label           string    `json:"label"`
	Color           []string  `json:"color"`
	BackgroundColor []string  `json:"backgroundColor"`
	Data            []float64 `json:"data"`
}

// ChartData represents the chart response structure
type ChartData struct {
	Type     ChartType      `json:"type"`
	Labels   []string       `json:"labels"`
	Datasets []ChartDataset `json:"datasets"`
}

// ChartBuilder main struct
type ChartBuilder struct {
	dataModel *DataModelQuery
}

// NewChartBuilder creates a new chart builder instance
func NewChartBuilder(model *DataModelQuery) *ChartBuilder {
	return &ChartBuilder{
		dataModel: model,
	}
}

// BuildGraph builds the chart data based on filters and parameters
func (cb *ChartBuilder) BuildGraph(filter map[string]FilterValue, isAdmin bool) (*ChartData, error) {
	context := cb.dataModel.QueryContext

	if context.Params == nil {
		context.Params = make(map[string]interface{})
	}

	// Extract parameters
	totalType := cb.getStringParam(context.Params, "runningCalculation", cb.getStringParam(context.Params, "total", "COUNT"))
	xAxis := cb.getColumn(cb.getStringParam(context.Params, "dimension", cb.getStringParam(context.Params, "xAxis", cb.getStringParam(context.Params, "targetKey", ""))))
	groupBy := cb.getColumn(cb.getStringParam(context.Params, "dimensionBreakdown", cb.getStringParam(context.Params, "groupBy", "")))
	chartType := cb.getStringParam(context.Params, "type", "LINEAR")
	periodicity := cb.getStringParam(context.Params, "periodicity", "NONE")
	targetKey := cb.getStringParam(context.Params, "targetKey", "")

	if chartType != "PIE" {
		if periodicity != "NONE" {
			if !helper.Contains(cb.dataModel.Model.DateFields, xAxis) {
				return nil, errors.New(`Field "dimension" must be date / time`)
			}
		} else {
			if xAxis != "id" && !helper.Contains(cb.dataModel.Model.OptionFields, xAxis) && !helper.Contains(cb.dataModel.Model.ParentKeys, xAxis) {
				return nil, errors.New(`Field "dimension" must be one of these (id, ` + strings.Join(cb.dataModel.Model.OptionFields, ", ") + strings.Join(cb.dataModel.Model.ParentKeys, ", ") + ")")
			} else if helper.IsEmpty(groupBy) && chartType == "LINE" {
				return nil, errors.New(`Field "dimensionBreakdown" is required when "periodicity" is NONE`)
			}
		}
	} else {
		if xAxis != "id" && !helper.Contains(cb.dataModel.Model.OptionFields, xAxis) && !helper.Contains(cb.dataModel.Model.ParentKeys, xAxis) {
			return nil, errors.New(`Field "dimension" must be one of these (id, ` + strings.Join(cb.dataModel.Model.OptionFields, ", ") + strings.Join(cb.dataModel.Model.ParentKeys, ", ") + ")")
		}
	}

	if totalType != "COUNT" && helper.IsEmpty(targetKey) {
		return nil, errors.New(`"targetKey" parameter is required when "total" is not COUNT`)
	}

	var yAxis string
	if chartType == "PIE" {
		yAxis = cb.getColumn(cb.getStringParam(context.Params, "metric", cb.getStringParam(context.Params, "targetKey", "")))
	} else {
		yAxis = cb.getColumn(cb.getStringParam(context.Params, "metric", cb.getStringParam(context.Params, "yAxis", "")))
	}

	// Initialize chart chartData
	chartData := &ChartData{
		Type: ChartType(chartType),
	}

	// Get data model (placeholder - would need actual implementation)
	minAxis := cb.dataModel.Min(xAxis, nil)
	maxAxis := cb.dataModel.Max(xAxis, nil)

	// Process filters
	dateKeys := []FilterOperator{FilterGreaterThan, FilterLessThan, FilterGreaterThanOrEqualTo, FilterLessThanOrEqualTo}
	// console.Error(dateKeys, xAxis, minAxis, maxAxis, filter)

	for key, filterValue := range filter {
		if key == xAxis && len(filterValue.In) >= 2 {
			minAxis = helper.GetTimestamp(filterValue.In[0])
			maxAxis = helper.GetTimestamp(filterValue.In[1])
		} else if key == xAxis {
			for _, dateKey := range dateKeys {
				var val interface{}
				switch dateKey {
				case FilterGreaterThan:
					val = filterValue.GreaterThan
				case FilterLessThan:
					val = filterValue.LessThan
				case FilterGreaterThanOrEqualTo:
					val = filterValue.GreaterThanOrEqualTo
				case FilterLessThanOrEqualTo:
					val = filterValue.LessThanOrEqualTo
				}

				if val != nil {
					if strings.Contains(string(dateKey), string(FilterGreaterThan)) {
						minAxis = helper.GetTimestamp(val)
					} else if strings.Contains(string(dateKey), string(FilterGreaterThanOrEqualTo)) {
						minAxis = helper.GetTimestamp(val)
					}

					if strings.Contains(string(dateKey), string(FilterLessThan)) {
						maxAxis = helper.GetTimestamp(val)
					} else if strings.Contains(string(dateKey), string(FilterLessThanOrEqualTo)) {
						maxAxis = helper.GetTimestamp(val)
					}
				}
			}
		}
	}

	// console.Error(dateKeys, minAxis, maxAxis, cb.dataModel.where)
	// Set date ranges based on periodicity
	// console.Log("Initial minAxis and maxAxis:", minAxis, maxAxis)
	var startDate, endDate time.Time

	if minAxis != nil {
		startDate = helper.GetTimestamp(minAxis)
	}
	if maxAxis != nil {
		endDate = helper.GetTimestamp(maxAxis)
	}

	if periodicity == string(PeriodicityHourly) && xAxis != "" {
		if endDate.IsZero() {
			endDate = helper.GetTimestamp(nil)
		}
		if startDate.IsZero() {
			startDate = helper.GetTimestamp(nil).AddDate(0, 0, -1)
		}

		if endDate.Sub(startDate) <= 0 {
			endDate = helper.GetTimestamp(nil)
		}
	} else if periodicity == string(PeriodicityDaily) && xAxis != "" {
		if startDate.IsZero() {
			startDate = helper.GetTimestamp(nil).AddDate(0, 0, -31)
		}
		if endDate.IsZero() {
			endDate = helper.GetTimestamp(nil)
		}
	} else if periodicity == string(PeriodicityWeekly) && xAxis != "" {
		if startDate.IsZero() {
			startDate = helper.GetTimestamp(nil).AddDate(0, 0, -84) // 12 weeks
		}
		if endDate.IsZero() {
			endDate = helper.GetTimestamp(nil)
		}
	} else if periodicity == string(PeriodicityMonthly) && xAxis != "" {
		if startDate.IsZero() {
			startDate = helper.GetTimestamp(nil).AddDate(0, -12, 0)
		}
		if endDate.IsZero() {
			endDate = helper.GetTimestamp(nil)
		}
	} else if periodicity == string(PeriodicityYearly) && xAxis != "" {
		if startDate.IsZero() {
			startDate = helper.GetTimestamp(nil).AddDate(-4, 0, 0)
		}
		if endDate.IsZero() {
			endDate = helper.GetTimestamp(nil)
		}
	} else if periodicity == string(PeriodicityNone) && xAxis != "" {
		if startDate.IsZero() {
			startDate = helper.GetTimestamp(nil).AddDate(-1, 0, 0)
		}
		if endDate.IsZero() {
			endDate = helper.GetTimestamp(nil)
		}
	}

	// console.Log("periodicity", periodicity)
	// console.Log("periodicity", string(PeriodicityMonthly))

	// Configure grouping based on periodicity and parameters
	cb.configureGrouping(periodicity, xAxis, groupBy, targetKey, yAxis, TotalType(totalType))

	// console.Log("cb.dataModel", cb.dataModel.where)
	localList := cb.dataModel.Find(nil)
	dataList := helper.ToMapList[interface{}](*localList)
	// console.Log("dataList", dataList)

	// Format data based on chart type
	var err error
	if chartData.Type == ChartTypePie {
		chartData, err = cb.getPieChartFormat(dataList, context.Params, startDate, endDate, isAdmin, xAxis, TotalType(totalType), Periodicity(periodicity))
	} else {
		chartData, err = cb.getLinearChartFormat(dataList, context.Params, startDate, endDate, isAdmin, xAxis, yAxis, TotalType(totalType), Periodicity(periodicity), groupBy)
	}
	// console.Log("chartData", chartData)

	if err != nil {
		return nil, err
	}

	return chartData, nil
}

// configureGrouping configures the data model grouping
func (cb *ChartBuilder) configureGrouping(periodicity string, xAxis string, groupBy string, targetKey string, yAxis string, totalType TotalType) {
	datePeriods := []string{string(PeriodicityHourly), string(PeriodicityDaily), string(PeriodicityDayHours), string(PeriodicityWeekly), string(PeriodicityWeekDays), string(PeriodicityMonthly), string(PeriodicityMonthDays), string(PeriodicityQuarterly), "SEMIANNUALLY", string(PeriodicityYearly), string(PeriodicityYearMonth)}

	var periodFormat string
	switch periodicity {
	case string(PeriodicityHourly):
		periodFormat = "%Y-%m-%d %H:00"
	case string(PeriodicityDaily):
		periodFormat = "%Y-%m-%d"
	case string(PeriodicityWeekly):
		periodFormat = "%V-Week-%Y"
	case string(PeriodicityMonthly):
		periodFormat = "%Y-%m"
	case string(PeriodicityYearly):
		periodFormat = "%Y"
	case string(PeriodicityNone):
		periodFormat = fmt.Sprintf("`%s`", xAxis)
	}

	// Configure grouping
	if periodFormat != "" && groupBy != "" && cb.contains(datePeriods, periodicity) {
		groupByConfig := map[string]interface{}{
			"period": map[string]interface{}{
				"$dateToString": map[string]interface{}{
					"format": periodFormat,
					"date":   fmt.Sprintf("$%s", xAxis),
				},
			},
			"group": fmt.Sprintf("$%s", groupBy),
		}
		cb.dataModel.GroupByRaw("_id", groupByConfig)
	} else if periodFormat != "" && cb.contains(datePeriods, periodicity) {
		groupByConfig := map[string]interface{}{
			"period": map[string]interface{}{
				"$dateToString": map[string]interface{}{
					"format": periodFormat,
					"date":   fmt.Sprintf("$%s", xAxis),
				},
			},
		}
		cb.dataModel.GroupByRaw("_id", groupByConfig)
	} else if helper.IsNotEmpty(xAxis) && helper.IsNotEmpty(groupBy) && periodicity == "NONE" {
		groupByConfig := map[string]interface{}{
			"group":     fmt.Sprintf("$%s", groupBy),
			"dimension": fmt.Sprintf("$%s", xAxis),
		}
		cb.dataModel.GroupByRaw("_id", groupByConfig)
	} else if helper.IsEmpty(xAxis) && helper.IsNotEmpty(groupBy) && periodicity == "NONE" {
		groupByConfig := map[string]interface{}{
			"group": fmt.Sprintf("$%s", groupBy),
		}
		cb.dataModel.GroupByRaw("_id", groupByConfig)
	} else if helper.IsNotEmpty(xAxis) && helper.IsEmpty(groupBy) && periodicity == "NONE" {
		groupByConfig := map[string]interface{}{
			"dimension": fmt.Sprintf("$%s", xAxis),
		}
		cb.dataModel.GroupByRaw("_id", groupByConfig)
	}

	// Configure aggregation
	switch totalType {
	case TotalTypeSum:
		cb.dataModel.GroupByRaw("total", map[string]interface{}{"$sum": fmt.Sprintf("$%s", targetKey)})
	case TotalTypeMax:
		cb.dataModel.GroupByRaw("total", map[string]interface{}{"$max": fmt.Sprintf("$%s", targetKey)})
	case TotalTypeMin:
		cb.dataModel.GroupByRaw("total", map[string]interface{}{"$min": fmt.Sprintf("$%s", targetKey)})
	case TotalTypeAvg, TotalTypeAverage:
		cb.dataModel.GroupByRaw("total", map[string]interface{}{"$avg": fmt.Sprintf("$%s", targetKey)})
	default:
		cb.dataModel.GroupByRaw("total", map[string]interface{}{"$sum": 1})
	}
}

// getPieChartFormat formats data for pie charts
func (cb *ChartBuilder) getPieChartFormat(collection []map[string]interface{}, params map[string]interface{}, startDate, endDate time.Time, isAdmin bool, groupBy string, totalType TotalType, periodicity Periodicity) (*ChartData, error) {
	data := &ChartData{
		Type: ChartTypePie,
	}

	// console.Log("groupBy", groupBy, "xAxis", groupBy)
	groups := cb.getGroups(params, groupBy, true, helper.GetFirst(helper.GetValueOfMap(params, "dimensionBreakdownWhere")))
	groupValues := make(map[string]float64)

	for key := range groups {
		groupValues[key] = 0
	}

	// Process collection data
	for key := range groups {
		for _, row := range collection {
			if id, ok := row["_id"].(map[string]interface{}); ok {
				if group, exists := id["dimension"]; exists && group == key {
					if total, ok := row["total"].(float64); ok {
						groupValues[key] += total
					} else if totalStr, ok := row["total"].(string); ok {
						if total, err := strconv.ParseFloat(totalStr, 64); err == nil {
							groupValues[key] += total
						}
					}
				}
			} else if group, ok := row["dimension"]; ok && group == key {
				if total, ok := row["total"].(float64); ok {
					groupValues[key] += total
				} else if totalStr, ok := row["total"].(string); ok {
					if total, err := strconv.ParseFloat(totalStr, 64); err == nil {
						groupValues[key] += total
					}
				}
			}
		}
	}

	// Create dataset
	dataset := ChartDataset{
		Label:           "",
		Color:           []string{},
		BackgroundColor: []string{},
		Data:            []float64{},
	}

	count := 0
	labels := make([]string, 0)
	sortedKeys := helper.SortedKeys(groups)
	for _, key := range sortedKeys {
		color := cb.getRandomColor(count)
		labels = append(labels, groups[key])

		dataset.Data = append(dataset.Data, groupValues[key])
		dataset.Color = append(dataset.Color, color)
		dataset.BackgroundColor = append(dataset.BackgroundColor, color)
		count++
	}

	data.Labels = labels
	data.Datasets = []ChartDataset{dataset}

	return data, nil
}

// getLinearChartFormat formats data for linear charts
func (cb *ChartBuilder) getLinearChartFormat(collection []map[string]interface{}, params map[string]interface{}, startDate, endDate time.Time, isAdmin bool, xAxis, yAxis string, totalType TotalType, periodicity Periodicity, groupBy string) (*ChartData, error) {
	data := &ChartData{
		Type: ChartTypeLinear,
	}

	groups := cb.getGroups(params, groupBy, true, helper.GetFirst(helper.GetValueOfMap(params, "dimensionBreakdownWhere")))
	datasets := make(map[string]ChartDataset)
	groupKeys := make([]string, 0, len(groups))
	for k := range groups {
		groupKeys = append(groupKeys, k)
	}
	sort.Strings(groupKeys) // or sort.Slice() for custom sorting

	count := 0
	for _, key := range groupKeys {
		color := cb.getRandomColor(count)
		datasets[key] = ChartDataset{
			Label:           groups[key],
			Color:           []string{color},
			BackgroundColor: []string{color},
			Data:            []float64{},
		}
		count++
	}

	// Get periods
	periods := cb.getPeriods(params, startDate, endDate, isAdmin, xAxis, yAxis, totalType, periodicity, groupBy)
	// console.Log("periods", startDate, periods)

	periodKeys := make([]string, 0, len(periods))
	for k := range periods {
		periodKeys = append(periodKeys, k)
	}
	sort.Strings(periodKeys) // or sort.Slice() for custom sorting

	for _, keyPeriod := range periodKeys {
		valuePeriod := periods[keyPeriod]
		data.Labels = append(data.Labels, valuePeriod)
		groupValues := make(map[string]float64)

		for key := range groups {
			groupValues[key] = 0
			// console.Log("keyPeriod", keyPeriod, "keyGroup", key)

			for _, row := range collection {
				// console.Log(key, keyPeriod, row)

				// console.Log("matched row", row)
				if cb.matchesGroupAndPeriod(row, key, keyPeriod) {
					if total, ok := row["total"].(float64); ok {
						groupValues[key] = total
					} else if totalStr, ok := row["total"].(string); ok {
						if total, err := strconv.ParseFloat(totalStr, 64); err == nil {
							groupValues[key] = total
						}
					}
				}
			}
		}

		// console.Success("groupValues", keyPeriod, groupValues)
		// Add values to datasets
		for key, value := range groupValues {
			if dataset, exists := datasets[key]; exists {
				dataset.Data = append(dataset.Data, value)
				datasets[key] = dataset
			}
		}
	}

	// Convert to response format
	sortedKeys := helper.SortedKeys(datasets)

	for _, key := range sortedKeys {
		data.Datasets = append(data.Datasets, datasets[key])
	}

	return data, nil
}

// Helper methods
func (cb *ChartBuilder) getStringParam(params map[string]interface{}, key, defaultValue string) string {
	if val, ok := params[key].(string); ok {
		return val
	}

	return defaultValue
}

func (cb *ChartBuilder) getColumn(column string) string {
	// Placeholder for column name processing
	return column
}

func (cb *ChartBuilder) contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func (cb *ChartBuilder) getGroups(params map[string]interface{}, groupBy string, hasNull bool, where interface{}) map[string]string {
	// Placeholder implementation - would need actual group resolution logic
	sources := []map[string]map[string]string{}
	values := map[string]string{
		"all": "All",
	}

	if helper.IsNotEmpty(groupBy) {
		if modelData, ok := Server.resolverChartGroupData[groupBy]; ok {
			model := Server.models[modelData.ClassName]

			if model != nil {
				modelQuery := model.Query()
				modelQuery.WhereAll(where)
				groupByConfig := map[string]interface{}{
					"id":   fmt.Sprintf("$%s", model.PrimaryKey),
					"name": fmt.Sprintf("$%s", model.PrimaryName),
				}
				modelQuery.GroupByRaw("_id", groupByConfig)
				modelQuery.OrderBy(fmt.Sprintf("_id.%s", model.PrimaryName), "ASC")

				list := modelQuery.Find(where)
				for _, v := range *list {
					if vi, ok := v["_id"]; ok {
						sources = append(sources, map[string]map[string]string{
							"_id": helper.ToMap[string](vi),
						})
					}
				}
			}
		} else {
			model := cb.dataModel.Model
			if hasNull {
				sources = append(sources, map[string]map[string]string{
					"_id": {
						"id":   "_none_",
						"name": "None",
					},
				})
			}
			for _, v := range model.Fields {
				if v.Name == groupBy {
					for _, vi := range v.Options {
						sources = append(sources, map[string]map[string]string{
							"_id": {
								"id":   vi.Value,
								"name": vi.Label,
							},
						})
					}

					break
				}
			}
		}
	}

	if len(sources) > 0 {
		if len(sources) > 16 {
			sources = sources[0:17]
		}

		values = map[string]string{}
		for _, v := range sources {
			values[v["_id"]["id"]] = v["_id"]["name"]
		}
	}

	return values
}

func (cb *ChartBuilder) getPeriods(params map[string]interface{}, startDate, endDate time.Time, isAdmin bool, xAxis, yAxis string, totalType TotalType, periodicity Periodicity, groupBy string) map[string]string {
	periods := make(map[string]string)

	if periodicity == PeriodicityNone {
		return cb.getGroups(params, xAxis, false, helper.GetFirst(helper.GetValueOfMap(params, "dimensionWhere")))
	}

	var period string
	var format string
	var formatKey string

	switch periodicity {
	case PeriodicityHourly:
		period = "hour"
		format = "Jan 2, 15:00"
		formatKey = "2006-01-02 15:00"
		endDate = endDate.Add(time.Hour)
	case PeriodicityDaily:
		period = "day"
		format = "2006-01-02"
		formatKey = "2006-01-02"
		endDate = endDate.AddDate(0, 0, 0)
	case PeriodicityWeekly:
		period = "week"
		format = "WW-Week-2006"
		formatKey = "WW-Week-2006"
	case PeriodicityMonthly:
		period = "month"
		format = "Jan-2006"
		formatKey = "2006-01"
		endDate = endDate.AddDate(0, 1, 0)
	case PeriodicityYearly:
		period = "year"
		format = "2006"
		formatKey = "2006"
	default:
		period = "day"
		format = "2006-01-02"
		formatKey = "2006-01-02"
	}

	// console.Error("startDate", startDate.Format("2006-01-02 15:04"), endDate.Format("2006-01-02 15:04"))
	// Generate periods between start and end date
	current := startDate

	for current.Before(endDate) || current.Equal(endDate) {
		key := current.Format(formatKey)
		value := current.Format(format)

		switch period {
		case "day":
			current = current.AddDate(0, 0, 1)
		case "week":
			_, week := current.ISOWeek()

			key = strings.ReplaceAll(current.Format(formatKey), "WW", fmt.Sprintf("%02d", week))
			value = strings.ReplaceAll(current.Format(format), "WW", fmt.Sprintf("%02d", week))

			current = current.AddDate(0, 0, 7)
		case "month":
			current = current.AddDate(0, 1, 0)
		case "year":
			current = current.AddDate(1, 0, 0)
		case "hour":
			current = current.Add(time.Hour)
		default:
			current = current.AddDate(0, 0, 1)
		}

		periods[key] = value
	}

	return periods
}

func (cb *ChartBuilder) matchesGroupAndPeriod(row map[string]interface{}, groupKey, periodKey string) bool {
	period, periodOk := row["period"]
	group, groupOk := row["group"]
	dimension, dimensionOk := row["dimension"]

	if periodOk && groupOk {
		return period == periodKey && group == groupKey
	}

	if periodOk && dimensionOk {
		return period == periodKey && dimension == groupKey
	}

	if id, ok := row["_id"].(map[string]interface{}); ok {
		period, periodOk := id["period"]
		group, groupOk := id["group"]

		if periodOk && groupOk {
			return period == periodKey && group == groupKey
		}

		dimension, dimensionOk := id["dimension"]
		if dimensionOk && groupOk {
			if groupKey == "_none_" || groupKey == "all" {
				return dimension == periodKey && group == nil
			}

			return dimension == periodKey && group == groupKey
		}

		if dimensionOk {
			if groupKey == "_none_" || groupKey == "all" {
				return dimension == periodKey && group == nil
			}
		}
	}

	if periodOk && !groupOk && !dimensionOk {
		return period == periodKey
	}

	return false
}

func (cb *ChartBuilder) matchesGroup(row map[string]interface{}, groupKey string) bool {
	group, groupOk := row["group"].(string)
	if groupOk {
		return group == groupKey
	}

	if id, ok := row["_id"].(map[string]interface{}); ok {
		group, groupOk := id["group"].(string)

		if groupOk {
			return group == groupKey
		}
	}

	return false
}

func (cb *ChartBuilder) matchesPeriod(row map[string]interface{}, periodKey string) bool {
	if period, ok := row["period"].(string); ok {
		return period == periodKey
	}

	if id, ok := row["_id"].(map[string]interface{}); ok {
		if period, ok := id["period"].(string); ok {
			return period == periodKey
		}
		if dimension, ok := id["dimension"].(string); ok {
			return dimension == periodKey
		}
	}

	return false
}

func (cb *ChartBuilder) getRandomColor(index int) string {
	// config := cb.dataModel.Model.App.Config.Theme;

	colors := []string{
		// Your original 10
		"#9966FF", "#FF6384", "#36A2EB", "#FFCE56", "#4BC0C0",
		"#FF9F40", "#FF6384", "#C9CBCF", "#4BC0C0", "#FF6384",

		// 30 New colors in the same tone
		"#55efc4", "#81ecec", "#74b9ff", "#a29bfe", "#dfe6e9",
		"#00b894", "#00cec9", "#0984e3", "#6c5ce7", "#b2bec3",
		"#ffeaa7", "#fab1a0", "#ff7675", "#fd79a8", "#636e72",
		"#fdcb6e", "#e17055", "#d63031", "#e84393", "#2d3436",
		"#badc58", "#dff9fb", "#f9ca24", "#f0932b", "#eb4d4b",
		"#6ab04c", "#c7ecee", "#7ed6df", "#e056fd", "#686de0",
	}

	return colors[index%len(colors)]
}
