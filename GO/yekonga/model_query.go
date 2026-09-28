package yekonga

import (
	"context"

	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/console"
	"github.com/robertkonga/yekonga-server-go/plugins/graphql"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
)

type FilterOperator string

// Filter operation constants
const (
	FilterEqualTo                 FilterOperator = "equalTo"
	FilterNotEqualTo              FilterOperator = "notEqualTo"
	FilterLessThan                FilterOperator = "lessThan"
	FilterNotLessThan             FilterOperator = "notLessThan"
	FilterLessThanOrEqualTo       FilterOperator = "lessThanOrEqualTo"
	FilterNotLessThanOrEqualTo    FilterOperator = "notLessThanOrEqualTo"
	FilterGreaterThan             FilterOperator = "greaterThan"
	FilterNotGreaterThan          FilterOperator = "notGreaterThan"
	FilterGreaterThanOrEqualTo    FilterOperator = "greaterThanOrEqualTo"
	FilterNotGreaterThanOrEqualTo FilterOperator = "notGreaterThanOrEqualTo"
	FilterMatchesRegex            FilterOperator = "matchesRegex"
	FilterOptions                 FilterOperator = "options"
)

type QueryContext struct {
	Data       interface{}
	Input      interface{}
	Filters    *datatype.DataMap
	Parent     interface{}
	Params     map[string]interface{}
	AccessRole string
	Route      string
}

type DataModelQuery struct {
	Model          *DataModel
	RequestContext *RequestContext
	QueryContext   QueryContext

	isAdmin          bool
	limit            int
	page             int
	skip             int
	where            datatype.DataMap
	orderBy          map[string]string
	selection        []string
	distinct         []string
	groupBy          []string
	groupByRaw       map[string]interface{}
	skipBeforeCommit bool
	skipTenant       bool
}

func NewDataModelQuery(model *DataModel) DataModelQuery {

	return DataModelQuery{
		Model:            model,
		limit:            10,
		isAdmin:          false,
		skipBeforeCommit: false,
	}
}

func (m *DataModelQuery) NewInstance() *DataModelQuery {
	return &DataModelQuery{
		Model:            m.Model,
		isAdmin:          false,
		skipBeforeCommit: m.skipBeforeCommit,
		QueryContext: QueryContext{
			Params: make(map[string]interface{}),
		},
	}
}

func (m *DataModelQuery) SkipTenant() *DataModelQuery {
	m.skipTenant = true

	return m
}

func (m *DataModelQuery) SkipBeforeCommit() *DataModelQuery {
	m.skipBeforeCommit = true

	return m
}

func (m *DataModelQuery) Admin() *DataModelQuery {
	m.isAdmin = true

	return m
}

func (m *DataModelQuery) SetIsAdmin(isAdmin bool) *DataModelQuery {
	m.isAdmin = isAdmin

	return m
}

func (m *DataModelQuery) Where(name string, value interface{}) *DataModelQuery {
	var newValue = value
	if m.where == nil {
		m.where = make(datatype.DataMap)
	}

	if m.Model.fieldKindSets().id[name] || name == "id" || name == "_id" {
		if name == "id" || name == "_id" {
			name = "_id"
		}

		if value != nil {
			if v, ok := value.(string); ok {
				switch v {
				case string(NULLValue):
					newValue = nil
				case string(NullValue):
					newValue = nil
				case string(nullValue):
					newValue = nil
				default:
					if helper.IsNotEmpty(v) {
						newValue = helper.ObjectID(v)
					} else {
						newValue = nil
					}
				}
			} else if helper.IsArray(value) {
				array := helper.ToList[interface{}](value)
				count := len(array)
				vpi := make([]bson.ObjectID, 0, count)

				for i := 0; i < count; i++ {
					vpi = append(vpi, helper.ObjectID(array[i]))
				}

				newValue = vpi
			} else if helper.IsMap(value) {
				v := helper.ToDataMap(value)
				vp := make(map[string]interface{})

				for ki, vi := range v {
					if vii, ok := vi.(string); ok {
						switch vii {
						case string(NULLValue):
							vp[ki] = nil
						case string(NullValue):
							vp[ki] = nil
						case string(nullValue):
							vp[ki] = nil
						default:
							vp[ki] = helper.ObjectID(vii)
						}
					} else if helper.IsArray(vi) {
						array := helper.ToList[interface{}](vi)
						count := len(array)
						vpi := make([]bson.ObjectID, 0, count)

						for i := 0; i < count; i++ {
							vpi = append(vpi, helper.ObjectID(array[i]))
						}

						vp[ki] = vpi
					} else if _, isBool := vi.(bool); isBool {
						// e.g. {"exists": false}: an operator flag, not an ID.
						vp[ki] = vi
					} else {
						switch vi {
						case string(NULLValue):
							vp[ki] = nil
						case string(NullValue):
							vp[ki] = nil
						case string(nullValue):
							vp[ki] = nil
						default:
							vp[ki] = helper.ObjectID(vi)
						}
					}
				}

				newValue = vp
			}
		}
	}

	if w, ok := m.where[name]; ok {
		if w != nil && newValue != nil {
			w1, ok1 := w.(map[string]interface{})
			w2, ok2 := newValue.(map[string]interface{})

			if ok1 && ok2 {
				for kii, vii := range w2 {
					w1[kii] = vii
				}
			} else {
				m.where[name] = newValue
			}
		} else {
			m.where[name] = newValue
		}
	} else {
		m.where[name] = newValue
	}

	return m
}

func (m *DataModelQuery) WhereMany(where interface{}) *DataModelQuery {
	if m.where == nil {
		m.where = make(datatype.DataMap)
	}

	if helper.IsMap(where) {
		p := helper.ToDataMap(where)
		for k, v := range p {
			m.Where(k, v)
		}
	}

	return m
}

func (m *DataModelQuery) WhereAll(where interface{}) *DataModelQuery {
	return m.WhereMany(where)
}

func (m *DataModelQuery) Distinct(name string) *DataModelQuery {
	if m.distinct == nil {
		m.distinct = make([]string, 0, 3)
	}

	m.distinct = append(m.distinct, name)

	return m
}

func (m *DataModelQuery) DistinctAll(values []string) *DataModelQuery {
	if m.distinct == nil {
		m.distinct = make([]string, 0, 3)
	}

	m.distinct = append(m.distinct, values...)

	return m
}

func (m *DataModelQuery) OrderBy(name string, value string) *DataModelQuery {
	if m.orderBy == nil {
		m.orderBy = make(map[string]string)
	}

	m.orderBy[name] = value

	return m
}

func (m *DataModelQuery) OrderByAll(values []map[string]string) *DataModelQuery {
	if m.orderBy == nil {
		m.orderBy = make(map[string]string)
	}

	for _, o := range values {
		for k, v := range o {
			m.orderBy[k] = v
		}
	}

	return m
}

func (m *DataModelQuery) GroupBy(name string) *DataModelQuery {
	if m.groupBy == nil {
		m.groupBy = make([]string, 0, 3)
	}

	m.groupBy = append(m.groupBy, name)

	return m
}

func (m *DataModelQuery) GroupByRaw(key string, value interface{}) *DataModelQuery {
	if m.groupByRaw == nil {
		m.groupByRaw = make(map[string]interface{})
	}

	m.groupByRaw[key] = value

	return m
}

func (m *DataModelQuery) Page(value int) *DataModelQuery {
	m.page = value
	m.skip = (m.page - 1) * m.limit

	return m
}

func (m *DataModelQuery) Take(value int) *DataModelQuery {
	m.limit = value

	return m
}

func (m *DataModelQuery) Skip(value int) *DataModelQuery {
	m.skip = value

	return m
}

func (m *DataModelQuery) Create(data datatype.DataMap) interface{} {
	if !m.skipTenant {
		if m.Model.HasTenant && m.RequestContext != nil && (m.Model.App.Config.HasTenant || m.Model.App.Config.HasTenantCatch) {
			tenantId := m.getTenantId()

			if helper.IsNotEmpty(tenantId) {
				data[TenantIDKey] = helper.ObjectID(tenantId)
			}
		}
	}

	if !m.skipBeforeCommit {
		triggerBefore := m.runTriggerAction(BeforeCreateTriggerAllAction, data)
		if v, ok := triggerBefore.(bool); ok && !v {
			return nil
		} else if helper.IsMap(triggerBefore) {
			data = helper.ToDataMap(triggerBefore)
		}

		triggerBefore = m.runTriggerAction(BeforeCreateTriggerAction, data)
		if v, ok := triggerBefore.(bool); ok && !v {
			return nil
		} else if helper.IsMap(triggerBefore) {
			data = helper.ToDataMap(triggerBefore)
		}
	}

	result, err := m.collection().create(*(m.formatInputData(data, CreateInputAction)))

	if err != nil {
		return err
	}

	triggerAfter := m.runTriggerAction(AfterCreateTriggerAllAction, result)
	if helper.IsMap(triggerAfter) {
		v := helper.ToDataMap(triggerAfter)
		result = &v
	}

	triggerAfter = m.runTriggerAction(AfterCreateTriggerAction, result)
	if helper.IsMap(triggerAfter) {
		v := helper.ToDataMap(triggerAfter)
		result = &v
	}

	if result != nil {
		m.recordAuditChange("create", m.auditDocumentId(*result), nil, *result)
	}

	m.Model.App.invalidateCaches(m.Model.Name)

	m.emitDatabaseEvent("create")

	return result
}

func (m *DataModelQuery) Update(data datatype.DataMap, where interface{}) interface{} {
	m.WhereAll(where)
	m.addTenantId()

	if !m.skipBeforeCommit {
		triggerBefore := m.runTriggerAction(BeforeUpdateTriggerAllAction, data)
		if v, ok := triggerBefore.(bool); ok && !v {
			return nil
		} else if helper.IsMap(triggerBefore) {
			data = helper.ToDataMap(triggerBefore)
		}

		triggerBefore = m.runTriggerAction(BeforeUpdateTriggerAction, data)
		if v, ok := triggerBefore.(bool); ok && !v {
			return nil
		} else if helper.IsMap(triggerBefore) {
			data = helper.ToDataMap(triggerBefore)
		}
	}

	var auditOldValue *datatype.DataMap
	var result *datatype.DataMap
	var err error

	formatted := *(m.formatInputData(data, UpdateInputAction))

	if withPrevious, ok := m.collection().(interface {
		updateWithPrevious(datatype.DataMap) (*datatype.DataMap, *datatype.DataMap, error)
	}); ok && m.auditEnabled() {
		// One round trip that also returns the record as it was.
		auditOldValue, result, err = withPrevious.updateWithPrevious(formatted)
	} else {
		if m.auditEnabled() {
			auditOldValue = m.collection().findOne()
		}

		result, err = m.collection().update(formatted)
	}

	if err != nil {
		console.Log(err.Error())
		return err
	}

	triggerAfter := m.runTriggerAction(AfterUpdateTriggerAllAction, result)
	if helper.IsMap(triggerAfter) {
		v := helper.ToDataMap(triggerAfter)
		result = &v
	}

	triggerAfter = m.runTriggerAction(AfterUpdateTriggerAction, result)
	if helper.IsMap(triggerAfter) {
		v := helper.ToDataMap(triggerAfter)
		result = &v
	}

	if result != nil {
		var oldValues datatype.DataMap
		if auditOldValue != nil {
			oldValues = *auditOldValue
		}
		m.recordAuditChange("update", m.auditDocumentId(*result), oldValues, *result)
	}

	m.Model.App.invalidateCaches(m.Model.Name)

	m.emitDatabaseEvent("update")

	return result
}

func (m *DataModelQuery) UpdateMany(data datatype.DataMap, where interface{}) interface{} {
	m.WhereAll(where)
	m.addTenantId()

	if !m.skipBeforeCommit {
		triggerBefore := m.runTriggerAction(BeforeUpdateTriggerAllAction, data)
		if v, ok := triggerBefore.(bool); ok && !v {
			return nil
		} else if helper.IsMap(triggerBefore) {
			data = helper.ToDataMap(triggerBefore)
		}

		triggerBefore = m.runTriggerAction(BeforeUpdateTriggerAction, data)
		if v, ok := triggerBefore.(bool); ok && !v {
			return nil
		} else if helper.IsMap(triggerBefore) {
			data = helper.ToDataMap(triggerBefore)
		}
	}

	var auditOldValues *[]datatype.DataMap
	if m.auditEnabled() {
		auditOldValues = m.collection().find()
	}

	result, err := m.collection().updateMany(*(m.formatInputData(data, UpdateInputAction)))

	if err != nil {
		console.Log(err.Error())
		return err
	}

	triggerAfter := m.runTriggerAction(AfterUpdateTriggerAllAction, result)
	if helper.IsArray(triggerAfter) {
		v := helper.ToDataMapList(triggerAfter)
		result = &v
	}

	triggerAfter = m.runTriggerAction(AfterUpdateTriggerAction, result)
	if helper.IsArray(triggerAfter) {
		v := helper.ToDataMapList(triggerAfter)
		result = &v
	}

	if result != nil {
		oldValuesById := make(map[string]datatype.DataMap)
		if auditOldValues != nil {
			for _, old := range *auditOldValues {
				oldValuesById[m.auditDocumentId(old)] = old
			}
		}

		for _, doc := range *result {
			id := m.auditDocumentId(doc)
			m.recordAuditChange("update", id, oldValuesById[id], doc)
		}
	}

	m.Model.App.invalidateCaches(m.Model.Name)

	m.emitDatabaseEvent("update")

	return result
}

func (m *DataModelQuery) Import(data []interface{}, uniqueKeys []string) interface{} {
	if !m.skipTenant {
		if m.Model.HasTenant && m.RequestContext != nil && (m.Model.App.Config.HasTenant || m.Model.App.Config.HasTenantCatch) {
			tenantId := m.getTenantId()

			if helper.IsNotEmpty(tenantId) {
				for i := range data {
					d := helper.ToDataMap(data[i])
					d[TenantIDKey] = helper.ObjectID(tenantId)

					data[i] = d
				}
			}
		}
	}

	if !m.skipBeforeCommit {
		triggerBefore := m.runTriggerAction(BeforeCreateTriggerAllAction, data)
		if v, ok := triggerBefore.(bool); ok && !v {
			return nil
		} else if helper.IsList(triggerBefore) {
			data = helper.ToList[any](triggerBefore)
		}

		triggerBefore = m.runTriggerAction(BeforeCreateTriggerAction, data)
		if v, ok := triggerBefore.(bool); ok && !v {
			return nil
		} else if helper.IsList(triggerBefore) {
			data = helper.ToList[any](triggerBefore)
		}
	}

	uniqueKeys = append(uniqueKeys, "_id")

	message := "FAIL"
	status := false
	deleted := 0
	ignored := 0
	imported := 0
	updated := 0
	afterData := []interface{}{}

	result := map[string]interface{}{}
	formattedCreateData := make([]datatype.DataMap, 0, len(data))
	formattedUpdateData := make([]datatype.DataMap, 0, len(data))
	// inputIds := []string{}

ParentLoop:
	for _, v := range data {
		vi := helper.ToDataMap(v)

		if helper.IsNotEmpty(vi) {
			isUpdate := false
			whereData := make(datatype.DataMap)

			for _, key := range uniqueKeys {
				val := helper.GetValueOf(vi, key)
				if key == "id" || key == "_id" {
					if key == "_id" && helper.IsEmpty(val) {
						val = helper.GetValueOf(vi, "id")
					}

					if key == "id" && helper.IsEmpty(val) {
						val = helper.GetValueOf(vi, "_id")
					}

					key = "id"
				}

				if helper.IsNotEmpty(val) {
					if helper.IsNotEmpty(val) {
						whereData[key] = val
						isUpdate = true
					} else {
						ignored++
						continue ParentLoop // Skip if unique key value is empty
					}
				}
			}

			if isUpdate {
				existsData := m.NewInstance().FindOne(whereData)
				// console.Log("existsData", existsData)

				if helper.IsNotEmpty(existsData) {
					// Update existing data
					vi["_id"] = helper.GetValueOf(existsData, "_id")
					formattedUpdateData = append(formattedUpdateData, vi)
				} else {
					// Create new data
					formattedCreateData = append(formattedCreateData, *m.formatInputData(vi, ImportInputAction))
				}
			} else {
				// No unique keys found, treat as new data
				formattedCreateData = append(formattedCreateData, *m.formatInputData(vi, ImportInputAction))
			}
		}
	}

	if len(formattedCreateData) > 0 {
		createData, err := m.collection().createMany(formattedCreateData)

		if err != nil {
			console.Error("DataModelQuery.Import", err.Error())
		} else if createData != nil {
			imported = len(*createData)

			status = true
			message = "SUCCESS"
		}

		triggerAfter := m.runTriggerAction(AfterCreateTriggerAllAction, createData)
		if result, ok := triggerAfter.([]datatype.DataMap); ok {
			createData = &(result)
		}

		triggerAfter = m.runTriggerAction(AfterCreateTriggerAction, createData)
		if result, ok := triggerAfter.([]datatype.DataMap); ok {
			createData = &(result)
		}

		// Added after the triggers so the returned records include their
		// changes, as Create's result does.
		if err == nil && createData != nil {
			for _, record := range *createData {
				afterData = append(afterData, record)
			}
		}
	}

	// console.Log("formattedUpdateData", formattedUpdateData)
	if len(formattedUpdateData) > 0 {
		for _, v := range formattedUpdateData {
			updateData := m.NewInstance().Where("id", v["_id"]).Update(*m.formatInputData(v, UpdateInputAction), nil)

			if helper.IsNotEmpty(updateData) {
				updated++
				interfaceData := helper.ToDataMap(updateData)
				afterData = append(afterData, interfaceData)

				status = true
				message = "SUCCESS"
			} else {
				ignored++
			}
		}
	}

	result["message"] = message
	result["status"] = status
	result["deleted"] = deleted
	result["ignored"] = ignored
	result["imported"] = imported
	result["updated"] = updated
	result["data"] = afterData

	m.Model.App.invalidateCaches(m.Model.Name)

	m.emitDatabaseEvent("import")

	return result
}

func (m *DataModelQuery) Delete(where interface{}) interface{} {
	m.WhereAll(where)
	m.addTenantId()

	if !m.skipBeforeCommit {
		triggerBefore := m.runTriggerAction(BeforeDeleteTriggerAllAction, m.where)
		if v, ok := triggerBefore.(bool); ok && !v {
			return nil
		} else if helper.IsMap(triggerBefore) {
			m.WhereAll(helper.ToDataMap(triggerBefore))
		}

		triggerBefore = m.runTriggerAction(BeforeDeleteTriggerAction, m.where)
		if v, ok := triggerBefore.(bool); ok && !v {
			return nil
		} else if helper.IsMap(triggerBefore) {
			m.WhereAll(helper.ToDataMap(triggerBefore))
		}
	}
	var auditOldValues *[]datatype.DataMap
	if m.auditEnabled() {
		auditOldValues = m.collection().find()
	}

	result, err := m.collection().delete()

	if err != nil {
		return err
	}

	triggerAfter := m.runTriggerAction(AfterDeleteTriggerAllAction, result)
	if helper.IsMap(triggerAfter) {
		result = helper.ToDataMap(triggerAfter)
	}

	triggerAfter = m.runTriggerAction(AfterDeleteTriggerAction, result)
	if helper.IsMap(triggerAfter) {
		result = helper.ToDataMap(triggerAfter)
	}

	if auditOldValues != nil {
		for _, old := range *auditOldValues {
			m.recordAuditChange("delete", m.auditDocumentId(old), old, nil)
		}
	}

	m.Model.App.invalidateCaches(m.Model.Name)

	m.emitDatabaseEvent("delete")

	return result
}

func (m *DataModelQuery) Exist(where interface{}) bool {
	result := m.FindOne(where)

	return helper.IsNotEmpty(result)
}

func (m *DataModelQuery) Value(key string) interface{} {
	result := m.FindOne(nil)

	if helper.IsNotEmpty(result) {
		return (*result)[key]
	}

	return nil
}

func (m *DataModelQuery) List(key string) []interface{} {
	return m.Values(key)
}

func (m *DataModelQuery) Values(key string) []interface{} {
	result := m.Find(nil)

	if helper.IsNotEmpty(result) {
		var values []interface{}
		for _, v := range *result {
			values = append(values, v[key])
		}
		return values
	}

	return nil
}

func (m *DataModelQuery) First(where interface{}) *datatype.DataMap {
	return m.FindOne(where)
}

func (m *DataModelQuery) FindOne(where interface{}) *datatype.DataMap {
	if !m.prepareFind(where) {
		return nil
	}

	return m.finishFindOne(m.collection().findOne())
}

// prepareFind applies where, the tenant filter and the before-find triggers.
// It returns false when a trigger vetoed the query.
func (m *DataModelQuery) prepareFind(where interface{}) bool {
	m.WhereAll(where)
	m.addTenantId()

	if !m.skipBeforeCommit {
		triggerBefore := m.runTriggerAction(BeforeFindTriggerAllAction, m.where)
		if v, ok := triggerBefore.(bool); ok && !v {
			return false
		} else if helper.IsMap(triggerBefore) {
			m.WhereAll(helper.ToDataMap(triggerBefore))
		}

		triggerBefore = m.runTriggerAction(BeforeFindTriggerAction, m.where)
		if v, ok := triggerBefore.(bool); ok && !v {
			return false
		} else if helper.IsMap(triggerBefore) {
			m.WhereAll(helper.ToDataMap(triggerBefore))
		}
	}

	return true
}

// finishFindOne runs the after-find triggers on a fetched record.
func (m *DataModelQuery) finishFindOne(result *datatype.DataMap) *datatype.DataMap {
	triggerAfter := m.runTriggerAction(AfterFindTriggerAllAction, result)
	if helper.IsMap(triggerAfter) {
		v := helper.ToDataMap(triggerAfter)
		result = &v
	}

	triggerAfter = m.runTriggerAction(AfterFindTriggerAction, result)
	if helper.IsMap(triggerAfter) {
		v := helper.ToDataMap(triggerAfter)
		result = &v
	}

	return result
}

func (m *DataModelQuery) Find(where interface{}) *[]datatype.DataMap {
	if !m.prepareFind(where) {
		vl := make([]datatype.DataMap, 0)
		return &vl
	}

	return m.finishFind(m.collection().find())
}

// finishFind runs the after-find triggers on fetched records.
func (m *DataModelQuery) finishFind(result *[]datatype.DataMap) *[]datatype.DataMap {
	triggerAfter := m.runTriggerAction(AfterFindTriggerAllAction, result)
	if helper.IsMapList(triggerAfter) {
		v := helper.ToDataMapList(triggerAfter)
		result = &v
	}

	triggerAfter = m.runTriggerAction(AfterFindTriggerAction, result)
	if helper.IsMapList(triggerAfter) {
		v := helper.ToDataMapList(triggerAfter)
		result = &v
	}

	return result
}

func (m *DataModelQuery) Paginate(where interface{}) *datatype.DataMap {
	m.WhereAll(where)
	m.addTenantId()

	if !m.skipBeforeCommit {
		triggerBefore := m.runTriggerAction(BeforeFindTriggerAllAction, m.where)
		if v, ok := triggerBefore.(bool); ok && !v {
			return nil
		} else if helper.IsMap(triggerBefore) {
			m.WhereAll(helper.ToDataMap(triggerBefore))
		}

		triggerBefore = m.runTriggerAction(BeforeFindTriggerAction, m.where)
		if v, ok := triggerBefore.(bool); ok && !v {
			return nil
		} else if helper.IsMap(triggerBefore) {
			m.WhereAll(helper.ToDataMap(triggerBefore))
		}
	}

	result := m.collection().pagination()

	triggerAfter := m.runTriggerAction(AfterFindTriggerAllAction, result)
	if helper.IsMap(triggerAfter) {
		v := helper.ToDataMap(triggerAfter)
		result = &v
	}

	triggerAfter = m.runTriggerAction(AfterFindTriggerAction, result)
	if helper.IsMap(triggerAfter) {
		v := helper.ToDataMap(triggerAfter)
		result = &v
	}

	return result
}

func (m *DataModelQuery) Summary(where interface{}) *datatype.DataMap {
	m.WhereAll(where)
	m.addTenantId()

	if !m.skipBeforeCommit {
		result := m.runTriggerAction(BeforeFindTriggerAllAction, m.where)
		if v, ok := result.(bool); ok && !v {
			return nil
		} else if helper.IsMap(result) {
			m.WhereAll(helper.ToDataMap(result))
		}

		result = m.runTriggerAction(BeforeFindTriggerAction, m.where)
		if v, ok := result.(bool); ok && !v {
			return nil
		} else if helper.IsMap(result) {
			m.WhereAll(helper.ToDataMap(result))
		}
	}

	return m.collection().summary()
}

func (m *DataModelQuery) Count(where interface{}) int64 {
	m.WhereAll(where)
	m.addTenantId()

	if !m.skipBeforeCommit {
		result := m.runTriggerAction(BeforeFindTriggerAllAction, m.where)
		if v, ok := result.(bool); ok && !v {
			return 0
		} else if helper.IsMap(result) {
			m.WhereAll(helper.ToDataMap(result))
		}

		result = m.runTriggerAction(BeforeFindTriggerAction, m.where)
		if v, ok := result.(bool); ok && !v {
			return 0
		} else if helper.IsMap(result) {
			m.WhereAll(helper.ToDataMap(result))
		}
	}

	return int64(m.collection().count())
}

func (m *DataModelQuery) Sum(target string, where interface{}) float64 {
	m.WhereAll(where)
	m.addTenantId()

	if !m.skipBeforeCommit {
		result := m.runTriggerAction(BeforeFindTriggerAllAction, m.where)
		if v, ok := result.(bool); ok && !v {
			return 0
		} else if helper.IsMap(result) {
			m.WhereAll(helper.ToDataMap(result))
		}

		result = m.runTriggerAction(BeforeFindTriggerAction, m.where)
		if v, ok := result.(bool); ok && !v {
			return 0
		} else if helper.IsMap(result) {
			m.WhereAll(helper.ToDataMap(result))
		}
	}

	return float64(m.collection().sum(target))
}

func (m *DataModelQuery) Max(target string, where interface{}) interface{} {
	m.WhereAll(where)
	m.addTenantId()

	if !m.skipBeforeCommit {
		result := m.runTriggerAction(BeforeFindTriggerAllAction, m.where)
		if v, ok := result.(bool); ok && !v {
			return nil
		} else if helper.IsMap(result) {
			m.WhereAll(helper.ToDataMap(result))
		}

		result = m.runTriggerAction(BeforeFindTriggerAction, m.where)
		if v, ok := result.(bool); ok && !v {
			return nil
		} else if helper.IsMap(result) {
			m.WhereAll(helper.ToDataMap(result))
		}
	}

	return m.collection().max(target)
}

func (m *DataModelQuery) Min(target string, where interface{}) interface{} {
	m.WhereAll(where)
	m.addTenantId()

	if !m.skipBeforeCommit {
		result := m.runTriggerAction(BeforeFindTriggerAllAction, m.where)
		if v, ok := result.(bool); ok && !v {
			return nil
		} else if helper.IsMap(result) {
			m.WhereAll(helper.ToDataMap(result))
		}

		result = m.runTriggerAction(BeforeFindTriggerAction, m.where)
		if v, ok := result.(bool); ok && !v {
			return nil
		} else if helper.IsMap(result) {
			m.WhereAll(helper.ToDataMap(result))
		}
	}

	return m.collection().min(target)
}

func (m *DataModelQuery) Average(target string, where interface{}) float64 {
	m.WhereAll(where)
	m.addTenantId()

	if !m.skipBeforeCommit {
		result := m.runTriggerAction(BeforeFindTriggerAllAction, m.where)
		if v, ok := result.(bool); ok && !v {
			return 0
		} else if helper.IsMap(result) {
			m.WhereAll(helper.ToDataMap(result))
		}

		result = m.runTriggerAction(BeforeFindTriggerAction, m.where)
		if v, ok := result.(bool); ok && !v {
			return 0
		} else if helper.IsMap(result) {
			m.WhereAll(helper.ToDataMap(result))
		}
	}

	return m.collection().average(target)
}

func (m *DataModelQuery) Graph(where interface{}, p *graphql.ResolveParams) interface{} {
	m.WhereAll(where)
	m.addTenantId()

	ctx, _ := p.Context.Value(RequestContextKey).(*RequestContext)
	parent := p.Source

	var accessRole string = helper.GetValueOfString(p.Args, "accessRole")
	var route string = helper.GetValueOfString(p.Args, "route")

	if m.QueryContext.Params == nil {
		m.QueryContext.Params = make(map[string]interface{})
	}

	if pp, ok := p.Source.(graphql.ResolveParams); ok {
		localWhere := helper.ToDataMap(pp.Args["where"])
		if localWhere != nil {
			m.WhereAll(localWhere)
		}

		accessRole = helper.GetValueOfString(pp.Args, "accessRole")
		route = helper.GetValueOfString(pp.Args, "route")

		if pp.Args != nil {
			for k, v := range pp.Args {
				m.QueryContext.Params[k] = v
			}
		}
	}

	// console.Log("p.Params 1", m.QueryContext.Params)
	filters := helper.ConvertToDataMap(helper.GetValueOfMap(p.Args, "where"))

	m.QueryContext.AccessRole = accessRole
	m.QueryContext.Route = route
	m.QueryContext.Filters = &filters

	if p.Args != nil {
		for k, v := range p.Args {
			m.QueryContext.Params[k] = v
		}
	}

	if ctx != nil {
		m.SetRequestContext(ctx)
	}

	if p, ok := parent.(datatype.DataMap); ok {
		m.QueryContext.Parent = &p
	}

	localWhere := helper.ToDataMap(p.Args["where"])
	if localWhere != nil {
		m.WhereAll(localWhere)
	}

	localOrderBy := helper.ToMapList[string](p.Args["orderBy"])
	if localOrderBy != nil {
		m.OrderByAll(localOrderBy)
	}

	if v, ok := p.Args["limit"].(int); ok {
		m.Take(v)
	}

	if v, ok := p.Args["page"].(int); ok {
		m.Page(v)
	}

	if v, ok := p.Args["skip"].(int); ok {
		m.Skip(v)
	}

	result := m.runTriggerAction(BeforeFindTriggerAllAction, m.where)
	if v, ok := result.(bool); ok && !v {
		return nil
	} else if helper.IsMap(result) {
		m.WhereAll(helper.ToDataMap(result))
	}

	result = m.runTriggerAction(BeforeFindTriggerAction, m.where)
	if v, ok := result.(bool); ok && !v {
		return nil
	} else if helper.IsMap(result) {
		m.WhereAll(helper.ToDataMap(result))
	}

	whereFilter := make(map[string]FilterValue)
	filterOps := append(append([]string{}, graphqlOperations...), graphqlArrayOperations...)

	for k, v := range m.where {
		isFilter := false
		var vMap map[string]interface{}

		if helper.IsMap(v) {
			vMap = helper.ToDataMap(v)
			for _, op := range filterOps {
				if _, ok := vMap[op]; ok {
					isFilter = true
					break
				}
			}
		}

		if isFilter {
			fv := FilterValue{}

			if val, ok := vMap["in"]; ok {
				fv.In = helper.ToList[interface{}](val)
			}
			if val, ok := vMap["greaterThan"]; ok {
				fv.GreaterThan = val
			}
			if val, ok := vMap["lessThan"]; ok {
				fv.LessThan = val
			}
			if val, ok := vMap["greaterThanOrEqualTo"]; ok {
				fv.GreaterThanOrEqualTo = val
			}
			if val, ok := vMap["lessThanOrEqualTo"]; ok {
				fv.LessThanOrEqualTo = val
			}
			if val, ok := vMap["equalTo"]; ok {
				fv.EqualTo = val
			}
			if val, ok := vMap["notEqualTo"]; ok {
				fv.NotEqualTo = val
			}
			if val, ok := vMap["notLessThan"]; ok {
				fv.NotLessThan = val
			}
			if val, ok := vMap["notLessThanOrEqualTo"]; ok {
				fv.NotLessThanOrEqualTo = val
			}
			if val, ok := vMap["notGreaterThan"]; ok {
				fv.NotGreaterThan = val
			}
			if val, ok := vMap["notGreaterThanOrEqualTo"]; ok {
				fv.NotGreaterThanOrEqualTo = val
			}
			if val, ok := vMap["matchesRegex"]; ok {
				fv.MatchesRegex = val
			}
			if val, ok := vMap["options"]; ok {
				fv.Options = val
			}
			whereFilter[k] = fv
		} else {
			whereFilter[k] = FilterValue{EqualTo: v}
		}
	}

	// console.Log("where", where)
	// console.Log("p.Args", p.Args)
	chart := NewChartBuilder(m)

	chartData, err := chart.BuildGraph(whereFilter, false)

	if err != nil {
		return err
	}

	return helper.ToMap[any](chartData)
}

func (m *DataModelQuery) Download(where interface{}, fileType string) interface{} {
	result := make(map[string]interface{})
	data := m.Find(where)

	result["data"], _ = helper.ConvertJSONArrayToCSV(data, []string{}, "", []string{})

	return result
}

func (m *DataModelQuery) SetRequest(req *Request, res *Response) *DataModelQuery {
	m.RequestContext = &RequestContext{
		Auth:         req.Auth(),
		App:          req.App,
		Client:       req.Client(),
		TokenPayload: req.TokenPayload(),
		Request:      req,
		Response:     res,
	}

	return m
}

func (m *DataModelQuery) SetRequestContext(context *RequestContext) *DataModelQuery {
	m.RequestContext = context

	return m
}

func (m *DataModelQuery) setRequestAccessRole(value string) *DataModelQuery {
	m.QueryContext.AccessRole = value

	return m
}

func (m *DataModelQuery) setRequestRoute(value string) *DataModelQuery {
	m.QueryContext.Route = value

	return m
}

func (m *DataModelQuery) formatInputData(input datatype.DataMap, action InputAction) *datatype.DataMap {
	formatInput := datatype.DataMap{}

	switch action {
	case CreateInputAction, ImportInputAction:
		for _, k := range m.Model.ValidFields {
			var v interface{} = nil

			if k == "id" {
				if _, exist := input["_id"]; !exist {
					k = "_id"
				} else {
					v = input["_id"]
				}

				if _, exist := input["id"]; exist {
					v = input["id"]
				}
			}

			if k == "_id" {
				v = helper.ObjectID(v)
			}

			if vi, exist := input[k]; exist {
				if m.Model.fieldKindSets().id[k] {
					if helper.IsNotEmpty(vi) {
						v = helper.ObjectID(vi)
					} else {
						v = vi
					}
				} else {
					v = vi
				}
			} else if field, exist := m.Model.Fields[k]; exist {
				v = field.DefaultValue

				if m.Model.fieldKindSets().date[string(field.Name)] && v == "now" {
					v = helper.GetTimestamp(nil)
				}
			}

			formatInput[k] = m.formatInputDataField(k, v)
		}
	case UpdateInputAction:
		for _, k := range m.Model.ValidFields {
			if k != "_id" && k != "id" {
				if v, ok := input[k]; ok {
					formatInput[k] = m.formatInputDataField(k, v)
				}
			}
		}

	case ActionInputAction:
		for k, v := range input {
			formatInput[k] = m.formatInputDataField(k, v)
		}
	}

	return &formatInput
}

func (m *DataModelQuery) formatInputDataField(key string, value interface{}) interface{} {
	var v interface{}

	if helper.IsNotEmpty(value) {
		kinds := m.Model.fieldKindSets()

		if kinds.id[key] || key == "id" || key == "_id" {
			v = helper.ObjectID(value)
		} else if kinds.date[key] {
			v = helper.GetTimestamp(value)
		} else if kinds.number[key] {
			v = helper.ToInt(value)
		} else if kinds.float[key] {
			v = helper.ToFloat(value)
		} else {
			v = value
		}
	} else {
		v = value
	}

	return v
}

// isTriggerAllAction reports whether action is one of the cross-model
// ("...All") trigger actions.
func isTriggerAllAction(action TriggerAction) bool {
	switch action {
	case BeforeFindTriggerAllAction, AfterFindTriggerAllAction,
		BeforeCreateTriggerAllAction, AfterCreateTriggerAllAction,
		BeforeUpdateTriggerAllAction, AfterUpdateTriggerAllAction,
		BeforeDeleteTriggerAllAction, AfterDeleteTriggerAllAction:
		return true
	}

	return false
}

func (m *DataModelQuery) runTriggerAction(action TriggerAction, data interface{}) interface{} {
	model := m.Model

	switch action {
	case BeforeFindTriggerAction, BeforeFindTriggerAllAction:
		if v, ok := data.(datatype.DataMap); ok {
			m.QueryContext.Filters = &v
		}
	case AfterFindTriggerAction, AfterFindTriggerAllAction:
		m.QueryContext.Data = data
	case BeforeCreateTriggerAction, BeforeCreateTriggerAllAction:
		m.QueryContext.Input = data
	case AfterCreateTriggerAction, AfterCreateTriggerAllAction:
		m.QueryContext.Data = data
	case BeforeUpdateTriggerAction, BeforeUpdateTriggerAllAction:
		if v, ok := data.(datatype.DataMap); ok {
			m.QueryContext.Input = &v
		}
	case AfterUpdateTriggerAction, AfterUpdateTriggerAllAction:
		m.QueryContext.Data = data
	case BeforeDeleteTriggerAction, BeforeDeleteTriggerAllAction:
		if v, ok := data.(datatype.DataMap); ok {
			m.QueryContext.Filters = &v
		}
	case AfterDeleteTriggerAction, AfterDeleteTriggerAllAction:
		m.QueryContext.Data = data
	}

	var result interface{}
	var err error

	if isTriggerAllAction(action) {
		result, err = model.App.triggerAllCallback(action, model, m.RequestContext, &m.QueryContext)
	} else {
		result, err = model.App.triggerCallback(model.Name, action, m.RequestContext, &m.QueryContext)
	}

	if err != nil {
		// if m.Model.Name == "Outlet" {
		// 	logger.Error("runTriggerAction", action, err.Error())
		// }
	}

	return result
}

func (m *DataModelQuery) addTenantId() {
	if !m.skipTenant {
		if m.Model.HasTenant && m.RequestContext != nil && (m.Model.App.Config.HasTenant || m.Model.App.Config.HasTenantCatch) {
			payload := m.RequestContext.TokenPayload
			var tenantId interface{}

			if payload != nil && helper.IsNotEmpty(payload.TenantId) {
				tenantId = payload.TenantId
			} else if m.RequestContext.Request != nil {
				tenantId = m.RequestContext.Request.TenantId()
			}

			if helper.IsEmpty(tenantId) {
				tenantId = "000000000000000000000000"
			}

			m.Where(TenantIDKey, helper.ObjectID(tenantId))
		}
	}
}

func (m *DataModelQuery) getTenantId() interface{} {
	var tenantId interface{}

	if !m.skipTenant {
		if m.Model.HasTenant && m.RequestContext != nil && (m.Model.App.Config.HasTenant || m.Model.App.Config.HasTenantCatch) {
			payload := m.RequestContext.TokenPayload

			if payload != nil && helper.IsNotEmpty(payload.TenantId) {
				tenantId = payload.TenantId
			} else if m.RequestContext.Request != nil {
				tenantId = m.RequestContext.Request.TenantId()
			}
		}
	}

	return tenantId
}

// auditEnabled reports whether this write should be recorded on the audit trail:
// audit trail must be enabled, the query must run within a request, and the
// AuditTrail model itself is always excluded to avoid auditing its own writes.
func (m *DataModelQuery) auditEnabled() bool {
	if m.RequestContext == nil || m.RequestContext.Request == nil {
		return false
	}

	if !m.Model.App.Config.AuditTrail.Enabled || m.Model.Name == "AuditTrail" {
		return false
	}

	for _, excluded := range m.Model.App.Config.AuditTrail.ExcludeModels {
		if excluded == m.Model.Name {
			return false
		}
	}

	return true
}

func (m *DataModelQuery) auditDocumentId(data datatype.DataMap) string {
	if data == nil {
		return ""
	}

	if id := data[m.Model.PrimaryName]; helper.IsNotEmpty(id) {
		return helper.ToString(id)
	}

	return helper.ToString(data["id"])
}

// recordAuditChange buffers one data change on the current request; it is
// flushed as a single batch to the AuditTrail collection once the request ends.
func (m *DataModelQuery) recordAuditChange(action string, documentId string, oldValues, newValues datatype.DataMap) {
	if !m.auditEnabled() {
		return
	}

	m.RequestContext.Request.AddAuditChange(AuditTrailChange{
		Action:     action,
		Collection: m.Model.Collection,
		Model:      m.Model.Name,
		DocumentId: documentId,
		OldValues:  oldValues,
		NewValues:  newValues,
	})
}

// emitDatabaseEvent tells socket clients a model's data changed. A write to
// a tenant's data only goes to that tenant's clients (and clients without a
// tenant); other tenants' screens have nothing to refresh.
func (m *DataModelQuery) emitDatabaseEvent(action string) {
	tenantId := ""
	if m.Model.HasTenant {
		tenantId = idString(m.getTenantId())
	}

	m.Model.App.socketServer.Of("/").broadcastToTenant(tenantId, "database", datatype.DataMap{
		"action": action,
		"model":  m.Model.Name,
	})
}

func (m *DataModelQuery) collection() dataModelQueryStructure {
	// Queries made while handling a request stop if the client disconnects;
	// anything else (cron jobs, background work) runs to completion.
	ctx := context.Background()
	if m.RequestContext != nil && m.RequestContext.Request != nil {
		ctx = m.RequestContext.Request.DatabaseContext()
	}

	switch m.Model.DatabaseType {
	case config.DBTypeMongodb:
		return &mongodbConnection{
			query:  m,
			ctx:    &ctx,
			client: m.Model.DBConnect.mongodbClient,
		}
	case config.DBTypeSql:
		return &sqlConnection{
			query:  m,
			ctx:    &ctx,
			client: m.Model.DBConnect.sqlClient,
		}
	case config.DBTypeMysql:
		return &sqlConnection{
			query:  m,
			ctx:    &ctx,
			client: m.Model.DBConnect.mysqlClient,
		}
	}

	return &localDbConnection{
		query:  m,
		ctx:    &ctx,
		client: m.Model.DBConnect.localClient,
	}
}
