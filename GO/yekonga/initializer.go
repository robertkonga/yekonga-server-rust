package yekonga

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/jwt"
	"github.com/robertkonga/yekonga-server-go/helper/logger"
	"github.com/robertkonga/yekonga-server-go/plugins/graphql/gqlerrors"
)

// Allowed file extensions
var DefaultExtensions = [...]string{
	// Web Files
	".html", ".css", ".js", ".webmanifest",
	".htm", ".json", ".xml", ".map",

	// Image Files
	".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico",
	".bmp", ".webp", ".tiff", ".tif", ".avif",

	// Font Files
	".ttf", ".otf", ".woff", ".woff2", ".eot",

	// documents
	".xlsx", ".pdf", ".doc", ".text", ".txt", ".csv",
	".docx", ".odt", ".rtf", ".md", ".xls", ".ods",

	// Video Files
	".mp4", ".webm", ".ogg", ".avi", ".mov",
	".wmv", ".flv", ".mkv",

	// Audio Files
	".mp3", ".wav", ".ogg", ".aac", ".flac",
	".opus", ".m4a",

	// Archive Files
	".zip", ".rar", ".tar.gz", ".7z",
}

func (y *YekongaData) initialize() {
	logger.Info("App Name", y.Config.AppName)
	dir := y.HomeDirectory()
	logger.Info("Data Directory", dir)

	y.All("/health", func(req *Request, res *Response) {
		res.Text("Ok!")
	})

	y.All("/api-health", func(req *Request, res *Response) {
		res.Json(map[string]string{"status": "OK!"})
	})

	y.All("/check-connection", func(req *Request, res *Response) {
		id := y.Config.ConnectionID
		if helper.IsEmpty(id) {
			id = "YEKONGA_CONNECTED"
		}

		res.Text(id)
	})

	if y.Config.IsAuthorizationServer {
		y.Get("/me/:moduleName?", y.authHandler)
		y.Post("/me/:moduleName?", y.authHandler)

		y.Get("/logout/:moduleName?", y.logoutHandler)
		y.Post("/logout/:moduleName?", y.logoutHandler)

		y.Get("/refresh/:moduleName?", y.refreshHandler)
		y.Post("/refresh/:moduleName?", y.refreshHandler)
	}

	y.initializerSocketRoutes()
	y.initializePaymentRoutes()

	for _, public := range y.Config.Public {
		if strings.HasPrefix(public, "/") {
			public = "./" + public[1:]
		}

		if !strings.HasPrefix(public, "./") {
			public = "./" + public[1:]
		}

		altPath := filepath.Join(y.RootPath, public)
		if helper.FileExists(altPath) {
			public = altPath
		}

		if !helper.FileExists(public) {
			public = helper.GetPath(public)
		}

		if helper.FileExists(public) {
			public = helper.GetPath(public)
			// Configure static file serving

			err := y.Static(StaticConfig{
				Directory:   public,               // Serve files from ./public directory
				PathPrefix:  y.AppendBaseUrl("/"), // Access files at /public URL path
				IndexFile:   "index.html",         // Default index file
				Extensions:  DefaultExtensions[:],
				CacheMaxAge: 2592000, // Cache for 30 days hours
			})

			if err != nil {
				logger.Error("Failed to configure static file serving", err)
			} else {
				// logger.Info("Static file serving configured for directory", public)
				logger.Info("Access static files at", y.AppendBaseUrl("/"))
			}
		} else {
			logger.Error("Failed", "Public directory not exist", public)
		}
	}

	if y.Config.IsAuthorizationServer {
		y.All(y.Config.Graphql.ApiAuthRoute, func(req *Request, res *Response) {
			requestQuery := req.Query("query")
			requestBody := req.Body()
			variableValues := map[string]interface{}{}
			operationName := ""

			graphqlContext := RequestContext{
				Auth:         req.Auth(),
				App:          y,
				Request:      req,
				Response:     res,
				TokenPayload: req.TokenPayload(),
				Client:       req.Client(),
			}

			if body, oki := requestBody.(map[string]interface{}); oki {
				if data, ok := body["query"]; ok {
					if str, ok := data.(string); ok {
						requestQuery = str
					}
				}
				if data, ok := body["operationName"]; ok {
					if str, ok := data.(string); ok {
						operationName = str
					}
				}
				if data, ok := body["variables"]; ok {
					if str, ok := data.(map[string]interface{}); ok {
						variableValues = str
					}
				}
			}

			if !y.Config.AuthPlaygroundEnable {
				if isIntrospectionQuery(requestQuery) {
					res.Json(datatype.DataMap{
						"errors": []map[string]string{
							{"message": "Introspection is disabled"},
						},
					})

					return
				}
			}

			result := executeGraphql(graphqlExecution{
				Schema:         &y.graphqlBuild.AuthSchema,
				Cache:          y.graphqlBuild.authDocumentCache,
				RequestString:  requestQuery,
				OperationName:  operationName,
				VariableValues: variableValues,
				Context:        &graphqlContext,
				Parent:         req.HttpRequest.Context(),
			})

			if len(result.Errors) > 0 {
				result.Errors = formatErrors(result.Errors)
			}

			res.Json(result)
		})
	}

	y.All(y.Config.Graphql.ApiRoute, func(req *Request, res *Response) {
		requestQuery := req.Query("query")
		requestBody := req.Body()
		variableValues := map[string]interface{}{}
		operationName := ""

		graphqlContext := RequestContext{
			Auth:         req.Auth(),
			App:          y,
			Request:      req,
			Response:     res,
			TokenPayload: req.TokenPayload(),
			Client:       req.Client(),
		}

		if body, oki := requestBody.(map[string]interface{}); oki {
			if data, ok := body["query"]; ok {
				if str, ok := data.(string); ok {
					requestQuery = str
				}
			}
			if data, ok := body["operationName"]; ok {
				if str, ok := data.(string); ok {
					operationName = str
				}
			}
			if data, ok := body["variables"]; ok {
				if str, ok := data.(map[string]interface{}); ok {
					variableValues = str
				}
			}
		}

		if !y.Config.ApiPlaygroundEnable {
			if isIntrospectionQuery(requestQuery) {
				res.Json(datatype.DataMap{
					"errors": []map[string]string{
						{"message": "Introspection is disabled"},
					},
				})

				return
			}
		}

		// start := time.Now()
		result := executeGraphql(graphqlExecution{
			Schema:         &y.graphqlBuild.Schema,
			Cache:          y.graphqlBuild.documentCache,
			RequestString:  requestQuery,
			OperationName:  operationName,
			VariableValues: variableValues,
			RootObject:     make(map[string]interface{}),
			Context:        &graphqlContext,
			Parent:         req.HttpRequest.Context(),
			WithSelectors:  true,
		})

		if len(result.Errors) > 0 {
			result.Errors = formatErrors(result.Errors)
		}

		// helper.TrackTime(&start, "Graphql query execute")
		res.Json(result)
		// helper.TrackTime(&start, "Json encode")
	})

	y.initializeRestApi()
	y.initializerOtherRoutes()

}

func (y *YekongaData) authHandler(req *Request, res *Response) {
	var user *datatype.DataMap
	auth := req.Auth()
	moduleName := req.Param("moduleName")

	if helper.IsNotEmpty(auth) {
		requestContext := &RequestContext{
			App:      y,
			Auth:     req.Auth(),
			Client:   req.Client(),
			Request:  req,
			Response: res,
		}

		user = y.GetLoginData(requestContext, &LoginData{
			UserID:     auth.ID,
			ProfileID:  auth.ProfileID,
			ModuleName: moduleName,
		})

		if helper.IsNotEmpty(user) {
			(*user)["token"] = nil
		}

		res.Json(user)
		return
	}

	res.Status(http.StatusUnauthorized)
	res.Json(datatype.DataMap{
		"error": "Missing or Invalid token",
	})
}

func (y *YekongaData) logoutHandler(req *Request, res *Response) {
	client := req.Client()
	moduleName := req.Param("moduleName")
	isJson := (strings.Contains(res.request.GetHeader("content-type"), "json"))

	if !isJson {
		isJson = (strings.Contains(res.request.GetHeader("accept"), "json"))
	}

	requestContext := &RequestContext{
		App:      y,
		Auth:     req.Auth(),
		Client:   client,
		Request:  req,
		Response: res,
	}
	y.clearAuthCookies(requestContext, client.OriginDomain(), moduleName)

	if !isJson {
		res.Status(http.StatusTemporaryRedirect)
		res.Redirect(helper.GetBaseUrl("/", client.OriginDomain()))
		return
	}

	res.Status(http.StatusOK)
	res.Json(datatype.DataMap{
		"status": "SUCCESS",
	})
}

func (y *YekongaData) refreshHandler(req *Request, res *Response) {
	client := req.Client()
	moduleName := req.Param("moduleName")
	isJson := (strings.Contains(res.request.GetHeader("content-type"), "json"))
	if !isJson {
		isJson = (strings.Contains(res.request.GetHeader("accept"), "json"))
	}
	result, status := y.refreshTokenProcess(req, res, nil, moduleName)

	if !y.Config.SecureAuthentication {
		result["token"] = nil
	}

	if !isJson {
		res.Status(http.StatusTemporaryRedirect)
		res.Redirect(client.Origin)
		return
	}

	res.Status(status)
	res.Json(result)
}

func (y *YekongaData) refreshTokenProcess(req *Request, res *Response, refreshToken interface{}, moduleName string) (datatype.DataMap, int) {
	var data *datatype.DataMap
	status := http.StatusUnauthorized
	result := datatype.DataMap{}

	cookieEnabled := ""
	cookie, err := req.HttpRequest.Cookie(COOKIE_ENABLED_KEY)
	if err == nil {
		cookieEnabled = cookie.Value
	}

	if helper.IsEmpty(refreshToken) {
		refreshToken = req.GetContext(string(RefreshTokenKey))

		if helper.IsEmpty(refreshToken) {
			refreshToken = req.HttpRequest.Header.Get("X-Refresh-Token")

			if helper.IsEmpty(refreshToken) {
				refreshToken = req.Query("refresh_token")
			}
		}
	}

	if helper.IsNotEmpty(refreshToken) {
		hashedToken := helper.HashRefreshToken(helper.ToString(refreshToken))
		data = y.ModelQuery("RefreshToken").SkipBeforeCommit().Where("tokenHash", hashedToken).FindOne(nil)

		if helper.IsNotEmpty(data) {
			revoked := helper.GetValueOfBoolean(data, "revoked")

			if revoked {
				result["error"] = "refresh_token is revoked"
			} else {
				expiresAt := helper.GetValueOfDate(data, "expiresAt")
				today := helper.GetTimestamp(nil)

				if expiresAt.After(today) {
					domain := req.Client().OriginDomain()
					tokenDomain := helper.GetValueOfString(data, "domain")
					userId := helper.GetValueOfString(data, "userId")
					tenantId := helper.GetValueOfString(data, "tenantId")
					profileId := helper.GetValueOfString(data, "profileId")
					adminId := helper.GetValueOfString(data, "adminId")
					permissions := y.GetUserPermission(tenantId, userId, moduleName)

					if tokenDomain == domain {
						accessTokenExpireTime := y.Config.AccessTokenExpireTime
						if accessTokenExpireTime <= 0 {
							accessTokenExpireTime = 15 // default 15 minutes
						}

						payload := TokenPayload{
							Domain:       domain,
							TenantId:     tenantId,
							ProfileId:    profileId,
							UserId:       userId,
							AdminId:      adminId,
							Username:     helper.GetValueOfString(data, "username"),
							UsernameType: helper.GetValueOfString(data, "usernameType"),
							Phone:        helper.GetValueOfString(data, "phone"),
							Email:        helper.GetValueOfString(data, "email"),
							Whatsapp:     helper.GetValueOfString(data, "whatsapp"),
							ModuleName:   moduleName,
							Roles:        make([]string, 0),
							Permissions:  permissions,
							ExpiresAt:    today.Add(time.Minute * accessTokenExpireTime),
						}

						newAccessToken, _ := jwt.EncodeJWT(payload.ToMap(), y.Config.Authentication.SecretToken)
						newRefreshToken := y.getRefreshToken(*req.Client(), payload, false)

						status = http.StatusOK

						if helper.IsEmpty(cookieEnabled) {
							result[helper.ToVariable(string(AccessTokenKey))] = newAccessToken
							result[helper.ToVariable(string(RefreshTokenKey))] = newRefreshToken
						} else {
							result[helper.ToVariable(string(AccessTokenKey))] = "Cookie is set"
							result[helper.ToVariable(string(RefreshTokenKey))] = "Cookie is set"
						}

						y.setAuthCookies(&RequestContext{
							App:      y,
							Request:  req,
							Response: res,
							Client:   req.Client(),
						}, newAccessToken, newRefreshToken, moduleName)

						y.ModelQuery("RefreshToken").SkipBeforeCommit().Where("tokenHash", hashedToken).Update(datatype.DataMap{
							"revoked": true,
						}, nil)
					} else {
						result["error"] = "Domain mismatch"
					}
				} else {
					result["error"] = "Refresh Token expired"
				}
			}
		} else {
			result["error"] = "Invalid Refresh Token"
		}
	} else {
		result["error"] = "Empty Refresh Token"
	}

	return result, status
}

func NewDatabaseStructureFile(file string, config *config.YekongaConfig) *DatabaseStructure {
	if !helper.FileExists(file) {
		file = helper.GetPath(file)
	}

	structure := generateExtraDatabaseStructure(file)
	databaseStructure := NewDatabaseStructure(structure, config)

	return databaseStructure
}

func NewDatabaseStructure(extraDatabaseStructure DatabaseStructure, config *config.YekongaConfig) *DatabaseStructure {
	var databaseAuthStructure = DefaultAuthDatabaseStructure
	var databaseTenantStructure = DefaultTenantDatabaseStructure
	var databaseBillingStructure = DefaultBillingDatabaseStructure
	var databasePaymentStructure = DefaultPaymentDatabaseStructure
	var databaseTenantCatchStructure = DefaultTenantCatchDatabaseStructure
	// Built on a copy: the Default* structures are package-level and must not
	// pick up this app's collections (e.g. when a process builds two servers).
	var databaseStructure = make(DatabaseStructure, len(DefaultExtraDatabaseStructure))
	for k, v := range DefaultExtraDatabaseStructure {
		databaseStructure[k] = databaseCollectionFieldConfigsFromMap(v)
	}

	if config.IsAuthorizationServer {
		for k, v := range databaseAuthStructure {
			k = helper.ToCamelCase(helper.Pluralize(k))
			if existing, ok := databaseStructure[k]; ok {
				mergeExtraCollectionFields(existing, extraDatabaseStructure[k])
			} else {
				databaseStructure[k] = databaseCollectionFieldConfigsFromMap(v)
			}
		}
	}

	if config.HasTenantCatch {
		for k, v := range databaseTenantCatchStructure {
			k = helper.ToCamelCase(helper.Pluralize(k))
			if existing, ok := databaseStructure[k]; ok {
				mergeExtraCollectionFields(existing, extraDatabaseStructure[k])
			} else {
				databaseStructure[k] = databaseCollectionFieldConfigsFromMap(v)
			}
		}
	}

	if config.HasTenant {
		for k, v := range databaseTenantStructure {
			k = helper.ToCamelCase(helper.Pluralize(k))
			if existing, ok := databaseStructure[k]; ok {
				mergeExtraCollectionFields(existing, extraDatabaseStructure[k])
			} else {
				databaseStructure[k] = databaseCollectionFieldConfigsFromMap(v)
			}
		}

		if config.HasTenantBilling {
			for k, v := range databaseBillingStructure {
				k = helper.ToCamelCase(helper.Pluralize(k))
				if existing, ok := databaseStructure[k]; ok {
					mergeExtraCollectionFields(existing, extraDatabaseStructure[k])
				} else {
					databaseStructure[k] = databaseCollectionFieldConfigsFromMap(v)
				}
			}

			for k, v := range databasePaymentStructure {
				k = helper.ToCamelCase(helper.Pluralize(k))
				if existing, ok := databaseStructure[k]; ok {
					mergeExtraCollectionFields(existing, extraDatabaseStructure[k])
				} else {
					databaseStructure[k] = databaseCollectionFieldConfigsFromMap(v)
				}
			}
		}
	}

	if (!config.HasTenant || (config.HasTenant && !config.HasTenantBilling)) && config.HasPaymentModule {
		for k, v := range databasePaymentStructure {
			k = helper.ToCamelCase(helper.Pluralize(k))
			if existing, ok := databaseStructure[k]; ok {
				mergeExtraCollectionFields(existing, extraDatabaseStructure[k])
			} else {
				databaseStructure[k] = databaseCollectionFieldConfigsFromMap(v)
			}
		}
	}

	for k, v := range extraDatabaseStructure {
		k = helper.ToCamelCase(helper.Pluralize(k))
		if existing, ok := databaseStructure[k]; ok {
			mergeExtraCollectionFields(existing, v)
		} else {
			databaseStructure[k] = databaseCollectionFieldConfigsFromMap(v)
		}
	}

	return &databaseStructure

}

func generateExtraDatabaseStructure(file string) DatabaseStructure {
	structure := DatabaseStructure{}
	var extraDatabaseStructure map[string]map[string]map[string]interface{}
	data, err := helper.LoadJSONFile(file)

	if err != nil {
		fmt.Println(err)
	}

	json.Unmarshal(helper.ToByte(data), &extraDatabaseStructure)

	for collection, fields := range extraDatabaseStructure {
		fieldConfigs := make(map[string]CollectionFieldConfig, len(fields))

		for fieldName, fieldData := range fields {
			fieldConfigs[fieldName] = databaseCollectionFieldConfigFromMap(fieldData)
		}

		structure[collection] = fieldConfigs
	}

	return structure
}

// mergeExtraCollectionFields merges field definitions loaded from the external
// database structure JSON file into an existing typed CollectionStructure.
func mergeExtraCollectionFields(existing map[string]CollectionFieldConfig, extraFields map[string]CollectionFieldConfig) {
	for kn, vn := range extraFields {
		existing[kn] = databaseCollectionFieldConfigFromMap(vn)
	}
}

func databaseCollectionFieldConfigsFromMap(fields map[string]CollectionFieldConfig) map[string]CollectionFieldConfig {
	result := make(map[string]CollectionFieldConfig, len(fields))

	for kn, vn := range fields {
		result[kn] = databaseCollectionFieldConfigFromMap(vn)
	}

	return result
}

// isIntrospectionQuery checks if the query is an introspection query
func isIntrospectionQuery(query string) bool {
	// Check for common introspection patterns
	introspectionPatterns := []string{
		"__schema",
		"__type",
		"__typename",
		"IntrospectionQuery",
	}

	for _, pattern := range introspectionPatterns {
		if containsPattern(query, pattern) {
			return true
		}
	}
	return false
}

func containsPattern(s, pattern string) bool {
	// Simple contains check - in production, use a proper parser
	return len(s) > 0 && len(pattern) > 0 &&
		(s == pattern || (len(s) >= len(pattern) &&
			findSubstring(s, pattern)))
}

func findSubstring(s, pattern string) bool {
	for i := 0; i <= len(s)-len(pattern); i++ {
		if s[i:i+len(pattern)] == pattern {
			return true
		}
	}
	return false
}

func parseRequest(r *http.Request, params interface{}) error {
	// Simplified request parsing - in production, use proper JSON decoder
	return json.NewDecoder(r.Body).Decode(params)
}

func formatErrors(errs []gqlerrors.FormattedError) []gqlerrors.FormattedError {
	formatted := make([]gqlerrors.FormattedError, len(errs))

	// console.Error("GraphQL Error", errs)

	for i, err := range errs {
		// Intercept Schema/Validation errors
		if strings.Contains(err.Message, "Unknown field") || strings.Contains(err.Message, "got invalid value") {
			formatted[i] = gqlerrors.FormattedError{
				Message: "Invalid request format. Please check your input fields.",
				// You can add custom extensions for the frontend to read
			}
		} else {
			// Keep the original error or mask it for production
			formatted[i] = gqlerrors.FormattedError{
				Message: err.Message,
				// You can add custom extensions for the frontend to read
			}
		}
	}
	return formatted
}
