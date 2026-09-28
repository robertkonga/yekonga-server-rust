package yekonga

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	mainConfig "github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/gateway/payment"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/logger"
)

const COOKIE_ENABLED_KEY = "YEKONGA_ENABLED"

// StaticConfig holds configuration for static file serving
type StaticConfig struct {
	Directory   string   // Root directory for static files
	PathPrefix  string   // URL path prefix for static files (e.g., "/public")
	IndexFile   string   // Default index file (e.g., "index.html")
	Extensions  []string // Allowed file extensions
	CacheMaxAge int      // Cache max age in seconds
}

// Route represents a route with its pattern and parameters
type Route struct {
	pattern    string
	re         *regexp.Regexp // compiled once from pattern in addRoute
	paramNames []string
	handler    Handler
}

// Handler represents a function that handles an HTTP request
type Handler func(req *Request, res *Response)
type SystemHandler func(req *Request, res *Response) (interface{}, error)

// Middleware represents a function that can modify requests/responses
type Middleware func(req *Request, res *Response) (int, error)

type CloudFunction func(interface{}, *RequestContext) (interface{}, error)
type BackendCloudFunction func(interface{}) (interface{}, error)
type TriggerFunction func(*RequestContext, *QueryContext) (interface{}, error)
type TriggerAllFunction func(*DataModel, *RequestContext, *QueryContext) (interface{}, error)
type ActionCloudFunction func(*RequestContext, *QueryContext) (GraphqlActionResult, error)

var Server *YekongaData

// Yekonga represents the main server structure
type YekongaData struct {
	routes                 map[string][]Route // method -> routes
	functions              map[string]CloudFunction
	systemFunctions        map[string]SystemHandler
	primaryFunctions       map[PrimaryCloudKey]BackendCloudFunction
	authTriggerFunctions   map[TriggerAction]TriggerFunction
	triggerFunctions       map[string]map[TriggerAction]map[string]TriggerFunction
	triggerAllFunctions    map[TriggerAction]TriggerAllFunction
	graphqlActionFunctions map[string]map[string]map[string]ActionCloudFunction
	graphqlCustomQuery     []CustomGraphqlQuery
	middlewares            []Middleware
	initMiddlewares        []Middleware
	preloadMiddlewares     []Middleware
	catchMiddlewares       []Middleware
	whenReady              []func()
	models                 map[string]*DataModel
	resolverChartGroupData map[string]ResolverChartGroupData
	databaseStructure      *DatabaseStructure
	graphqlBuild           *GraphqlAutoBuild
	socketServer           *SocketServer
	dbConnect              *DatabaseConnections
	staticConfig           []*StaticConfig
	logger                 *log.Logger
	cronjob                *Cronjob
	mut                    sync.RWMutex
	publicRoutes           []string
	rateLimiter            *rateLimiterStore
	rateLimiterOnce        sync.Once
	errorGuard             *errorGuard
	errorGuardOnce         sync.Once
	ipWhitelist            *ipWhitelistStore
	ipWhitelistOnce        sync.Once
	paymentController      *payment.Controller
	paymentControllerErr   error
	paymentControllerOnce  sync.Once
	tenantPayments         map[string]*tenantPaymentEntry
	tenantPaymentsMu       sync.Mutex
	tenantCache            *lookupCache[tenantRecords]
	tokenPaths             *tokenPaths
	tokenPathsOnce         sync.Once
	tenantCatchCache       *lookupCache[*datatype.DataMap]
	userCache              *lookupCache[*datatype.DataMap]

	Config   *mainConfig.YekongaConfig
	RootPath string
	IsDev    bool
}

// NewYekonga creates a new instance of Yekonga server
func ServerConfig(config mainConfig.YekongaConfig, databaseStructure DatabaseStructure) *YekongaData {
	logger.LogoLarge()
	mainConfig.SetYekongaConfig(&config)
	databaseStructureCombined := NewDatabaseStructure(databaseStructure, &config)
	systemModels := NewSystemModels(&config, databaseStructureCombined)
	dbConnect := NewDatabaseConnections(&config)
	resolverChartGroupData := SetDataGroups(systemModels)
	exPath := "./"

	IsDev := os.Getenv("APP_ENV") == "development"
	ex, err := os.Executable()
	if err == nil {
		exPath = filepath.Dir(ex)
	}

	Server = &YekongaData{
		Config:   &config,
		RootPath: exPath,
		IsDev:    IsDev,

		dbConnect:              dbConnect,
		models:                 systemModels,
		resolverChartGroupData: resolverChartGroupData,
		databaseStructure:      databaseStructureCombined,
		routes:                 make(map[string][]Route),
		middlewares:            make([]Middleware, 0, 5),
		initMiddlewares:        make([]Middleware, 0, 5),
		preloadMiddlewares:     make([]Middleware, 0, 5),
		catchMiddlewares:       make([]Middleware, 0, 5),
		graphqlCustomQuery:     make([]CustomGraphqlQuery, 0),
		whenReady:              make([]func(), 0, 0),
		functions:              make(map[string]CloudFunction),
		systemFunctions:        make(map[string]SystemHandler),
		primaryFunctions:       make(map[PrimaryCloudKey]BackendCloudFunction),
		graphqlActionFunctions: make(map[string]map[string]map[string]ActionCloudFunction),
		triggerFunctions:       make(map[string]map[TriggerAction]map[string]TriggerFunction),
		authTriggerFunctions:   make(map[TriggerAction]TriggerFunction),
		publicRoutes:           make([]string, 0),
		logger:                 &log.Logger{},
	}

	Server.initLookupCaches()

	dbConnect.appPath = Server.HomeDirectory()
	SetSystemModelDBconnection(Server, &systemModels)

	graphqlBuild := NewGraphqlAutoBuild(Server, systemModels)
	Server.graphqlBuild = graphqlBuild

	dbConnect.connect()
	go Server.ensureIndexes()
	Server.ensureSQLSchema()
	Server.initialize()
	Server.cronjob = NewCronjob(Server)
	Server.setNotification()

	return Server
}

func ServerLoad(configFile string, databaseFile string) {
	config := mainConfig.NewYekongaConfig(configFile)
	databaseStructure := NewDatabaseStructureFile(databaseFile, config)

	ServerConfig(*config, *databaseStructure)
}

func ServerConfigJson(configFile string, databaseFile string) {
	ServerLoad(configFile, databaseFile)
}

func (y *YekongaData) Model(name string) *DataModel {
	if mod, ok := y.models[name]; ok {
		return mod
	} else {
		logger.Error("" + name + " is not available")
	}

	return nil
}

func (y *YekongaData) ModelQuery(name string) *DataModelQuery {
	model := y.Model(name)

	if model != nil {
		return model.Query()
	}

	return nil
}

// flushAuditTrail persists every data change collected on this request as a
// single batch, once the request has finished. It runs off the request's
// goroutine so it never delays the response.
func (y *YekongaData) flushAuditTrail(req *Request) {
	if !y.Config.AuditTrail.Enabled {
		return
	}

	changes := req.AuditChanges()
	if len(changes) == 0 {
		return
	}

	auth := req.Auth()
	client := req.Client()

	go func() {
		auditModel := y.ModelQuery("AuditTrail")
		if auditModel == nil {
			return
		}

		for _, change := range changes {
			data := datatype.DataMap{
				"action":     change.Action,
				"collection": change.Collection,
				"model":      change.Model,
				"documentId": change.DocumentId,
				"oldValues":  change.OldValues,
				"newValues":  change.NewValues,
			}

			if auth != nil {
				data["tenantId"] = auth.TenantID
				data["profileId"] = auth.ProfileID
				data["userId"] = auth.UserId
			}

			if client != nil {
				data["ipAddress"] = client.IpAddress
				data["userAgent"] = client.UserAgent
				data["browser"] = client.UserAgent
			}

			auditModel.SkipBeforeCommit().Create(data)
		}
	}()
}

func (y *YekongaData) HomeDirectory() string {
	return helper.HomeDirectory(helper.ToSlug(y.Config.AppName))
}

// parseRoute parses a route pattern and extracts parameter names.
// e.g. /user-:name-:action/:id.svg returns (["name", "action", "id"], "^/user-([^/-]+)-([^/.]+)/([^/]+)$")
func parseRoute(pattern string) ([]string, string) {
	var params []string
	var patternBuf strings.Builder

	patternBuf.WriteString("^")

	parts := strings.Split(pattern, "/")
	for i, part := range parts {
		if !strings.Contains(part, ":") {
			if i > 0 {
				patternBuf.WriteString("/")
			}
			patternBuf.WriteString(regexp.QuoteMeta(part))
			continue
		}

		segment, err := parseSegment(part)
		if err != nil {
			if i > 0 {
				patternBuf.WriteString("/")
			}
			// patternBuf.WriteString("([^/]+)")
			patternBuf.WriteString("([a-zA-Z0-9_]+)")
			params = append(params, part)
			continue
		}

		params = append(params, segment.params...)

		if segment.fullyOptional && i > 0 {
			// Wrap the leading slash + segment together as optional
			patternBuf.WriteString("(?:/" + segment.pattern + ")?")
		} else {
			if i > 0 {
				patternBuf.WriteString("/")
			}
			patternBuf.WriteString(segment.pattern)
		}
	}

	patternBuf.WriteString("$")

	return params, patternBuf.String()
}

type parsedSegment struct {
	params        []string
	pattern       string
	fullyOptional bool // true when the whole segment is "/:param?" and the slash should be optional too
}

// parseSegment handles a single path segment that contains one or more params.
// e.g. "user-:name-:action" → params: ["name","action"], pattern: "user-([^/-]+)-([^/]+)"
var (
	paramRe      = regexp.MustCompile(`:([a-zA-Z0-9_]+)(\?)?`)
	validParamRe = regexp.MustCompile(`^[a-zA-Z0-9]+$`)
)

func parseSegment(part string) (parsedSegment, error) {
	var params []string
	var patternBuf strings.Builder

	remaining := part
	for {
		loc := paramRe.FindStringIndex(remaining)
		if loc == nil {
			patternBuf.WriteString(regexp.QuoteMeta(remaining))
			break
		}

		leadingLiteral := remaining[:loc[0]]
		match := paramRe.FindStringSubmatch(remaining[loc[0]:])
		if len(match) < 2 {
			return parsedSegment{}, fmt.Errorf("invalid param in segment: %q", part)
		}

		paramName := match[1]
		optional := match[2] == "?"
		params = append(params, paramName)
		remaining = remaining[loc[1]:]

		nextParamLoc := paramRe.FindStringIndex(remaining)
		boundary := remaining
		if nextParamLoc != nil {
			boundary = remaining[:nextParamLoc[0]]
		}

		var captureGroup string
		if boundary == "" {
			// captureGroup = "([^/]+)"
			captureGroup = "([a-zA-Z0-9_-]+)"
		} else {
			// captureGroup = "([^/" + regexp.QuoteMeta(boundary) + "]+)"
			captureGroup = "([a-zA-Z0-9_-]+)"
		}

		if optional {
			patternBuf.WriteString("(?:")
			patternBuf.WriteString(regexp.QuoteMeta(leadingLiteral))
			patternBuf.WriteString(captureGroup)
			patternBuf.WriteString(")?")
		} else {
			patternBuf.WriteString(regexp.QuoteMeta(leadingLiteral))
			patternBuf.WriteString(captureGroup)
		}
	}

	pattern := patternBuf.String()

	// The segment is fully optional if it's just a single "?" param with no leading/trailing literals.
	// e.g. ":name?" but not "user-:name?" or ":name?.svg"
	fullyOptional := len(params) == 1 &&
		paramRe.MatchString(part) &&
		strings.TrimSuffix(strings.TrimPrefix(part, ":"+params[0]), "?") == ""

	return parsedSegment{params: params, pattern: pattern, fullyOptional: fullyOptional}, nil
}

func isValidParam(param string) bool {
	return validParamRe.MatchString(param)
}

// matchRoute checks if a path matches a compiled route regex and extracts parameters.
func matchRoute(re *regexp.Regexp, paramNames []string, path string) (bool, map[string]string) {
	matches := re.FindStringSubmatch(path)

	// if strings.Contains(path, "/nome") {
	// 	console.Error("path", path)
	// 	console.Error("pattern", pattern)
	// 	console.Error("matches", matches)
	// }

	if matches == nil {
		return false, nil
	}

	// matches[0] is the full match, captures start at [1]
	if len(matches)-1 != len(paramNames) {
		return false, nil
	}

	params := make(map[string]string, len(paramNames))
	for i, name := range paramNames {
		params[name] = matches[i+1]
	}

	return true, params
}

// Yekonga methods
func (y *YekongaData) addRoute(method, pattern string, handler Handler) {
	y.mut.Lock()
	defer y.mut.Unlock()

	if y.routes[method] == nil {
		y.routes[method] = []Route{}
	}

	pattern = y.AppendBaseUrl(pattern)
	paramNames, normalized := parseRoute(pattern)

	// if strings.Contains(pattern, "/nome") {
	// 	console.Info("pattern", pattern)
	// 	console.Info("normalized", normalized)
	// 	console.Info("paramNames", paramNames)
	// }

	y.routes[method] = append(y.routes[method], Route{
		pattern:    normalized,
		re:         regexp.MustCompile(normalized),
		paramNames: paramNames,
		handler:    handler,
	})
}

func (y *YekongaData) AppendBaseUrl(pattern string) string {
	if helper.IsNotEmpty(y.Config.BaseUrl) {
		baseUrl := y.Config.BaseUrl

		if baseUrl != "/" {
			baseUrl = strings.TrimSuffix(baseUrl, "/")
			pattern = strings.TrimPrefix(pattern, "/")

			pattern = baseUrl + "/" + pattern
		}
	}

	pattern = strings.TrimSuffix(pattern, "/")
	return "/" + strings.TrimPrefix(pattern, "/")
}

func (y *YekongaData) findRoute(method, path string) (*Route, map[string]string) {
	y.mut.RLock()
	routes := y.routes[method]
	y.mut.RUnlock()

	for i := range routes {
		if matches, params := matchRoute(routes[i].re, routes[i].paramNames, path); matches {
			return &routes[i], params
		}
	}

	return nil, nil
}

func (y *YekongaData) Middleware(middleware Middleware, middlewareType MiddlewareType) {
	switch middlewareType {
	case GlobalMiddleware:
		y.middlewares = append(y.middlewares, middleware)
	case InitMiddleware:
		y.initMiddlewares = append(y.initMiddlewares, middleware)
	case PreloadMiddleware:
		y.preloadMiddlewares = append(y.preloadMiddlewares, middleware)
	case CatchMiddleware:
		y.catchMiddlewares = append(y.catchMiddlewares, middleware)
	default:
		y.logger.Printf("Unknown middlewares type: %s, select between global, init, or preload", middlewareType)
	}
}

func (y *YekongaData) PreloadMiddlewares(middleware Middleware) {
	y.preloadMiddlewares = append(y.preloadMiddlewares, middleware)
}

func (y *YekongaData) GlobalMiddleware(middleware Middleware) {
	y.middlewares = append(y.middlewares, middleware)
}

func (y *YekongaData) InitMiddleware(middleware Middleware) {
	y.initMiddlewares = append(y.initMiddlewares, middleware)
}

func (y *YekongaData) CatchMiddleware(middleware Middleware) {
	y.catchMiddlewares = append(y.catchMiddlewares, middleware)
}

func (y *YekongaData) Catch(middleware Middleware) {
	y.catchMiddlewares = append(y.catchMiddlewares, middleware)
}

func (y *YekongaData) Get(path string, handler Handler) {
	y.addRoute(http.MethodGet, path, handler)
}

func (y *YekongaData) Post(path string, handler Handler) {
	y.addRoute(http.MethodPost, path, handler)
}

func (y *YekongaData) Put(path string, handler Handler) {
	y.addRoute(http.MethodPut, path, handler)
}

func (y *YekongaData) Patch(path string, handler Handler) {
	y.addRoute(http.MethodPatch, path, handler)
}

func (y *YekongaData) Options(path string, handler Handler) {
	y.addRoute(http.MethodOptions, path, handler)
}

func (y *YekongaData) Delete(path string, handler Handler) {
	y.addRoute(http.MethodDelete, path, handler)
}

func (y *YekongaData) All(path string, handler Handler) {
	methods := []string{
		http.MethodGet,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodOptions,
		http.MethodDelete,
	}
	for _, method := range methods {
		y.addRoute(method, path, handler)
	}
}

// Static configures static file serving
func (y *YekongaData) Static(config StaticConfig) error {
	// Set default values if not provided
	if config.IndexFile == "" {
		config.IndexFile = "index.html"
	}
	if config.Extensions == nil {
		config.Extensions = DefaultExtensions[:]
	}
	if config.CacheMaxAge == 0 {
		config.CacheMaxAge = 2592000 // 24 hours
	}

	// Verify directory exists
	if _, err := os.Stat(config.Directory); os.IsNotExist(err) {
		return err
	}

	y.mut.Lock()
	defer y.mut.Unlock()
	y.staticConfig = append(y.staticConfig, &config)
	return nil
}

// staticConfigs returns the registered static directories. Static only ever
// appends, so the returned slice is safe to read while more are added.
func (y *YekongaData) staticConfigs() []*StaticConfig {
	y.mut.RLock()
	defer y.mut.RUnlock()

	return y.staticConfig
}

// isStaticPath checks if the path is for static file serving
func (y *YekongaData) isStaticPath(path string) bool {
	ext := filepath.Ext(path)

	for _, static := range y.staticConfigs() {
		if static != nil && helper.Contains(static.Extensions, ext) {
			return true
		}
	}

	return false
}

// handleStaticFile serves static files
func (y *YekongaData) handleStaticFile(w http.ResponseWriter, r *http.Request) bool {
	for _, static := range y.staticConfigs() {
		if static != nil {
			// Remove path prefix to get relative file path
			urlPath := strings.TrimPrefix(r.URL.Path, static.PathPrefix)
			urlPath = strings.TrimPrefix(urlPath, "/")
			// console.Info("Static file request:", urlPath)

			// Construct full file path
			filePath := filepath.Join(static.Directory, urlPath)
			// console.Info("Checking file path:", filePath)

			// Check if path is a directory
			fileInfo, err := os.Stat(filePath)
			if err == nil && fileInfo.IsDir() {
				filePath = filepath.Join(filePath, static.IndexFile)
			}

			// Verify file exists and extension is allowed
			if fileInfo, err := os.Stat(filePath); err == nil && !fileInfo.IsDir() {
				ext := strings.ToLower(path.Ext(filePath))
				for _, allowedExt := range static.Extensions {
					if ext == allowedExt {
						// Set cache headers; uploaded files are cached for 1 year
						cacheMaxAge := static.CacheMaxAge
						slashPath := "/" + strings.TrimPrefix(filepath.ToSlash(filePath), "/")
						if strings.Contains(slashPath, "/public/uploads/") {
							cacheMaxAge = 31536000
						}
						w.Header().Set("Cache-Control", fmt.Sprintf("max-age=%d", cacheMaxAge))

						// Serve the file
						http.ServeFile(w, r, filePath)
						return true
					}
				}
			}
		}
	}

	return false
}

func (y *YekongaData) RegisterCronjob(name string, frequency time.Duration, callback func(app *YekongaData, time time.Time)) {
	y.cronjob.registerJob(name, frequency, callback)
}

func (y *YekongaData) RegisterCronjobAt(name string, frequency JobFrequency, time time.Time, callback func(app *YekongaData, time time.Time)) {
	y.cronjob.registerJobAt(name, frequency, time, callback)
}

func (y *YekongaData) RegisterCronjobOn(name string, frequency JobFrequency, time time.Time, callback func(app *YekongaData, time time.Time)) {
	y.cronjob.registerJobAt(name, frequency, time, callback)
}

func (y *YekongaData) SetPublicRoute(route string) {
	y.publicRoutes = append(y.publicRoutes, route)
}

// ServeHTTP implements the http.Handler interface
func (y *YekongaData) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// logger.Info("ServeHTTP", "All routes pass")
	var err error
	var status int
	var rawBody interface{}

	// Block sources that have tripped the error guard (too many 400/401/403/404
	// responses per second, i.e. scanning, brute-forcing, or enumeration)
	// before doing anything else at all, including static file lookups.
	if !y.checkErrorGuardBlock(w, r) {
		return
	}

	// Check for static file requests first
	// console.Info("Static file request:", r.URL.Path)
	if y.isStaticPath(r.URL.Path) {
		if y.handleStaticFile(w, r) {
			return
		}
	}

	// Reject over-budget clients before doing any request parsing, so an
	// abusive client can't burn CPU/memory on multipart or JSON decoding.
	if !y.checkRateLimit(w, r) {
		return
	}

	origin := "*"
	originList := r.Header["Origin"]

	if len(originList) > 0 {
		origin = originList[0]
	} else {
		originList = r.Header["Referer"]

		if len(originList) > 0 {
			origin = originList[0]
		}

		if helper.IsEmpty(origin) {
			origin = r.Host
		}
	}

	// to account for form headers and boundaries.
	maxUploadSize := y.Config.Security.MaxBodyBytes
	if maxUploadSize <= 0 {
		maxUploadSize = int64(310 << 20) // ~310 MB
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	// Payment gateways sign the raw body, so keep a copy of it for the
	// webhook handler before the form/JSON parsing below consumes it.
	var paymentWebhookBody []byte
	isPaymentWebhook := y.isPaymentWebhookPath(r.URL.Path)
	if isPaymentWebhook {
		paymentWebhookBody, _ = io.ReadAll(io.LimitReader(r.Body, paymentWebhookMaxBodyBytes))
		r.Body = io.NopCloser(bytes.NewReader(paymentWebhookBody))
	}

	// 1. Parse the multipart form (max 32MB in memory)
	r.ParseMultipartForm(32 << 20)

	cookieValue := y.Config.ConnectionID
	cookie, err := r.Cookie(COOKIE_ENABLED_KEY)
	if helper.IsEmpty(cookieValue) {
		cookieValue = "YEKONGA_CONNECTED"
	}

	if err != nil || helper.IsEmpty(cookie.Value) {
		http.SetCookie(w, &http.Cookie{
			Name:     COOKIE_ENABLED_KEY,
			Value:    cookieValue,
			Domain:   "",
			HttpOnly: true,
			Secure:   y.Config.SecureOnly,
			SameSite: http.SameSiteDefaultMode,
			MaxAge:   30 * 24 * 60 * 60, // 30 days
		})
	}

	json.NewDecoder(r.Body).Decode(&rawBody)

	if isPaymentWebhook {
		r.Body = io.NopCloser(bytes.NewReader(paymentWebhookBody))
	}

	route, params := y.findRoute(r.Method, r.URL.Path)

	req := Request{
		HttpRequest: r,
		RawBody:     rawBody,
		Context:     datatype.Context{},
		Params:      params,
		App:         y,
	}

	stopWatchingDisconnect := req.watchDisconnect()
	defer stopWatchingDisconnect()

	res := Response{
		httpResponseWriter: &w,
		staticConfig:       y.staticConfigs(),
		request:            &req,
		headers:            make(MIMEHeader, 0),
		statusCode:         http.StatusOK,
	}

	if y.Config.Cors {
		w.Header().Set("access-control-allow-origin", origin)
	}

	w.Header().Set("access-control-allow-headers", "content-type, authorization, x-requested-with, x-csrf-token, timezone, upgrade-insecure-requests")
	w.Header().Set("access-control-allow-credentials", "true")
	w.Header().Set("access-control-allow-methods", "GET, POST, OPTIONS, PUT, PATCH, DELETE")
	w.Header().Set("keep-alive", "timeout=5, max=98")
	w.Header().Set("connection", "keep-alive")

	defer res.Close()
	defer y.flushAuditTrail(&req)

	// Apply middlewares
	status, err = MasterKeyMiddleware(&req, &res)
	if err != nil {
		res.Abort(status, err.Error())
		return
	}

	// Apply middlewares
	status, err = ApplicationIDMiddleware(&req, &res)
	if err != nil {
		res.Abort(status, err.Error())
		return
	}

	// Apply middlewares
	for _, middleware := range y.preloadMiddlewares {
		if middleware != nil {
			status, err = middleware(&req, &res)
			if err != nil {
				res.Abort(status, err.Error())
				return
			}
		}
	}

	// Apply client middleware
	status, err = ClientMiddleware(&req, &res)
	if err != nil {
		res.Abort(status, err.Error())
		return
	}

	// Apply client middleware
	status, err = TenantCatchMiddleware(&req, &res)
	if err != nil {
		res.Abort(status, err.Error())
		return
	}

	// Apply token middleware
	status, err = TokenMiddleware(&req, &res)
	if err != nil {
		res.Abort(status, err.Error())
		return
	}

	// Apply billing middleware
	status, err = BillingMiddleware(&req, &res)
	if err != nil {
		res.Abort(status, err.Error())
		return
	}

	// Apply userinfo middlewares
	status, err = UserInfoMiddleware(&req, &res)
	if err != nil {
		res.Abort(status, err.Error())
		return
	}

	// Apply middlewares
	for _, middleware := range y.initMiddlewares {
		if middleware != nil {
			status, err = middleware(&req, &res)
			if err != nil {
				res.Abort(status, err.Error())
				return
			}
		}
	}

	// Apply middlewares
	for _, middleware := range y.middlewares {
		if middleware != nil {
			status, err = middleware(&req, &res)
			if err != nil {
				res.Abort(status, err.Error())
				return
			}
		}
	}

	if route == nil {
		var htmlPage []byte
		var err error
		var textPage []byte

		// No route matched: the catch middlewares run in order until one
		// responds. An error aborts, as in the other chains. If none
		// responds, the default page below is served. (Only the first used
		// to run, and an empty 200 was sent when it didn't respond.)
		for _, middleware := range y.catchMiddlewares {
			if middleware == nil {
				continue
			}

			status, err := middleware(&req, &res)
			if err != nil {
				res.Abort(status, err.Error())
				return
			}

			if res.wroteHeader {
				return
			}
		}

		if r.URL.Path == "" || r.URL.Path == "/" || r.URL.Path == "/index.html" {
			textPage = []byte("Welcome to Yekonga Server")
			htmlPage, err = StaticFS.ReadFile("static/index.html")
		} else {
			y.recordErrorResponse(&req, http.StatusNotFound)
			w.WriteHeader(http.StatusNotFound)
			textPage = []byte("404 Page Not Found")
			htmlPage, err = StaticFS.ReadFile("static/404.html")
		}

		if err != nil {
			// If the file is missing or there's an error, send a default message
			w.Write(textPage)
			return
		}

		w.Write(htmlPage) // Send the custom 404 page content
		return
	}

	// Execute route handler
	route.handler(&req, &res)
}

// Redirect HTTP to HTTPS
func redirectToHTTPS(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "https://"+r.Host+r.RequestURI, http.StatusMovedPermanently)
}

func (y *YekongaData) Stop() {
	if helper.IsNotEmpty(y.socketServer) {
		y.socketServer.Close()
	}
}

func (y *YekongaData) Start(address interface{}) {
	port := y.Config.Ports.Server

	if y.Config.Ports.Secure {
		port = y.Config.Ports.SSLServer
	}

	if helper.IsNotEmpty(address) {
		port = helper.ToInt(address)
	}

	Server.graphqlBuild.initialize()
	runDefaultCloudFunctions()

	y.Config.Ports.Server = port

	serverPort := fmt.Sprint(":", port)
	y.socketServer = NewSocketServer(y)

	defer y.socketServer.Close()

	logRunningServer(serverPort, y.Config.Ports.Secure)
	y.runWhenReady()

	httpServer := y.newHTTPServer(serverPort)

	if y.Config.Ports.Secure {
		go func() {
			httpMux := http.NewServeMux()
			httpMux.HandleFunc("/", redirectToHTTPS)

			redirectServer := y.newHTTPServer(":" + fmt.Sprint(y.Config.Ports.Server))
			redirectServer.Handler = httpMux

			err := redirectServer.ListenAndServe()
			if err != nil {
				fmt.Println("HTTP server error:", err)
			}
		}()

		if err := httpServer.ListenAndServeTLS("certificate/cert.pem", "certificate/key.pem"); err != nil {
			logger.Error("Error starting https server", err)
		}
	} else {
		if err := httpServer.ListenAndServe(); err != nil {
			logger.Error("Error starting server", err)
		}
	}
}

// newHTTPServer builds an *http.Server with explicit timeouts and header size
// limits so a slow or abusive client can't hold a connection open indefinitely
// (Slowloris-style resource exhaustion). Values come from config.Security.
//
// ReadHeaderTimeout and IdleTimeout default to safe non-zero values because
// no legitimate client has a reason to trickle in headers or sit idle between
// requests. ReadTimeout and WriteTimeout default to 0 (no limit, matching the
// server's previous behavior) because this framework allows request bodies up
// to ~310MB (see config.Security.MaxBodyBytes) and can stream large exports
// (CSV/Excel/PDF) back to the client — a blanket deadline there would cut off
// legitimate slow-network uploads/downloads along with abusive ones. Set them
// explicitly in config.json once you know the real traffic profile, or handle
// slow-read/slow-write protection at a reverse proxy in front of this server.
func (y *YekongaData) newHTTPServer(addr string) *http.Server {
	sec := y.Config.Security

	var readTimeout, writeTimeout time.Duration
	if sec.ReadTimeoutSeconds > 0 {
		readTimeout = time.Duration(sec.ReadTimeoutSeconds) * time.Second
	}
	if sec.WriteTimeoutSeconds > 0 {
		writeTimeout = time.Duration(sec.WriteTimeoutSeconds) * time.Second
	}

	readHeaderTimeout := 5 * time.Second
	if sec.ReadHeaderTimeoutSeconds > 0 {
		readHeaderTimeout = time.Duration(sec.ReadHeaderTimeoutSeconds) * time.Second
	}

	idleTimeout := 60 * time.Second
	if sec.IdleTimeoutSeconds > 0 {
		idleTimeout = time.Duration(sec.IdleTimeoutSeconds) * time.Second
	}

	maxHeaderBytes := sec.MaxHeaderBytes
	if maxHeaderBytes <= 0 {
		maxHeaderBytes = http.DefaultMaxHeaderBytes // 1 MB
	}

	return &http.Server{
		Addr:              addr,
		Handler:           y,
		ReadTimeout:       readTimeout,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}

func logRunningServer(port string, secure bool) {
	ips, err := helper.GetLocalIPS()

	if err != nil {
		ips = append(ips, "127.0.0.1")
	}

	for _, ip := range ips {
		serverAddress := fmt.Sprint(ip + port)
		if secure {
			logger.Success("HTTPS Server is running on", serverAddress)
		} else {
			logger.Success("Server is running on", serverAddress)
		}
	}
}
