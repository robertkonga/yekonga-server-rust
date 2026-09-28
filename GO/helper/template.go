package helper

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/robertkonga/yekonga-server-go/config"
)

//go:embed static/*
var StaticFS embed.FS

var (
	reTemplatePlaceholder = regexp.MustCompile(`{{\s*([a-zA-Z0-9_]+(?:\.[a-zA-Z0-9_]+)*)\s*}}`)
	reHTMLTag             = regexp.MustCompile(`<[^>]+>`)
	reDisallowedChars     = regexp.MustCompile(`[^a-zA-Z0-9'"?!.,;:-_&()+\s]`)
)

// TextTemplate replaces {{key}} and {{nested.key}} placeholders
// Uses the safer FindAllStringSubmatchIndex approach to avoid infinite loops
func TextTemplate(templateString string, data map[string]interface{}, customPattern *regexp.Regexp) string {
	if customPattern == nil {
		// Supports: {{ name }}, {{ user.name }}, {{ items.0.price }}, spaces around name
		customPattern = reTemplatePlaceholder
	}

	result := []byte(templateString)
	var buf strings.Builder

	lastIndex := 0

	for _, match := range customPattern.FindAllStringSubmatchIndex(string(result), -1) {
		// match[0] = start of whole match
		// match[1] = start of capture group (the key)
		// match[2] = end   of capture group

		keyStart, keyEnd := match[2], match[3]
		key := string(result[keyStart:keyEnd])

		// Write text before this placeholder
		buf.Write(result[lastIndex:match[0]])

		// Get value and convert to string
		value := GetNestedValue(data, key)
		if value != nil {
			buf.WriteString(fmt.Sprintf("%v", value))
		} // else → leave empty (you can also write "MISSING" or similar)

		lastIndex = match[1] // end of this match
	}

	// Write remaining text after last match
	buf.Write(result[lastIndex:])

	return buf.String()
}

// getTextContent reads a template file and processes it with data
func GetTextContent(templateName string, data map[string]interface{}) string {
	content := contentFile(templateName, "text", ".text")
	content = TextTemplate(content, data, nil)

	// Assuming clearSpecialCharacters is defined elsewhere
	content = ClearSpecialCharacters(content)

	return content
}

// getWhatsappContent processes template content and attempts to parse it as JSON
func GetWhatsappContent(templateName string, data map[string]interface{}) string {
	content := contentFile(templateName, "whatsapp", ".text")
	content = TextTemplate(content, data, nil)

	var result string
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		// Ignore JSON parse error, return content as-is
		return content
	}

	return result
}

// getEmailContent reads a template file, processes it, and wraps it in a layout
func GetEmailContent(layout, templateName string, data map[string]interface{}) string {
	content := contentFile(templateName, "mail", ".html")
	content = TextTemplate(content, data, nil)

	if IsEmpty(layout) {
		temp, err := StaticFS.ReadFile(templateName)
		if err == nil {
			layout = GetPath("temp/" + UUID())
			SaveToFile(temp, layout)
		}
	}

	// Assuming getEmailLayout is defined elsewhere
	return GetEmailLayout(layout, content, data)
}

// getEmailLayout processes an email layout template with content and server.Configuration data
func GetEmailLayout(layout, content string, data map[string]interface{}) string {
	dirname := GetDirectoryPath()
	temp := ""
	domain := ""
	if v, ok := data["domain"].(string); ok {
		domain = v
	}
	website := GetBaseUrl("", domain) // Assuming getBaseUrl is defined elsewhere
	logo := website + "/img/mail/@notificationId/logo.png"
	appName := config.Config.AppName
	year := ToTimestampString(nil, "2006") // Go uses "2006" for YYYY
	primaryColor := "#306da7"
	secondaryColor := "#306da7"
	darkBackgroundColor := "#033360"

	if IsNotEmpty(config.Config.Branding) {
		if IsNotEmpty(config.Config.Branding.PrimaryColor) {
			primaryColor = config.Config.Branding.PrimaryColor
		}

		if IsNotEmpty(config.Config.Branding.SecondaryColor) {
			secondaryColor = config.Config.Branding.SecondaryColor
		}

		if IsNotEmpty(config.Config.Branding.DarkBackgroundColor) {
			darkBackgroundColor = config.Config.Branding.DarkBackgroundColor
		}
	}

	if layout != "" {
		filePath := filepath.Join(dirname, layout)
		if data, err := os.ReadFile(filePath); err == nil {
			temp = string(data)
		} else {
			// Log error (replace with proper logging if needed)
			println("Error reading layout file:", err.Error())
		}
	}

	if temp == "" || strings.TrimSpace(temp) == "" {
		if config.Config.EmailTemplate != "" {
			filePath := filepath.Join(dirname, config.Config.EmailTemplate)
			if data, err := os.ReadFile(filePath); err == nil {
				temp = string(data)
			} else {
				// Log error (replace with proper logging if needed)
				println("Error reading email template file:", err.Error())
			}
		}
	}

	if temp == "" || strings.TrimSpace(temp) == "" {
		filePath := filepath.Join(dirname, "assets/mail/email.html")
		data, err := os.ReadFile(filePath)
		if err != nil {
			// Log error (replace with proper logging if needed)
			println("Error reading default email template:", err.Error())
			return ""
		}
		temp = string(data)
	}

	htmlContent := TextTemplate(temp, map[string]interface{}{
		"content":             content,
		"logo":                logo,
		"website":             website,
		"appName":             appName,
		"year":                year,
		"primaryColor":        primaryColor,
		"secondaryColor":      secondaryColor,
		"darkBackgroundColor": darkBackgroundColor,
	}, nil)

	return htmlContent
}

func contentFile(templateName string, category string, ext string) string {
	var staticPath = path.Join("static", category, templateName+ext)
	templateByte, err := StaticFS.ReadFile(staticPath)

	if err != nil {
		if _, err := os.Stat(templateName); err == nil {
			// is the <templateName> is actually file path
			templateByte = []byte(ReadFile(GetPath(templateName)))
		} else {
			dirname := GetDirectoryPath()
			if _, err := os.Stat(filepath.Join(dirname, templateName)); err == nil {
				// is the <templateName> is relative file path
				templateByte = []byte(ReadFile(filepath.Join(dirname, templateName)))
			} else {
				// the <templateName> is actually content it self
				templateByte = []byte(templateName)
			}
		}
	}

	return string(templateByte)
}

// GetNestedValue returns the value at the dot-separated path or nil if not found
func GetNestedValue(data map[string]interface{}, path string) interface{} {
	parts := strings.Split(path, ".")
	current := interface{}(data)

	for _, part := range parts {
		if current == nil {
			return nil
		}
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		current, ok = m[part]
		if !ok {
			return nil
		}
	}
	return current
}

// clearSpecialCharacters cleans the input string by replacing curly apostrophes,
// removing HTML tags, and keeping only allowed characters.
func ClearSpecialCharacters(val string) string {
	// If val is empty, return an empty string
	if val == "" {
		return ""
	}

	// Replace curly apostrophes (’) with straight apostrophes (')
	val = strings.ReplaceAll(val, "’", "'")

	// Remove HTML tags (e.g., <p>, <div>, etc.)
	val = reHTMLTag.ReplaceAllString(val, "")

	// Keep only allowed characters: a-z, A-Z, 0-9, '"?!.,;:-_&()+\s
	val = reDisallowedChars.ReplaceAllString(val, "")

	return val
}

// ExtractEmbedFile takes a file from the embed.FS and writes it to the OS temp directory.
// It returns the absolute path to the temporary file and a cleanup function.
func ExtractEmbedFile(fs embed.FS, embedPath string) (string, func(), error) {
	// 1. Read the data from the embedded filesystem
	data, err := fs.ReadFile(embedPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read embedded file: %w", err)
	}

	// 2. Create a temp file
	// "" uses the default system temp dir (e.g., /tmp or C:\Users\...\Temp)
	// The pattern "static-*" adds a random suffix for concurrency safety
	tmpFile, err := os.CreateTemp("", "ext-asset-*"+filepath.Ext(embedPath))
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	// 3. Write the embedded data to the physical temp file
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("failed to write to temp file: %w", err)
	}

	// Close the file handle so other processes can read it
	tmpFile.Close()

	// 4. Provide a cleanup function to delete the file after use
	cleanup := func() {
		os.Remove(tmpFile.Name())
	}

	return tmpFile.Name(), cleanup, nil
}
