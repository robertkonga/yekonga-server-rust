package yekonga

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
)

type RequestContext struct {
	Auth             *AuthPayload
	App              *YekongaData
	Request          *Request
	Response         *Response
	Client           *ClientPayload
	TokenPayload     *TokenPayload
	QuerySelectors   map[uint][]string
	QueryRelatedData datatype.JsonObject
	QueryWhereData   datatype.JsonObject
	mut              sync.RWMutex

	relations *relationLoader // batches GraphQL relation lookups; see graphql_loader.go
}

type AuthPayload struct {
	ID string `json:"id"`

	Domain     string `json:"domain"`
	TenantID   string `json:"tenantId"`
	ProfileID  string `json:"profileId"`
	UserId     string `json:"userId"`
	AdminId    string `json:"adminId"`
	ModuleName string `json:"moduleName"`

	UsernameType string `json:"usernameType"`
	Username     string `json:"username"`
	FirstName    string `json:"firstName"`
	LastName     string `json:"lastName"`
	Phone        string `json:"phone"`
	Email        string `json:"email"`
	Whatsapp     string `json:"whatsapp"`

	Roles       []string  `json:"roles"`
	Permissions []string  `json:"permissions"`
	ExpiresAt   time.Time `json:"expiresAt"`
	Extracts    map[string]interface{}
}

func (a *AuthPayload) ToMap() map[string]interface{} {
	// // Marshal the struct into JSON
	// jsonData, _ := json.Marshal(a)

	// // Unmarshal the JSON into a map[string]interface{}
	// var result map[string]interface{}
	// json.Unmarshal(jsonData, &result)

	result := map[string]interface{}{
		"id": a.ID,

		"domain":     a.Domain,
		"tenantID":   a.TenantID,
		"profileId":  a.ProfileID,
		"userId":     a.UserId,
		"adminId":    a.AdminId,
		"moduleName": a.ModuleName,

		"usernameType": a.UsernameType,
		"username":     a.Username,
		"firstName":    a.FirstName,
		"lastName":     a.LastName,
		"phone":        a.Phone,
		"email":        a.Email,
		"whatsapp":     a.Whatsapp,

		"roles":       a.Roles,
		"permissions": a.Permissions,
		"expiresAt":   a.ExpiresAt,
		"extracts":    a.Extracts,
	}

	return result
}

func (a *AuthPayload) ToJson() string {
	result, _ := json.Marshal(a.ToMap())

	return string(result)
}

// AuditTrailChange is one recorded data change (create/update/delete),
// collected on the request and flushed in a single batch once the request ends.
type AuditTrailChange struct {
	Action     string
	Collection string
	Model      string
	DocumentId string
	OldValues  datatype.DataMap
	NewValues  datatype.DataMap
}

type TokenPayload struct {
	Domain     string      `json:"domain"`
	TenantId   interface{} `json:"tenantId"`
	ProfileId  string      `json:"profileId"`
	UserId     string      `json:"userId"`
	AdminId    string      `json:"adminId"`
	ModuleName string      `json:"moduleName"`

	UsernameType string `json:"usernameType"`
	Username     string `json:"username"`
	Phone        string `json:"phone"`
	Email        string `json:"email"`
	Whatsapp     string `json:"whatsapp"`

	Roles       []string  `json:"roles"`
	Permissions []string  `json:"permissions"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

func (a *TokenPayload) ToMap() map[string]interface{} {
	result := map[string]interface{}{
		"domain":       a.Domain,
		"tenantId":     a.TenantId,
		"profileId":    a.ProfileId,
		"userId":       a.UserId,
		"adminId":      a.AdminId,
		"moduleName":   a.ModuleName,
		"usernameType": a.UsernameType,
		"username":     a.Username,
		"phone":        a.Phone,
		"email":        a.Email,
		"whatsapp":     a.Whatsapp,
		"roles":        a.Roles,
		"permissions":  a.Permissions,
		"expiresAt":    a.ExpiresAt,
	}

	return result
}

func (a *TokenPayload) ToJson() string {
	result, _ := json.Marshal(a.ToMap())

	return string(result)
}

type ClientPayload struct {
	TenantId  interface{} `json:"tenantId"`
	Origin    string      `json:"origin"`
	Host      string      `json:"host"`
	Port      string      `json:"port"`
	Proto     string      `json:"proto"`
	Path      string      `json:"path"`
	Method    string      `json:"method"`
	UserAgent string      `json:"userAgent"`
	IpAddress string      `json:"ipAddress"`
}

func (a *ClientPayload) ToMap() map[string]interface{} {
	result := map[string]interface{}{
		"tenantId":  a.TenantId,
		"origin":    a.Origin,
		"host":      a.Host,
		"port":      a.Port,
		"protocol":  a.Proto,
		"path":      a.Path,
		"method":    a.Method,
		"userAgent": a.UserAgent,
		"ipAddress": a.IpAddress,
	}

	return result
}

func (a *ClientPayload) ToJson() string {
	result, _ := json.Marshal(a.ToMap())

	return string(result)
}

func (a *ClientPayload) OriginDomain() string {
	return helper.ExtractDomain(a.Origin)
}

// Request represents an HTTP request with additional context
type Request struct {
	HttpRequest   *http.Request
	RawBody       interface{}
	Context       datatype.Context
	ContextObject datatype.ContextObject
	Params        map[string]string
	next          func(Request, Response)
	mut           sync.RWMutex
	App           *YekongaData
	auditChanges  []AuditTrailChange

	// Auth() decoded from the user info, reused until the user info changes.
	auth       AuthPayload
	authCached bool

	dbCtx context.Context // see DatabaseContext
}

// DatabaseContext is the context this request's database reads run with. It's
// cancelled if the client disconnects while the request is being handled, so
// abandoned requests stop querying. Unlike HttpRequest.Context(), it is not
// cancelled when the handler returns, so work a handler starts in the
// background can keep using the database.
func (r *Request) DatabaseContext() context.Context {
	if r == nil || r.dbCtx == nil {
		return context.Background()
	}

	return r.dbCtx
}

// watchDisconnect sets up DatabaseContext for the duration of the handler.
// The returned func must be called when the handler returns.
func (r *Request) watchDisconnect() (stop func() bool) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.HttpRequest.Context()))
	r.dbCtx = ctx

	return context.AfterFunc(r.HttpRequest.Context(), cancel)
}

// Request methods
func (r *Request) SetContext(key string, value interface{}) {
	r.mut.Lock()
	defer r.mut.Unlock()
	if r.Context == nil {
		r.Context = make(datatype.Context)
	}

	r.Context[key] = value

	if key == string(UserInfoPayloadKey) {
		r.authCached = false
	}
}

// lookupContext reads a context value under the lock SetContext takes, so
// handlers that work from several goroutines don't race.
func (r *Request) lookupContext(key string) (interface{}, bool) {
	r.mut.RLock()
	defer r.mut.RUnlock()

	value, exists := r.Context[key]
	return value, exists
}

func (r *Request) GetContext(key string) interface{} {
	r.mut.RLock()
	defer r.mut.RUnlock()
	if r.Context == nil {
		return nil
	}

	return r.Context[key]
}

// AddAuditChange records one data change made during this request, to be
// flushed as a single batch to the AuditTrail collection once the request ends.
func (r *Request) AddAuditChange(change AuditTrailChange) {
	r.mut.Lock()
	defer r.mut.Unlock()

	r.auditChanges = append(r.auditChanges, change)
}

// AuditChanges returns a copy of the data changes collected so far on this request.
func (r *Request) AuditChanges() []AuditTrailChange {
	r.mut.RLock()
	defer r.mut.RUnlock()

	changes := make([]AuditTrailChange, len(r.auditChanges))
	copy(changes, r.auditChanges)

	return changes
}

// Request methods
func (r *Request) SetContextObject(key string, value map[string]interface{}) {
	r.mut.Lock()
	defer r.mut.Unlock()
	if r.ContextObject == nil {
		r.ContextObject = make(map[string]map[string]interface{})
	}

	r.ContextObject[key] = value
}

func (r *Request) GetContextObject(key string) map[string]interface{} {
	r.mut.RLock()
	defer r.mut.RUnlock()
	if r.ContextObject == nil {
		return nil
	}

	return r.ContextObject[key]
}

func (r *Request) Param(name string) string {
	if r.Params == nil {
		return ""
	}

	return r.Params[name]
}

// New query parameter methods for Request
func (r *Request) Query(key string) string {
	return r.HttpRequest.URL.Query().Get(key)
}

func (r *Request) QueryInt(key string, defaultValue int) int {
	value := r.Query(key)
	if value == "" {
		return defaultValue
	}

	intValue, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}

	return intValue
}

func (r *Request) QueryFloat(key string, defaultValue float64) float64 {
	value := r.Query(key)
	if value == "" {
		return defaultValue
	}

	floatValue, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return defaultValue
	}
	return floatValue
}

func (r *Request) QueryBool(key string, defaultValue bool) bool {
	value := r.Query(key)
	if value == "" {
		return defaultValue
	}

	boolValue, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}
	return boolValue
}

func (r *Request) QueryArray(key string) []string {
	return r.HttpRequest.URL.Query()[key]
}

func (r *Request) QueryMap() map[string][]string {
	return r.HttpRequest.URL.Query()
}

func (r *Request) Next(req Request, res Response) error {
	if r.next != nil {
		r.next(req, res)
	}
	return nil
}

// Auth returns the signed-in user, decoded from the user info on the request
// context. The decode goes through JSON, so it's done once per request and
// each call gets its own copy.
func (r *Request) Auth() *AuthPayload {
	r.mut.Lock()
	defer r.mut.Unlock()

	data, exists := r.Context[string(UserInfoPayloadKey)]
	if !exists {
		return nil
	}

	if !r.authCached {
		var typeValue AuthPayload
		json.Unmarshal(helper.ToByte(data), &typeValue)

		r.auth = typeValue
		r.authCached = true
	}

	auth := r.auth
	return &auth
}

func (r *Request) Client() *ClientPayload {
	data, exists := r.lookupContext(string(ClientPayloadKey))
	if !exists {
		return nil
	}

	if m, ok := data.(ClientPayload); ok {
		return &m
	}

	return nil
}

func (r *Request) TokenPayload() *TokenPayload {
	data, exists := r.lookupContext(string(TokenPayloadKey))
	if !exists {
		return nil
	}

	if m, ok := data.(TokenPayload); ok {
		return &m
	}

	return nil
}

func (r *Request) TenantId() interface{} {
	data, exists := r.lookupContext(string(CurrentTenantId))
	if !exists {
		return nil
	}

	return data
}

func (r *Request) SetTenantId(tenantId interface{}) {
	r.SetContext(string(CurrentTenantId), tenantId)
}

func (r *Request) Tenant() config.TenantConfig {
	data, exists := r.lookupContext(string(CurrentTenantConfig))
	if !exists {
		return config.TenantConfig{}
	}

	if m, ok := data.(config.TenantConfig); ok {
		return m
	}

	return config.TenantConfig{}
}

func (r *Request) SetTenant(tenant config.TenantConfig) {
	r.SetContext(string(CurrentTenantConfig), tenant)
}

func (r *Request) Token() string {
	token := r.GetContext(string(AccessTokenKey))

	if helper.IsEmpty(token) {
		token = r.HttpRequest.Header.Get("Authorization")

		if helper.IsEmpty(token) {
			token = r.Query("token")
		}

		if helper.IsEmpty(token) {
			token = r.HttpRequest.Header.Get("X-Core-Session-Token")
		}

		if helper.IsNotEmpty(token) {
			// "Bearer <token>", or the bare token. Slicing off 7 characters
			// used to panic on shorter values and mangle bare tokens.
			value := helper.ToString(token)
			if bearer, ok := bearerToken(value); ok {
				return bearer
			}
			return strings.TrimSpace(value)
		}
	} else {
		return helper.ToString(token)
	}

	return ""
}

// bearerToken returns the token from an "Bearer <token>" Authorization value.
// The scheme is matched case-insensitively (RFC 6750); ok is false for any
// other value.
func bearerToken(value string) (token string, ok bool) {
	value = strings.TrimSpace(value)

	const prefix = "bearer "
	if len(value) < len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return "", false
	}

	return strings.TrimSpace(value[len(prefix):]), true
}

func (r *Request) Body() interface{} {
	return r.RawBody
}

func (r *Request) Header(key string) string {
	return r.HttpRequest.Header.Get(key)
}

func (r *Request) GetHeader(key string) string {
	return r.HttpRequest.Header.Get(key)
}

func (r *Request) SetHeader(key, value string) {
	r.HttpRequest.Header.Set(key, value)
}
