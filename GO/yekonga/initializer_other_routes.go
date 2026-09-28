package yekonga

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/console"
)

type WebConfig struct {
	Config                map[string]any   `json:"config"`
	ProfileConfig         map[string]any   `json:"profileConfig"`
	SystemLanguages       []map[string]any `json:"systemLanguages"`
	SystemDefaultLanguage []map[string]any `json:"systemDefaultLanguage"`
	SystemPermissions     []map[string]any `json:"systemPermissions"`
	SystemTemplateConfig  map[string]any   `json:"systemTemplateConfig"`
}

func (y *YekongaData) GetWebConfig(req *Request) WebConfig {
	locale := "en"
	tenantConfig := y.GetTenantConfig(req)

	// console.Log("Tenant config for", tenantConfig)
	// auth := *req.Auth()
	client := *req.Client()
	config := map[string]any{}
	profileConfig := map[string]any{}
	systemTemplateConfig := map[string]any{}
	systemPermissions := []map[string]any{}
	port := client.Port

	if helper.IsEmpty(port) || port == "80" || port == "443" {
		port = ""
	} else {
		port = ":" + port
	}

	baseUrl := client.Proto + "://" + client.Host + port
	systemLanguages := []map[string]interface{}{}
	systemDefaultLanguage := []map[string]interface{}{}

	listA := make([]datatype.DataMap, 0)

	if y.Config.IsAuthorizationServer {
		listA = *(y.ModelQuery("TranslatorLanguage").Find(nil))
	}

	countA := len(listA)

	for i := 0; i < countA; i++ {
		e := (listA)[i]
		d := datatype.DataMap{
			"locale": e["locale"],
			"name":   e["name"],
			"flag":   e["flag"],
			"id":     e["id"],
		}

		systemLanguages = append(systemLanguages, d)
	}

	listB := make([]datatype.DataMap, 0)
	if y.Config.IsAuthorizationServer {
		listB = *(y.ModelQuery("TranslatorTranslation").SkipBeforeCommit().Find(map[string]any{"locale": locale}))
	}
	countB := len(listB)

	for i := 0; i < countB; i++ {
		e := (listB)[i]
		d := datatype.DataMap{
			"id":           e["id"],
			"locale":       e["locale"],
			"name":         e["name"],
			"namespace":    e["namespace"],
			"group":        e["group"],
			"item":         e["item"],
			"descriptions": e["descriptions"],
			"text":         e["text"],
			"locked":       e["locked"],
		}

		systemDefaultLanguage = append(systemDefaultLanguage, d)
	}

	logoUrl := helper.GetValueOf(tenantConfig, "logoUrl")
	faviconUrl := helper.GetValueOf(tenantConfig, "faviconUrl")
	tenantName := helper.GetValueOf(tenantConfig, "tenantName")
	description := helper.GetValueOf(tenantConfig, "description")

	config["baseUrl"] = baseUrl
	config["logoUrl"] = logoUrl
	config["faviconUrl"] = faviconUrl
	config["host"] = client.Host
	config["appName"] = y.Config.AppName
	config["description"] = description
	config["tenantName"] = tenantName
	config["endToEndEncryption"] = y.Config.EndToEndEncryption
	config["apiRoute"] = y.Config.Graphql.ApiRoute
	config["authRoute"] = y.Config.Graphql.ApiAuthRoute
	config["googleApiKey"] = y.Config.GoogleApiKey
	config["googleClientId"] = y.Config.GoogleClientId
	config["googleClientSecret"] = y.Config.GoogleClientSecret
	config["cryptojsKey"] = y.Config.Authentication.CryptoJsKey
	config["cryptojsIv"] = y.Config.Authentication.CryptoJsIv
	config["userIdentifiers"] = y.Config.UserIdentifiers
	config["locale"] = locale
	config["language"] = locale

	return WebConfig{
		Config:                config,
		ProfileConfig:         profileConfig,
		SystemLanguages:       systemLanguages,
		SystemDefaultLanguage: systemDefaultLanguage,
		SystemPermissions:     systemPermissions,
		SystemTemplateConfig:  systemTemplateConfig,
	}
}

// tenantConfigPrivateKeys are TenantConfig fields never sent to clients:
// the tenant's SMTP/SMS/WhatsApp gateway settings, which include credentials.
var tenantConfigPrivateKeys = []string{"smtp", "sms", "whatsapp"}

func (y *YekongaData) initializerOtherRoutes() {

	y.All("/upload", func(req *Request, res *Response) {
		uploadFileHandler(*res.httpResponseWriter, req.HttpRequest)
	})

	y.All("/upload-files", func(req *Request, res *Response) {
		uploadMultipleFileHandler(*res.httpResponseWriter, req.HttpRequest)
	})

	y.All("/excel-to-csv", func(req *Request, res *Response) {
		uploadExcelFileHandler(*res.httpResponseWriter, req.HttpRequest)
	})

	y.All("/languages", func(req *Request, res *Response) {
		languages := []map[string]interface{}{}

		list := y.ModelQuery("TranslatorLanguage").SkipBeforeCommit().Find(nil)
		count := len(*list)

		for i := 0; i < count; i++ {
			e := (*list)[i]
			d := datatype.DataMap{
				"locale": e["locale"],
				"name":   e["name"],
				"flag":   e["flag"],
				"id":     e["locale"],
			}

			languages = append(languages, d)
		}

		res.Json(languages)
	})

	y.All("/translations/:locale", func(req *Request, res *Response) {
		locale := req.Param("locale")
		translations := []map[string]interface{}{}
		list := y.ModelQuery("TranslatorTranslation").SkipBeforeCommit().Find(map[string]any{"locale": locale})
		count := len(*list)

		for i := 0; i < count; i++ {
			e := (*list)[i]
			d := datatype.DataMap{
				"id":           e["id"],
				"locale":       e["locale"],
				"name":         e["name"],
				"namespace":    e["namespace"],
				"group":        e["group"],
				"item":         e["item"],
				"descriptions": e["descriptions"],
				"text":         e["text"],
				"locked":       e["locked"],
			}

			translations = append(translations, d)
		}

		res.Json(translations)
	})

	y.All("/config/data", func(req *Request, res *Response) {
		wetConfig := y.GetWebConfig(req)

		res.Json(map[string]interface{}{
			"config":                wetConfig.Config,
			"profileConfig":         wetConfig.ProfileConfig,
			"systemLanguages":       wetConfig.SystemLanguages,
			"systemDefaultLanguage": wetConfig.SystemDefaultLanguage,
			"systemPermissions":     wetConfig.SystemPermissions,
			"systemTemplateConfig":  wetConfig.SystemTemplateConfig,
		})
	})

	y.All("/config/report", func(req *Request, res *Response) {
		config := map[string]any{"reportConfig": nil}

		res.Json(config)
	})

	y.All("/permissions", func(req *Request, res *Response) {
		config := map[string]any{}

		auth := req.Auth()
		if auth == nil {
			config["error"] = "You must login first"
		} else {
			config["permissions"] = []interface{}{}
		}

		res.Json(config)
	})

	y.All("/config", func(req *Request, res *Response) {
		wetConfig := y.GetWebConfig(req)

		content := "window['systemLanguages'] = " + helper.ToJson(wetConfig.SystemLanguages) + ";\n" +
			"window['systemDefaultLanguage'] = " + helper.ToJson(wetConfig.SystemDefaultLanguage) + ";\n" +
			"window['systemTemplateConfig'] = {};\n" +
			"window['systemPermissions'] = " + helper.ToJson(wetConfig.SystemPermissions) + ";\n" +
			"window['systemConfig'] = " + helper.ToJson(wetConfig.Config) + ";\n" +
			"window['ProfileConfig'] = " + helper.ToJson(wetConfig.ProfileConfig) + ";\n"

		res.SetHeader("Cache-Control", "public, max-age=0")
		res.SetHeader("content-type", "application/javascript; charset=UTF-8")
		res.Byte([]byte(content))
	})

	y.Get("/tenant", func(req *Request, res *Response) {
		tenantModelName := "Tenant"
		client := req.Client()
		host := client.OriginDomain()

		var domain any
		var tenantId any
		var tenantName any

		if req.App.Config.HasTenant {
			tenant := req.App.ModelQuery(tenantModelName).SkipBeforeCommit().FindOne(datatype.DataMap{
				"domain": host,
			})

			if helper.IsEmpty(tenant) {
				tenant = req.App.ModelQuery(tenantModelName).SkipBeforeCommit().FindOne(datatype.DataMap{
					"subdomain": host,
				})
			}

			if helper.IsNotEmpty(tenant) {
				domain = host
				tenantId = helper.GetValueOfString(tenant, "_id")
				tenantName = helper.GetValueOfString(tenant, "name")
			}
		}

		if helper.IsNotEmpty(tenantId) {
			res.Json(datatype.DataMap{
				"domain":   domain,
				"tenantId": tenantId,
				"name":     tenantName,
			})
			return
		}

		res.Status(404)
		res.Json(datatype.DataMap{
			"error": "Tenant not found",
		})
	})

	y.Get("/tenant-config", func(req *Request, res *Response) {
		tenantConfig := y.GetTenantConfig(req)
		// console.Error("tenantConfig", tenantConfig)

		if helper.IsNotEmpty(tenantConfig) {
			data := helper.ToMap[interface{}](tenantConfig)

			// This route is public; the gateway settings hold credentials.
			for _, key := range tenantConfigPrivateKeys {
				delete(data, key)
			}

			res.Json(data)
			return
		}

		res.Status(404)
		res.Json(datatype.DataMap{
			"error": "Tenant config not found",
		})
	})

	y.All("/download/:filename.:ext", func(req *Request, res *Response) {
		filename := req.Param("filename")
		ext := req.Param("ext")
		title := req.Query("title")
		console.Log(filename, ext, title)

		publicDir, _ := helper.GetPublicPath()
		file := path.Join(publicDir, "tmp", filename+"."+ext)

		// console.Log(file)

		res.Download(file, title)
	})

	y.Get("/yekonga/yekonga.js", func(req *Request, res *Response) {
		scripts := ""
		client := *req.Client()
		var content []byte

		content, _ = StaticFS.ReadFile("static/dk/axios.min.js")
		scripts += string(content) + "\n"

		content, _ = StaticFS.ReadFile("static/sdk/simplepeer.min.js")
		scripts += string(content) + "\n"

		content, _ = StaticFS.ReadFile("static/sdk/yekonga.io.js")
		scripts += string(content) + "\n"

		var config string = "window.YekongaServer = {};\n" +
			"window.YekongaServer.Applications = {};\n" +
			"window.socket = null;\n" +
			"window.socketSystem = null;\n" +
			"window.YekongaServer.Host = '" + client.Host + "';\n" +
			"window.YekongaServer.Proto = '" + client.Proto + "';\n" +
			"window.YekongaServer.Port = '" + client.Port + "';\n" +
			"window.YekongaServer.graphql = '" + y.Config.Graphql.ApiRoute + "';"

		scripts += config + "\n"

		content, _ = StaticFS.ReadFile("static/sdk/webRTC.js")
		scripts += string(content) + "\n"

		content, _ = StaticFS.ReadFile("static/sdk/yekonga.js")
		scripts += string(content) + "\n"

		res.SetHeader("Cache-Control", "public, max-age=0")
		res.SetHeader("content-type", "application/javascript")
		res.Byte([]byte(scripts))
	})

	y.Get("/theme.css", runCustomCSS(y))
	y.Get("/custom-style.css", runCustomCSS(y))

	if y.Config.ApiPlaygroundEnable || y.Config.AuthPlaygroundEnable {
		y.Get("/playground", func(req *Request, res *Response) {
			content, _ := StaticFS.ReadFile("static/playground/index.html")
			html := string(content)

			apiRoute := y.AppendBaseUrl(y.Config.Graphql.ApiRoute)
			baseUrl := y.AppendBaseUrl("")
			if baseUrl == "/" {
				baseUrl = ""
			}

			data := map[string]interface{}{
				"apiRoute": apiRoute,
				"baseUrl":  baseUrl,
			}
			// console.Log("data", y.Config.BaseUrl, data)
			html = helper.TextTemplate(html, data, nil)

			res.Html(html)
		})

		y.Get("/playground/font.css", func(req *Request, res *Response) {
			content, _ := StaticFS.ReadFile("static/playground/font.css")

			res.SetHeader("content-type", "text/css; charset=utf-8")
			res.Byte(content)
		})

		y.Get("/playground/index.css", func(req *Request, res *Response) {
			content, _ := StaticFS.ReadFile("static/playground/index.css")

			res.SetHeader("content-type", "text/css; charset=utf-8")
			res.Byte(content)
		})

		y.Get("/playground/favicon.png", func(req *Request, res *Response) {
			content, _ := StaticFS.ReadFile("static/playground/fovicon.png")

			res.SetHeader("content-type", "image/png")
			res.Byte(content)
		})

		y.Get("/playground/middleware.js", func(req *Request, res *Response) {
			content, _ := StaticFS.ReadFile("static/playground/middleware.js")

			res.SetHeader("content-type", "text/javascript")
			res.Byte(content)
		})
	}

	y.Get("/placeholder.jpg", func(req *Request, res *Response) {
		content, _ := StaticFS.ReadFile("static/placeholder.jpg")

		res.SetHeader("content-type", "image/jpeg")
		res.Byte(content)
	})
}

func (y *YekongaData) initializerSocketRoutes() {

	y.Get("/yekonga.io/yekonga.io.js", func(req *Request, res *Response) {
		scripts := ""
		var content []byte

		content, _ = StaticFS.ReadFile("static/sdk/yekonga.io.js")
		scripts += string(content) + "\n"

		res.SetHeader("Cache-Control", "public, max-age=0")
		res.SetHeader("content-type", "application/javascript")
		res.Byte([]byte(scripts))
	})

	y.All("/yekonga.io/", func(req *Request, res *Response) {
		(*res.httpResponseWriter).Header().Set("access-control-allow-origin", "*")
		(*res.httpResponseWriter).Header().Set("access-control-allow-headers", "content-type, authorization, x-requested-with, x-csrf-token, timezone, upgrade-insecure-requests")

		y.socketServer.ServeWS(req, res)
	})
}

func runCustomCSS(y *YekongaData) Handler {
	return func(req *Request, res *Response) {
		content := "/* @charset \"UTF-8\"; */" +
			"" +
			""

		custom, err := y.CustomCSS(req, res)
		if err == nil && helper.IsNotEmpty(custom) {
			if str, ok := custom.(string); ok {
				content += str
			}
		}

		res.SetHeader("Cache-Control", "public, max-age=0")
		res.SetHeader("content-type", "text/css; charset=utf-8")
		res.Byte([]byte(content))
	}
}

func uploadExcelFileHandler(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		http.Error(w, "Expected multipart/form-data", http.StatusUnsupportedMediaType)
		return
	}

	// to account for form headers and boundaries.
	maxUploadSize := int64(310 << 20) // ~310 MB
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	// 1. Parse the multipart form (max 32MB in memory)
	err := r.ParseMultipartForm(32 << 20)

	if err != nil {
		console.Error(err.Error())
		http.Error(w, "Form too large", http.StatusBadRequest)
		return
	}

	// 2. Retrieve the file from form data
	file, handler, err := r.FormFile("file")

	if err != nil {
		console.Error(err.Error())
		http.Error(w, "Error retrieving the file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// 3. Create the destination directory if it doesn't exist
	uploadDir := filepath.Join(helper.GetPath("public"), "uploads")
	os.MkdirAll(uploadDir, os.ModePerm)

	fileExt := filepath.Ext(handler.Filename)
	// 4. Create a local file to save the uploaded data
	savedFile := filepath.Join(uploadDir, helper.GetHexString(24)+fileExt)
	dst, err := os.Create(savedFile)
	if err != nil {
		http.Error(w, "Error saving the file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// 5. Copy the uploaded file to the destination
	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	fileDataPath, _ := helper.ConvertExcelToCSV(savedFile)
	fileData := helper.ReadFile(fileDataPath)

	defer helper.RemoveFile(fileDataPath)
	defer helper.RemoveFile(savedFile)

	// 1. Set the header so the client (Vue/Postman) knows it's JSON
	w.Header().Set("Content-Type", "application/json")

	// 2. Prepare your data
	data := map[string]interface{}{
		"status": "success",
		"csv":    fileData,
	}

	// 4. Encode directly to the response writer
	if err := json.NewEncoder(w).Encode(data); err != nil {
		// If encoding fails, we can't change the header anymore,
		// but we can log the error.
		fmt.Println("Error encoding JSON:", err)
		fmt.Fprint(w, err.Error())
	}
}

func uploadFileHandler(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		http.Error(w, "Expected multipart/form-data", http.StatusUnsupportedMediaType)
		return
	}

	// to account for form headers and boundaries.
	maxUploadSize := int64(310 << 20) // ~310 MB
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	// 1. Parse the multipart form (max 32MB in memory)
	err := r.ParseMultipartForm(32 << 20)

	if err != nil {
		console.Error(err.Error())
		http.Error(w, "Form too large", http.StatusBadRequest)
		return
	}

	fileNames := []string{}
	// 2. Retrieve the file from form data
	file, handler, err := r.FormFile("file")

	if err != nil {
		console.Error(err.Error())
		http.Error(w, "Error retrieving the file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// 3. Create the destination directory if it doesn't exist
	uploadDir := filepath.Join(helper.GetPath("public"), "uploads")
	os.MkdirAll(uploadDir, os.ModePerm)

	fileExt := filepath.Ext(handler.Filename)
	// 4. Create a local file to save the uploaded data
	savedFile := filepath.Join(uploadDir, helper.GetHexString(24)+fileExt)
	dst, err := os.Create(savedFile)
	if err != nil {
		http.Error(w, "Error saving the file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()
	uploadedUrl := helper.GetBaseUrl("uploads/"+filepath.Base(dst.Name()), r.Host)
	fileNames = append(fileNames, uploadedUrl)

	// 5. Copy the uploaded file to the destination
	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if helper.Contains([]string{".png", ".jpg", ".jpeg", ".webp"}, fileExt) {
		// 5️⃣  Convert WebP input → JPEG output (WebP decode is supported)
		targetFile := strings.TrimSuffix(savedFile, fileExt) + ".webp"

		err := helper.ResizeFile(savedFile, targetFile, helper.ResizeOptions{
			MaxWidth:     1400,                                              // 900 was default value
			OutputFormat: strings.TrimPrefix(filepath.Ext(targetFile), "."), // "webp",
			Quality:      80,
		})
		if err == nil {
			savedFile = targetFile
		}
	}

	// 1. Set the header so the client (Vue/Postman) knows it's JSON
	w.Header().Set("Content-Type", "application/json")

	// 2. Prepare your data
	data := map[string]interface{}{
		"status": "success",
		"files":  fileNames,
	}

	// 4. Encode directly to the response writer
	if err := json.NewEncoder(w).Encode(data); err != nil {
		// If encoding fails, we can't change the header anymore,
		// but we can log the error.
		fmt.Println("Error encoding JSON:", err)
		fmt.Fprint(w, err.Error())
	}
}

func uploadMultipleFileHandler(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		http.Error(w, "Expected multipart/form-data", http.StatusUnsupportedMediaType)
		return
	}

	// to account for form headers and boundaries.
	maxUploadSize := int64(310 << 20) // ~310 MB
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	// 1. Parse the multipart form (max 32MB in memory)
	err := r.ParseMultipartForm(32 << 20)
	if err != nil {
		http.Error(w, "Forms too large", http.StatusBadRequest)
		return
	}

	// 2. Get the files from the specific key
	fileNames := []string{}
	files := r.MultipartForm.File["files"]

	uploadDir := filepath.Join(helper.GetPath("public"), "uploads")
	os.MkdirAll(uploadDir, os.ModePerm)

	for _, fileHeader := range files {
		// Open the uploaded file
		file, err := (*fileHeader).Open()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer file.Close()

		// 3. Create destination path
		fileExt := filepath.Ext(fileHeader.Filename)
		savedFile := filepath.Join(uploadDir, helper.GetHexString(24)+fileExt)
		dst, err := os.Create(savedFile)
		if err != nil {
			console.Log("Saving file to:", err.Error())

			http.Error(w, "Unable to save file", http.StatusInternalServerError)
			return
		}
		defer dst.Close()

		console.Log("Saving file to:", savedFile)
		// 4. Copy the data
		if _, err := io.Copy(dst, file); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if helper.Contains([]string{".png", ".jpg", ".jpeg", ".webp"}, fileExt) {
			// 5️⃣  Convert WebP input → JPEG output (WebP decode is supported)
			targetFile := strings.TrimSuffix(savedFile, fileExt) + ".webp"

			err := helper.ResizeFile(savedFile, targetFile, helper.ResizeOptions{
				MaxWidth:     1400,                                              // 900 was default value
				OutputFormat: strings.TrimPrefix(filepath.Ext(targetFile), "."), // "webp",
				Quality:      80,
			})
			if err == nil {
				savedFile = targetFile
			}
		}

		uploadedUrl := helper.GetBaseUrl("uploads/"+filepath.Base(savedFile), r.Host)
		fileNames = append(fileNames, uploadedUrl)

		fmt.Printf("Saved: %s\n", fileHeader.Filename)
	}

	// 1. Set the header so the client (Vue/Postman) knows it's JSON
	w.Header().Set("Content-Type", "application/json")

	// 2. Prepare your data
	data := map[string]interface{}{
		"status": "success",
		"files":  fileNames,
	}

	w.WriteHeader(http.StatusOK)

	// 4. Encode directly to the response writer
	if err := json.NewEncoder(w).Encode(data); err != nil {
		// If encoding fails, we can't change the header anymore,
		// but we can log the error.
		fmt.Println("Error encoding JSON:", err)
		fmt.Fprint(w, err.Error())
	}
}
