package yekonga

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/jwt"
)

// Define custom middleware keys
type MiddlewareType string

const (
	GlobalMiddleware  MiddlewareType = "global"
	InitMiddleware    MiddlewareType = "init"
	PreloadMiddleware MiddlewareType = "preload"
	CatchMiddleware   MiddlewareType = "catch"
)

func BillingMiddleware(req *Request, res *Response) (int, error) {
	return http.StatusOK, nil
}

// Middleware to set client detail
func ClientMiddleware(req *Request, res *Response) (int, error) {
	r := req.HttpRequest
	protoList := strings.Split(strings.ToLower(r.Proto), "/")
	hostList := strings.Split(strings.ToLower(r.Host), ":")
	proto := protoList[0]
	host := hostList[0]
	port := ""
	ipAddress := helper.GetClientIP(r)
	origin := r.Header.Get("origin")
	altProto := r.Header.Get("X-Forwarded-Proto")

	if len(hostList) > 1 {
		port = hostList[len(hostList)-1]
	}

	if helper.IsNotEmpty(altProto) {
		proto = altProto
	}

	if helper.IsEmpty(origin) {
		referer := r.Header.Get("referer")
		origin = helper.ExtractDomain(referer)

		if helper.IsEmpty(origin) {
			origin = "" + host
		}
	}

	origin = proto + "://" + helper.ExtractDomain(origin)

	client := ClientPayload{
		Host:      host,
		Proto:     proto,
		Port:      port,
		Path:      r.URL.Path,
		Method:    strings.ToLower(r.Method),
		Origin:    origin,
		UserAgent: r.UserAgent(),
		IpAddress: ipAddress,
	}

	req.SetContext(string(ClientPayloadKey), client)

	return http.StatusOK, nil
}

func TenantCatchMiddleware(req *Request, res *Response) (int, error) {
	client := *req.Client()
	host := client.OriginDomain()
	// console.Log("host", client)

	var tenantId interface{}

	if req.App.Config.HasTenant {
		tenant, _ := req.App.tenantByHost(host)

		if tenant != nil {
			tenantId = helper.GetValueOfString(tenant, "_id")
		}

		if helper.IsEmpty(tenantId) && req.App.Config.TenantOnly {
			return http.StatusBadRequest, errors.New("Tenant not found for the request")
		}

		if helper.IsNotEmpty(tenantId) {
			req.SetTenantId(tenantId)

			tenantConfig := req.App.GetTenantConfig(req)
			// console.Log("tenantConfig", tenantConfig)

			if helper.IsNotEmpty(tenantConfig) {
				req.SetTenant(*tenantConfig)
			}

		}
	} else if req.App.Config.HasTenantCatch {
		tenant, err := req.App.FetchTenantByDomain(host, req, res)

		if err == nil {
			if helper.IsNotEmpty(tenant) {
				tenantId = helper.GetValueOf(tenant, "tenantId")
			}
		}
	}

	if req.App.Config.HasTenant || req.App.Config.HasTenantCatch {
		mainDomain := helper.GetMainDomain(host)
		// console.Error("Tenant", "host:", host, "mainDomain:", mainDomain)

		if helper.IsNotEmpty(mainDomain) {
			if host != *mainDomain {
				if helper.IsEmpty(tenantId) {
					return http.StatusNotFound, errors.New("Tenant not found")
				}
			}
		}
	}

	if helper.IsNotEmpty(tenantId) {
		client.TenantId = tenantId

		req.SetTenantId(tenantId)
		req.SetContext(string(ClientPayloadKey), client)
	}

	return http.StatusOK, nil
}

// tokenPaths are the routes where a missing or invalid access token isn't an
// error: exact paths, and path.Match patterns.
type tokenPaths struct {
	exact    []string
	patterns []string
}

// tokenOptionalPaths is built once: it only depends on config.
func (y *YekongaData) tokenOptionalPaths() *tokenPaths {
	y.tokenPathsOnce.Do(func() {
		config := y.Config
		y.tokenPaths = &tokenPaths{
			exact: []string{
				y.AppendBaseUrl("me"),
				y.AppendBaseUrl("logout"),
				y.AppendBaseUrl("refresh"),
				y.AppendBaseUrl("languages"),
				y.AppendBaseUrl("excel-to-csv"),
				y.AppendBaseUrl("upload"),
				y.AppendBaseUrl("upload-files"),
				y.AppendBaseUrl("config/data"),
				y.AppendBaseUrl("config/report"),
				y.AppendBaseUrl("permissions"),
				y.AppendBaseUrl("config"),
				y.AppendBaseUrl("tenant"),
				y.AppendBaseUrl("tenant-config"),
				y.AppendBaseUrl("theme.css"),
				y.AppendBaseUrl("custom-style.css"),
				y.AppendBaseUrl(config.RestApi),
				y.AppendBaseUrl(config.RestAuthApi),
				y.AppendBaseUrl(config.Graphql.ApiRoute),
				y.AppendBaseUrl(config.Graphql.ApiAuthRoute),
			},
			patterns: []string{
				y.AppendBaseUrl("me/*"),
				y.AppendBaseUrl("refresh/*"),
				y.AppendBaseUrl("download/*"),
				y.AppendBaseUrl("translations/*"),
				y.AppendBaseUrl("image/*"),
			},
		}
	})

	return y.tokenPaths
}

// Middleware to add token as a string
func TokenMiddleware(req *Request, res *Response) (int, error) {
	app := req.App
	config := req.App.Config
	clientPayload := req.Client()
	moduleName := req.Param("moduleName")
	masterKey := req.GetContext(string(MasterKey))
	domain := clientPayload.OriginDomain()
	isJson := (strings.Contains(res.request.GetHeader("content-type"), "json"))

	if !isJson {
		isJson = (strings.Contains(res.request.GetHeader("accept"), "json"))
	}

	currentPath := req.HttpRequest.URL.Path
	optional := app.tokenOptionalPaths()
	mandatoryValidToken := !helper.Contains(optional.exact, currentPath)
	for _, pattern := range optional.patterns {
		if !mandatoryValidToken {
			break
		}
		if helper.MatchPath(currentPath, pattern) {
			mandatoryValidToken = false
		}
	}

	for _, v := range req.App.publicRoutes {
		test := helper.MatchPath(currentPath, app.AppendBaseUrl(v))
		if test {
			mandatoryValidToken = !test
			break
		}
	}

	var isValid bool
	var accessToken string
	var refreshToken string
	var tokenPayloadMap datatype.DataMap
	var tokenPayload TokenPayload

	accessCookie, err := req.HttpRequest.Cookie(string(AccessTokenKey))
	if err == nil {
		accessToken = accessCookie.Value
	}

	refreshCookie, err := req.HttpRequest.Cookie(string(RefreshTokenKey))
	if err == nil {
		refreshToken = refreshCookie.Value
	}

	if helper.IsEmpty(accessToken) {
		var values []string = req.HttpRequest.Header["Authorization"]

		if len(values) > 0 {
			// Anything other than "Bearer <token>" (e.g. Basic auth added by a
			// proxy) carries no access token. It used to panic the request.
			accessToken, _ = bearerToken(values[0])
		}
	}

	logoutUrl := "logout"
	if helper.IsNotEmpty(moduleName) {
		logoutUrl = "logout/" + moduleName
	}

	if helper.IsNotEmpty(accessToken) {
		isValid, tokenPayloadMap = jwt.DecodeJWTInto(accessToken, config.Authentication.SecretToken, &tokenPayload)

		if !isValid || helper.IsEmpty(tokenPayloadMap) {
			if mandatoryValidToken {
				if !isJson {
					return http.StatusTemporaryRedirect, errors.New(helper.GetBaseUrl(logoutUrl, domain))
				}

				return http.StatusTemporaryRedirect, errors.New("Access token invalid")
			}
		}

		if tokenPayload.ExpiresAt.Before(helper.GetTimestamp(nil)) {
			if mandatoryValidToken {
				if !isJson {
					return http.StatusTemporaryRedirect, errors.New(helper.GetBaseUrl("refresh", domain) + "?" + "redirect=" + currentPath)
				}

				return http.StatusUnauthorized, errors.New("Token expired")
			}
		}

		if domain != tokenPayload.Domain && helper.IsNotEmpty(tokenPayload.Domain) {
			if !isJson {
				return http.StatusTemporaryRedirect, errors.New(helper.GetBaseUrl(logoutUrl, domain))
			}

			return http.StatusUnauthorized, errors.New("Domain mismatch expired")
		}

		if req.App.Config.TenantOnly {
			if helper.IsNotEmpty(tokenPayload) && helper.IsEmpty(tokenPayload.TenantId) {
				return http.StatusBadRequest, errors.New("tenant not found for the request")
			}
		}

		req.SetContext(string(AccessTokenKey), accessToken)
		req.SetContext(string(TokenPayloadKey), tokenPayload)

		if helper.IsNotEmpty(tokenPayload.TenantId) && helper.IsEmpty(req.TenantId()) {
			req.SetTenantId(tokenPayload.TenantId)
		}
	}

	if helper.IsNotEmpty(refreshToken) {
		req.SetContext(string(RefreshTokenKey), refreshToken)
	}

	if config.AuthorizedOnly {
		if !(helper.IsNotEmpty(masterKey) && masterKey == config.MasterKey) && (mandatoryValidToken && helper.IsEmpty(accessToken)) {
			if isJson {
				return http.StatusUnauthorized, errors.New("Must be authorized/login")
			}
		}
	}

	return http.StatusOK, nil
}

// Middleware to add user info as a map
func UserInfoMiddleware(req *Request, res *Response) (int, error) {
	var userInfo *datatype.DataMap
	tokenPayload := req.GetContext(string(TokenPayloadKey))

	// console.Error("tokenPayload", tokenPayload)

	if helper.IsNotEmpty(tokenPayload) {
		if payload, ok := tokenPayload.(TokenPayload); ok {
			id := payload.UserId

			if helper.IsNotEmpty(id) {
				if req.App.Config.IsAuthorizationServer {
					userInfo = req.App.userById(id)
				} else {
					data := helper.ToDataMap(payload.ToMap())
					data["_id"] = id
					data["id"] = id

					userInfo = &data
				}

			}
		}
	}

	// console.Error("userInfo", userInfo)

	if helper.IsNotEmpty(userInfo) {
		req.SetContext(string(UserInfoPayloadKey), *userInfo)
	}

	return http.StatusOK, nil
}

func ApplicationIDMiddleware(req *Request, res *Response) (int, error) {
	// Payment gateways can't send the application key; their webhooks are
	// authenticated by the provider's own signature check instead.
	if req.App.isPaymentWebhookPath(req.HttpRequest.URL.Path) {
		return http.StatusOK, nil
	}

	if req.App.Config.EnableAppKey {

		var appKey string = req.GetHeader("application-key")
		var path string = req.HttpRequest.URL.Path
		var extension string = filepath.Ext(path)
		var allowed = []string{
			".css",
			".js",
			".ico",
			".pdf",
			".flv",
			".jpg",
			".jpeg",
			".png",
			".gif",
			".webp",
			".woff2",
			".woff",
			".ttf",
			".eot",
		}

		if helper.IsEmpty(appKey) {
			appKey = req.GetHeader("X-Core-Application-Id")
		}

		if !helper.Contains(allowed, extension) {
			if helper.IsEmpty(appKey) {
				appKey = req.Query("application-key")

				if helper.IsEmpty(appKey) {
					appKey = req.Query("app-key")
				}
			}

			if helper.IsNotEmpty(appKey) {
				if appKey != req.App.Config.AppKey {
					return http.StatusUnauthorized, errors.New("application key invalid")
				}
			} else {
				return http.StatusUnauthorized, errors.New("application key not provided")
			}
		}
	}

	return http.StatusOK, nil
}

func MasterKeyMiddleware(req *Request, res *Response) (int, error) {
	var masterKey string = req.GetHeader("master-key")

	if helper.IsEmpty(masterKey) {
		masterKey = req.GetHeader("X-Core-Master-Key")
	}

	if helper.IsEmpty(masterKey) {
		masterKey = req.Query("master-key")
	}

	if helper.IsNotEmpty(masterKey) {
		req.SetContext(string(MasterKey), masterKey)

		if masterKey != req.App.Config.MasterKey {
			return http.StatusUnauthorized, errors.New("master key invalid")
		}
	}

	return http.StatusOK, nil
}
