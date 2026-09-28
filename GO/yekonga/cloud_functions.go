package yekonga

import (
	"errors"
	"fmt"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/console"
	"github.com/robertkonga/yekonga-server-go/helper/logger"
)

const (
	FetchTenantByDomain string = "__SET_FETCH_TENANT_BY_DOMAIN__"
	CustomCSS           string = "__SET_CUSTOM_CSS__"
	CustomConfig        string = "__SET_CUSTOM_CONFIG__"
)

type TriggerAction string

const (
	BeforeOtpTriggerAction TriggerAction = "BeforeOtp"
	AfterOtpTriggerAction  TriggerAction = "AfterOtp"

	BeforeLoginTriggerAction TriggerAction = "BeforeLogin"
	AfterLoginTriggerAction  TriggerAction = "AfterLogin"

	BeforeRegisterTriggerAction TriggerAction = "BeforeRegister"
	AfterRegisterTriggerAction  TriggerAction = "AfterRegister"

	// Before ALL
	BeforeFindTriggerAllAction TriggerAction = "AllBeforeFind"
	AfterFindTriggerAllAction  TriggerAction = "AllAfterFind"

	BeforeCreateTriggerAllAction TriggerAction = "AllBeforeCreate"
	AfterCreateTriggerAllAction  TriggerAction = "AllAfterCreate"

	BeforeUpdateTriggerAllAction TriggerAction = "AllBeforeUpdate"
	AfterUpdateTriggerAllAction  TriggerAction = "AllAfterUpdate"

	BeforeDeleteTriggerAllAction TriggerAction = "AllBeforeDelete"
	AfterDeleteTriggerAllAction  TriggerAction = "AllAfterDelete"

	// Before Specific model
	BeforeFindTriggerAction TriggerAction = "BeforeFind"
	AfterFindTriggerAction  TriggerAction = "AfterFind"

	BeforeCreateTriggerAction TriggerAction = "BeforeCreate"
	AfterCreateTriggerAction  TriggerAction = "AfterCreate"

	BeforeUpdateTriggerAction TriggerAction = "BeforeUpdate"
	AfterUpdateTriggerAction  TriggerAction = "AfterUpdate"

	BeforeDeleteTriggerAction TriggerAction = "BeforeDelete"
	AfterDeleteTriggerAction  TriggerAction = "AfterDelete"
)

type ContextKey string

const (
	AccessTokenKey      ContextKey = "access_token"
	RefreshTokenKey     ContextKey = "refresh_token"
	UserInfoPayloadKey  ContextKey = "userInfoPayload"
	MasterKey           ContextKey = "masterKey"
	ClientPayloadKey    ContextKey = "clientPayload"
	TokenPayloadKey     ContextKey = "tokenPayload"
	CurrentTenantId     ContextKey = "currentTenantId"
	CurrentTenantConfig ContextKey = "currentTenantConfig"
	RequestKey          ContextKey = "requestObject"
	YekongaKey          ContextKey = "yekongaObject"
	RequestContextKey   ContextKey = "requestContext"
	ResponseContextKey  ContextKey = "responseContext"
)

type PrimaryCloudKey string

const (
	SendSMSCloudFunctionKey      PrimaryCloudKey = "__SendSMS__"
	SendEmailCloudFunctionKey    PrimaryCloudKey = "__SendEmail__"
	SendWhatsappCloudFunctionKey PrimaryCloudKey = "__SendWhatsapp__"
	PaymentCloudFunctionKey      PrimaryCloudKey = "__Payment__"
)

// AddCloudFunction registers a new cloud function
func (y *YekongaData) Define(name string, fn CloudFunction) error {
	y.mut.Lock()
	defer y.mut.Unlock()

	if _, exists := y.functions[name]; exists {
		return fmt.Errorf("cloud function %s already exists", name)
	}

	y.functions[name] = fn
	logger.Error("Registered cloud function", name)
	return nil
}

func (y *YekongaData) Run(name string, data interface{}, ctx *RequestContext) (interface{}, error) {
	y.mut.RLock()
	fun, exists := y.functions[name]
	y.mut.RUnlock()

	if exists {
		if ctx == nil {
			ctx = &RequestContext{}
		}

		return fun(data, ctx)
	}

	return nil, nil
}

// AddCloudFunction registers a new cloud function
func (y *YekongaData) Action(model string, action string, accessRole interface{}, route interface{}, fn ActionCloudFunction) error {
	y.mut.Lock()
	defer y.mut.Unlock()

	if y.graphqlActionFunctions[model] == nil {
		y.graphqlActionFunctions[model] = make(map[string]map[string]ActionCloudFunction)
	}

	if y.graphqlActionFunctions[model][action] == nil {
		y.graphqlActionFunctions[model][action] = make(map[string]ActionCloudFunction)
	}

	actionAccess := ""

	if v, ok := accessRole.(string); ok {
		actionAccess = v
	}

	if v, ok := route.(string); ok {
		if helper.IsEmpty(actionAccess) {
			actionAccess = v
		} else {
			actionAccess += "_" + v
		}
	}

	actionAccess = helper.ToSlug(actionAccess)

	if _, exists := y.graphqlActionFunctions[model][action][actionAccess]; exists {
		return fmt.Errorf("cloud function %s -> %v -> %v already exists", model, action, accessRole)
	}

	y.graphqlActionFunctions[model][action][actionAccess] = fn
	logger.Error("Registered cloud function: %s -> %v -> %v", model, action, accessRole)
	return nil
}

// AddCloudFunction registers a new cloud function
func (y *YekongaData) actionCallback(model string, action string, ctxRequest *RequestContext, ctxQuery *QueryContext) (interface{}, error) {
	y.mut.RLock()

	if y.graphqlActionFunctions[model] == nil {
		y.mut.RUnlock()
		return nil, errors.New("not exists")
	}

	if y.graphqlActionFunctions[model][action] == nil {
		y.mut.RUnlock()
		return nil, errors.New("not exists")
	}

	actionAccess := ctxQuery.AccessRole

	if helper.IsEmpty(actionAccess) {
		actionAccess = ctxQuery.Route
	} else {
		actionAccess += "_" + ctxQuery.Route
	}

	actionAccess = helper.ToSlug(actionAccess)

	if _, exists := y.graphqlActionFunctions[model][action][actionAccess]; exists {
		var result interface{}
		var err error

		result, err = y.graphqlActionFunctions[model][action][actionAccess](ctxRequest, ctxQuery)

		y.mut.RUnlock()

		return result, err
	}

	return nil, errors.New("not exists")
}

func (y *YekongaData) BeforeOtp(fn TriggerFunction) interface{} {
	return y.setAuthTrigger(BeforeOtpTriggerAction, fn)
}

func (y *YekongaData) AfterOtp(fn TriggerFunction) interface{} {
	return y.setAuthTrigger(AfterOtpTriggerAction, fn)
}

func (y *YekongaData) BeforeLogin(fn TriggerFunction) interface{} {
	return y.setAuthTrigger(BeforeLoginTriggerAction, fn)
}

func (y *YekongaData) AfterLogin(fn TriggerFunction) interface{} {
	return y.setAuthTrigger(AfterLoginTriggerAction, fn)
}

func (y *YekongaData) BeforeRegister(fn TriggerFunction) interface{} {
	return y.setAuthTrigger(BeforeRegisterTriggerAction, fn)
}

func (y *YekongaData) AfterRegister(fn TriggerFunction) interface{} {
	return y.setAuthTrigger(AfterRegisterTriggerAction, fn)
}

func (y *YekongaData) BeforeFindAll(fn TriggerAllFunction) interface{} {
	return y.setTriggerAll(BeforeFindTriggerAllAction, fn)
}

func (y *YekongaData) BeforeFind(model string, accessRole interface{}, route interface{}, fn TriggerFunction) interface{} {
	return y.setTrigger(model, BeforeFindTriggerAction, accessRole, route, fn)
}

func (y *YekongaData) AfterFindAll(fn TriggerAllFunction) interface{} {
	return y.setTriggerAll(AfterFindTriggerAllAction, fn)
}

func (y *YekongaData) AfterFind(model string, accessRole interface{}, route interface{}, fn TriggerFunction) interface{} {
	return y.setTrigger(model, AfterFindTriggerAction, accessRole, route, fn)
}

func (y *YekongaData) BeforeCreateAll(fn TriggerAllFunction) interface{} {
	return y.setTriggerAll(BeforeCreateTriggerAllAction, fn)
}

func (y *YekongaData) BeforeCreate(model string, accessRole interface{}, route interface{}, fn TriggerFunction) interface{} {
	return y.setTrigger(model, BeforeCreateTriggerAction, accessRole, route, fn)
}

func (y *YekongaData) AfterCreateAll(fn TriggerAllFunction) interface{} {
	return y.setTriggerAll(AfterCreateTriggerAllAction, fn)
}

func (y *YekongaData) AfterCreate(model string, accessRole interface{}, route interface{}, fn TriggerFunction) interface{} {
	return y.setTrigger(model, AfterCreateTriggerAction, accessRole, route, fn)
}

func (y *YekongaData) BeforeUpdateAll(fn TriggerAllFunction) interface{} {
	return y.setTriggerAll(BeforeUpdateTriggerAllAction, fn)
}

func (y *YekongaData) BeforeUpdate(model string, accessRole interface{}, route interface{}, fn TriggerFunction) interface{} {
	return y.setTrigger(model, BeforeUpdateTriggerAction, accessRole, route, fn)
}

func (y *YekongaData) AfterUpdateAll(fn TriggerAllFunction) interface{} {
	return y.setTriggerAll(AfterUpdateTriggerAllAction, fn)
}

func (y *YekongaData) AfterUpdate(model string, accessRole interface{}, route interface{}, fn TriggerFunction) interface{} {
	return y.setTrigger(model, AfterUpdateTriggerAction, accessRole, route, fn)
}

func (y *YekongaData) BeforeDeleteAll(fn TriggerAllFunction) interface{} {
	return y.setTriggerAll(BeforeDeleteTriggerAllAction, fn)
}

func (y *YekongaData) BeforeDelete(model string, accessRole interface{}, route interface{}, fn TriggerFunction) interface{} {
	return y.setTrigger(model, BeforeDeleteTriggerAction, accessRole, route, fn)
}

func (y *YekongaData) AfterDeleteAll(fn TriggerAllFunction) interface{} {
	return y.setTriggerAll(AfterDeleteTriggerAllAction, fn)
}

func (y *YekongaData) AfterDelete(model string, accessRole interface{}, route interface{}, fn TriggerFunction) interface{} {
	return y.setTrigger(model, AfterDeleteTriggerAction, accessRole, route, fn)
}

// AddCloudFunction registers a new cloud function
func (y *YekongaData) setTrigger(model string, action TriggerAction, accessRole interface{}, route interface{}, fn TriggerFunction) error {
	y.mut.Lock()
	defer y.mut.Unlock()

	if y.triggerFunctions == nil {
		y.triggerFunctions = make(map[string]map[TriggerAction]map[string]TriggerFunction)
	}

	if y.triggerFunctions[model] == nil {
		y.triggerFunctions[model] = map[TriggerAction]map[string]TriggerFunction{}
	}

	if y.triggerFunctions[model][action] == nil {
		y.triggerFunctions[model][action] = map[string]TriggerFunction{}
	}

	actionAccess := ""

	if v, ok := accessRole.(string); ok {
		actionAccess = v
	}

	if v, ok := route.(string); ok {
		if helper.IsEmpty(actionAccess) {
			actionAccess = v
		} else {
			actionAccess += "_" + v
		}
	}

	actionAccess = helper.ToSlug(actionAccess)

	if _, exists := y.triggerFunctions[model][action][actionAccess]; exists {
		logger.Error("cloud function %s -> %v -> %v ->  %v already exists", model, action, accessRole, route)
		return nil
	}

	y.triggerFunctions[model][action][actionAccess] = fn
	// logger.Warn("Registered cloud function: %s -> %v -> %v -> %v", model, action, accessRole, route)
	return nil
}

// AddCloudFunction registers a new cloud function
//
// The lock only covers the map lookup. Triggers run user code that usually
// queries the database (and can fire nested triggers), so holding y.mut while
// they run would block every writer and risk a recursive-RLock deadlock.
func (y *YekongaData) triggerCallback(model string, action TriggerAction, ctxRequest *RequestContext, ctxQuery *QueryContext) (interface{}, error) {
	y.mut.RLock()
	modelTriggers := y.triggerFunctions[model]
	hasAction := modelTriggers[action] != nil
	y.mut.RUnlock()

	if modelTriggers == nil {
		return nil, fmt.Errorf("%v model not exists, action %v", model, string(action))
	}

	if !hasAction {
		return nil, fmt.Errorf("%v -> %v action not exists", model, action)
	}

	actionAccess := ctxQuery.AccessRole

	if actionAccess == "" {
		actionAccess = ctxQuery.Route
	} else {
		actionAccess += "_" + ctxQuery.Route
	}

	actionAccess = helper.ToSlug(actionAccess)

	y.mut.RLock()
	fn, exists := y.triggerFunctions[model][action][actionAccess]
	y.mut.RUnlock()

	if exists {
		result, err := fn(ctxRequest, ctxQuery)

		if err != nil {
			console.Error("Error:triggerCallback", model, action, actionAccess, err.Error())
		}
		return result, err
	} else {
		if actionAccess != "" {
			if missingTriggerLog.first(model + "|" + string(action) + "|" + actionAccess) {
				logger.Warn("cloud function %s -> %v -> %v not exists", model, action, actionAccess)
			}
			return false, fmt.Errorf("%v -> %v -> %v action not exists", model, action, actionAccess)
		}
	}

	return nil, errors.New("not exists")
}

// AddCloudFunction registers a new cloud function
func (y *YekongaData) setAuthTrigger(action TriggerAction, fn TriggerFunction) error {
	y.mut.Lock()
	defer y.mut.Unlock()

	if y.authTriggerFunctions == nil {
		y.authTriggerFunctions = make(map[TriggerAction]TriggerFunction)
	}

	y.authTriggerFunctions[action] = fn
	logger.Warn("Registered Auth cloud function: %s", action)

	return nil
}

// AddCloudFunction registers a new cloud function
func (y *YekongaData) authTriggerCallback(action TriggerAction, ctxRequest *RequestContext, ctxQuery *QueryContext) (interface{}, error) {
	y.mut.RLock()
	fn, exists := y.authTriggerFunctions[action]
	y.mut.RUnlock()

	if exists {
		return fn(ctxRequest, ctxQuery)
	}

	return nil, errors.New("not exists")
}

// AddCloudFunction registers a new cloud function
func (y *YekongaData) setTriggerAll(action TriggerAction, fn TriggerAllFunction) error {
	y.mut.Lock()
	defer y.mut.Unlock()

	if y.triggerAllFunctions == nil {
		y.triggerAllFunctions = map[TriggerAction]TriggerAllFunction{}
	}

	if y.triggerAllFunctions[action] == nil {
		y.triggerAllFunctions[action] = fn

		logger.Warn("Registered cloud function: %s", action)
	} else {
		logger.Error("Registered Trigger All function: %s All ready exists", action)
	}

	return nil
}

// AddCloudFunction registers a new cloud function
func (y *YekongaData) triggerAllCallback(action TriggerAction, model *DataModel, ctxRequest *RequestContext, ctxQuery *QueryContext) (interface{}, error) {
	y.mut.RLock()
	fn := y.triggerAllFunctions[action]
	y.mut.RUnlock()

	if fn == nil {
		return nil, fmt.Errorf("%v -> action not exists", action)
	}

	return fn(model, ctxRequest, ctxQuery)
}

// AddCloudFunction registers a new cloud function
func (y *YekongaData) SetCustomCSS(fn SystemHandler) error {
	y.mut.Lock()
	defer y.mut.Unlock()

	if _, exists := y.systemFunctions[CustomCSS]; exists {
		return fmt.Errorf("cloud function %s already exists", CustomCSS)
	}

	y.systemFunctions[CustomCSS] = fn
	logger.Error("Registered system cloud function SetCustomCSS")
	return nil
}

func (y *YekongaData) CustomCSS(req *Request, res *Response) (interface{}, error) {
	y.mut.RLock()
	fun, exists := y.systemFunctions[CustomCSS]
	y.mut.RUnlock()

	if exists {
		return fun(req, res)
	}

	return nil, errors.New("Custom CSS not set")
}

// AddCloudFunction registers a new cloud function
func (y *YekongaData) SetFetchTenantByDomain(fn CloudFunction) error {
	y.mut.Lock()
	defer y.mut.Unlock()

	if _, exists := y.functions[FetchTenantByDomain]; exists {
		return fmt.Errorf("cloud function %s already exists", FetchTenantByDomain)
	}

	y.functions[FetchTenantByDomain] = fn
	logger.Error("Registered system cloud function SetFetchTenantByDomain")
	return nil
}

func (y *YekongaData) FetchTenantByDomain(data any, req *Request, res *Response) (interface{}, error) {
	y.mut.RLock()
	fun, exists := y.functions[FetchTenantByDomain]
	y.mut.RUnlock()

	var tenant any
	var tenantId any
	var domain any
	var err error
	var response any
	if value, ok := data.(string); ok {
		domain = value
	} else {
		domain = helper.GetValueOf(data, "domain")

		if helper.IsEmpty(domain) {
			domain = helper.GetValueOf(data, "host")
		}
	}

	// console.Log("FetchTenantByDomain domain", domain)

	if helper.IsNotEmpty(domain) {
		if y.Config.HasTenantCatch {
			if record := y.tenantCatchByDomain(domain); record != nil {
				tenant = record
			}

			if helper.IsNotEmpty(tenant) {
				tenantId = helper.GetValueOf(tenant, "tenantId")
			}
		}

		if exists && helper.IsEmpty(tenantId) {
			auth := req.Auth()
			client := req.Client()
			tokenPayload := req.TokenPayload()

			ctx := RequestContext{
				App:          y,
				Auth:         auth,
				Request:      req,
				Response:     res,
				Client:       client,
				TokenPayload: tokenPayload,
			}
			response, err = fun(data, &ctx)
			// console.Log("FetchTenantByDomain response", response, "err", err)

			if err == nil {
				domain = helper.GetValueOf(response, "domain")
				tenantId = helper.GetValueOf(response, "tenantId")

				if y.Config.HasTenantCatch && helper.IsNotEmpty(domain) && helper.IsNotEmpty(tenantId) {
					tenant = req.App.ModelQuery(tenantCatchModelName).Create(datatype.DataMap{
						"domain":   domain,
						"tenantId": tenantId,
					})

					console.Log("Created tenant catch", tenant)
				}
			}
		}

		return datatype.DataMap{
			"domain":   domain,
			"tenantId": tenantId,
		}, err
	}

	return nil, errors.New("Fetch Tenant By Domain not set")
}

// AddCloudFunction registers a new cloud function
func (y *YekongaData) SetCustomConfig(fn SystemHandler) error {
	y.mut.Lock()
	defer y.mut.Unlock()

	if _, exists := y.systemFunctions[CustomConfig]; exists {
		return fmt.Errorf("cloud function %s already exists", CustomConfig)
	}

	y.systemFunctions[CustomConfig] = fn
	logger.Error("Registered system cloud function SetCustomConfig")
	return nil
}

func (y *YekongaData) CustomConfig(req *Request, res *Response) (interface{}, error) {
	y.mut.RLock()
	fun, exists := y.systemFunctions[CustomConfig]
	y.mut.RUnlock()

	if exists {
		return fun(req, res)
	}

	return nil, errors.New("Custom CONFIG not set")
}

func (y *YekongaData) WhenReady(fun func()) {
	y.whenReady = append(y.whenReady, fun)
}

func (y *YekongaData) runWhenReady() {
	count := len(y.whenReady)
	for i := 0; i < count; i++ {
		go y.whenReady[i]()
	}
}
