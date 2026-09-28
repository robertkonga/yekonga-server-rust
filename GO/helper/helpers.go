package helper

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nfnt/resize"
	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper/console"
	"github.com/robertkonga/yekonga-server-go/helper/logger"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
	"github.com/robertkonga/yekonga-server-go/plugins/uuid"
	"github.com/skip2/go-qrcode"
	"golang.org/x/image/draw"
)

func IsMap(data interface{}) bool {
	val := reflect.ValueOf(data)

	// Dereference pointers to check the underlying value
	if val.Kind() == reflect.Ptr {
		if !val.IsNil() {
			val = val.Elem()
		}
	}

	if IsNotEmpty(data) {
		ok := val.Kind() == reflect.Map

		// Type assertion to check if data is a map[string]interface{}
		if ok {
			return true
		} else if _, ok := val.Interface().(datatype.Record); ok {
			return true
		} else if _, ok := val.Interface().(datatype.DataMap); ok {
			return true
		} else if _, ok := val.Interface().(datatype.Context); ok {
			return true
		} else if _, ok := val.Interface().(datatype.ContextObject); ok {
			return true
		} else if _, ok := val.Interface().(datatype.JsonObject); ok {
			return true
		} else if _, ok := val.Interface().(bson.M); ok {
			return true
		}
	}

	return false
}

func IsMapList(data interface{}) bool {
	val := reflect.ValueOf(data)

	// Dereference pointers to check the underlying value
	if val.Kind() == reflect.Ptr {
		if !val.IsNil() {
			val = val.Elem()
		}
	}

	if IsNotEmpty(data) {
		ok := val.Kind() == reflect.Array || val.Kind() == reflect.Slice

		// Type assertion to check if data is a map[string]interface{}
		if ok {
			return true
		} else if _, ok := val.Interface().([]datatype.Record); ok {
			return true
		} else if _, ok := val.Interface().([]datatype.DataMap); ok {
			return true
		} else if _, ok := val.Interface().([]datatype.Context); ok {
			return true
		} else if _, ok := val.Interface().([]datatype.ContextObject); ok {
			return true
		} else if _, ok := val.Interface().([]datatype.JsonObject); ok {
			return true
		} else if _, ok := val.Interface().([]bson.M); ok {
			return true
		}
	}

	return false
}

func ToDataMap(input interface{}) datatype.DataMap {
	result := make(map[string]interface{})

	value := reflect.ValueOf(input)

	if value.Kind() != reflect.Map {
		if value.Kind() == reflect.Ptr {
			if !value.IsNil() {
				value = value.Elem()
			}
		} else {
			console.Error("input must be a map => input:", input)
		}
	}

	if value.Kind() == reflect.Map {
		for _, key := range value.MapKeys() {
			result[fmt.Sprintf("%v", key.Interface())] = value.MapIndex(key).Interface()
		}
	}

	return result
}

func ToDataMapList(data interface{}) []datatype.DataMap {
	val := reflect.ValueOf(data)
	if val.Kind() != reflect.Slice {
		return nil
	}

	result := make([]datatype.DataMap, val.Len())
	for i := 0; i < val.Len(); i++ {
		item := val.Index(i).Interface()

		// Use a simple type switch for the individual items
		result[i] = ToDataMap(item)
	}
	return result
}

func ToInt(value interface{}) int {
	number := 0
	if v, ok := value.(string); ok {
		n, err := strconv.Atoi(v)
		if err == nil {
			number = n
		}
	} else if v, ok := value.(int); ok {
		number = v
	} else if IsNumeric(value) {
		n, err := strconv.ParseInt(fmt.Sprintf("%v", value), 32, 64)
		if err == nil {
			number = int(n)
		}
	}

	return number
}

func ToFloat(value interface{}) float64 {
	// console.Log("ToFloat", fmt.Sprintf("%v", value))

	var number float64 = 0
	if v, ok := value.(string); ok {
		n, err := strconv.ParseFloat(v, 64)
		if err == nil {
			number = n
		}
	} else if v, ok := value.(float64); ok {
		number = v
	} else if v, ok := value.(int); ok {
		n, err := strconv.ParseFloat(strconv.Itoa(v), 64)
		if err == nil {
			number = n
		}
	} else if IsNumeric(value) {
		n, err := strconv.ParseFloat(fmt.Sprintf("%v", value), 64)
		if err == nil {
			number = n
		}
	}

	// console.Log("ToFloat", GetType(number), number)

	return number
}

func ToFloat64(value interface{}) float64 {
	return ToFloat(value)
}

func CompareValues(a, b interface{}) int {
	// Convert both values to float64 for comparison
	aVal := ToFloat64(a)
	bVal := ToFloat64(b)

	if aVal < bVal {
		return -1
	} else if aVal > bVal {
		return 1
	}
	return 0
}

func ToJsonFormatted(data interface{}) string {
	jsonData, _ := json.MarshalIndent(data, "", "    ")

	return string(jsonData)
}

func ToJson(data interface{}) string {
	jsonData, _ := json.Marshal(data)

	return string(jsonData)
}

func ToByte(data interface{}) []byte {
	jsonData, _ := json.Marshal(data)

	return jsonData
}

// JSON file and converts it to a map
func ToMap[T any](data interface{}) map[string]T {
	// Map inputs are converted directly: this runs per resolver call and per
	// row, and the result is identical to the JSON round-trip below.
	if result, handled := toMapFast[T](data); handled {
		return result
	}

	var result map[string]T
	var dataByte []byte
	if s, ok := data.(string); ok {
		dataByte = []byte(s)
	} else {
		dataByte = ToByte(data)
	}

	if err := json.Unmarshal(dataByte, &result); err != nil {
		return nil
	}

	return result
}

func ToMapList[T any](data interface{}) []map[string]T {
	converted := []map[string]T{}
	val := reflect.ValueOf(data)

	if val.Kind() == reflect.Slice || val.Kind() == reflect.Array {
		count := val.Len()
		converted = make([]map[string]T, count)

		for i := 0; i < count; i++ {
			elem := ToMap[T](val.Index(i).Interface())
			converted[i] = elem
		}
	}

	return converted
}

func ToList[T any](data interface{}) []T {
	converted := []T{}
	val := reflect.ValueOf(data)

	if val.Kind() == reflect.Slice || val.Kind() == reflect.Array {
		count := val.Len()
		converted = make([]T, 0, count)

		for i := 0; i < count; i++ {
			var result T
			if err := json.Unmarshal(ToByte(val.Index(i).Interface()), &result); err != nil {
				console.Error("ToList", err.Error())
			} else {
				converted = append(converted, result)
			}
		}

	}

	return converted
}

func ToInterface(data interface{}) (interface{}, error) {
	var result interface{}

	// JSON file and converts it to a map
	if err := json.Unmarshal(ToByte(data), &result); err != nil {
		console.Error("ConvertTo", err.Error())
		return result, err
	}

	return result, nil
}

func UUID() string {
	id, _ := uuid.NewV1()

	return id.String()
}

func IsSlice(v interface{}) bool {
	if v == nil {
		return false
	}

	if IsPointer(v) {
		return IsSlice(reflect.ValueOf(v).Elem().Interface())
	}

	t := reflect.TypeOf(v)

	// Exclude MongoDB ObjectID
	if t == reflect.TypeOf(bson.ObjectID{}) {
		return false
	}

	kind := t.Kind()
	return kind == reflect.Array || kind == reflect.Slice
}

func IsList(v interface{}) bool {
	return IsSlice(v)
}

func IsArray(v interface{}) bool {
	return IsSlice(v)
}

func IsUsernameIdentifier() bool {
	return (len(config.Config.UserIdentifiers) == 0 || Contains(config.Config.UserIdentifiers, "username"))
}

func IsPhoneIdentifier() bool {
	return (len(config.Config.UserIdentifiers) == 0 || Contains(config.Config.UserIdentifiers, "phone"))
}

func IsEmailIdentifier() bool {
	return (len(config.Config.UserIdentifiers) == 0 || Contains(config.Config.UserIdentifiers, "email"))
}

func IsWhatsappIdentifier() bool {
	return (len(config.Config.UserIdentifiers) == 0 || Contains(config.Config.UserIdentifiers, "whatsapp"))
}

// convertTo converts a map[string]interface{} to a struct of type T
func ConvertTo[T any](data interface{}) (T, error) {
	var result T

	// JSON file and converts it to a map
	if err := json.Unmarshal(ToByte(data), &result); err != nil {
		console.Error("ConvertTo", err.Error())
		return result, err
	}

	return result, nil
}

// convertTo converts a map[string]interface{} to a struct of type T
func ConvertToDataMap(data map[string]interface{}) datatype.DataMap {
	var result datatype.DataMap

	// Get the reflect.Value of the result struct
	val := reflect.ValueOf(&result).Elem()
	if !val.CanSet() {
		return result
	}

	// Ensure the result is a struct
	if val.Kind() != reflect.Struct {
		return result
	}

	// Iterate over the struct fields
	typ := val.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fieldName := field.Name

		// Look for a matching key in the map (case-sensitive)
		mapValue, exists := data[fieldName]
		if !exists {
			continue // Skip if the map doesn't have this field
		}

		// Get the reflect.Value of the field
		fieldVal := val.Field(i)
		if !fieldVal.CanSet() {
			continue // Skip if the field is unexported
		}

		// Convert the map value to the field's type
		if err := setField(fieldVal, mapValue); err != nil {
			return result
		}
	}

	return result
}

// setField sets the reflect.Value of a struct field to the provided value
func setField(field reflect.Value, value interface{}) error {
	// Convert the value to the field's type
	val := reflect.ValueOf(value)

	// Check if the value can be converted to the field's type
	if !val.Type().ConvertibleTo(field.Type()) {
		return fmt.Errorf("cannot convert %v to %v", val.Type(), field.Type())
	}

	// Perform the conversion and set the field
	field.Set(val.Convert(field.Type()))
	return nil
}

// IsEmpty checks if a value is empty/zero for various types
func IsEmpty(v interface{}) bool {
	if v == nil {
		return true
	}

	val := reflect.ValueOf(v)

	// Dereference pointers to check the underlying value
	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return true
		}
		// Get the element the pointer points to
		val = val.Elem()
	}

	switch val.Kind() {
	case reflect.String:
		return val.Len() == 0

	case reflect.Bool:
		return !val.Bool()

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return val.Int() == 0

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return val.Uint() == 0

	case reflect.Float32, reflect.Float64:
		return val.Float() == 0

	case reflect.Complex64, reflect.Complex128:
		return val.Complex() == 0

	case reflect.Interface:
		if val.IsNil() {
			return true
		}
		return IsEmpty(val.Interface())

	case reflect.Array, reflect.Slice:
		return val.Len() == 0

	case reflect.Map:
		return val.Len() == 0

	case reflect.Chan:
		return val.IsNil()

	case reflect.Struct:
		// Check if it's time.Time specifically
		if t, ok := val.Interface().(time.Time); ok {
			return t.IsZero()
		}
		// For other structs, compare with zero value
		return val.IsZero()

	case reflect.Func:
		return val.IsNil()

	default:
		// Use IsZero for any other types (available in Go 1.13+)
		return val.IsZero()
	}
}

func IsNotEmpty(value interface{}) bool {
	return !IsEmpty(value)
}

// CreateFuzzyRegex converts "R F Konga" into "(?i)R.*F.*Konga"
func CreateFuzzyRegex(input string) string {
	// 1. Split by whitespace
	parts := strings.Fields(input)

	// 2. Escape special characters in each part to prevent injection
	for i, part := range parts {
		parts[i] = regexp.QuoteMeta(part)
	}

	// 3. Join with .* and add the case-insensitive flag (?i)
	pattern := "(?i)" + strings.Join(parts, ".*")
	return pattern
}

// Utility function to check if slice contains an element
func Contains(slice []string, item string) bool {
	for _, v := range slice {
		if v == item {
			return true
		}
	}
	return false
}

func Reverse[T interface{}](slice []T) {
	for i, j := 0, len(slice)-1; i < j; i, j = i+1, j-1 {
		slice[i], slice[j] = slice[j], slice[i]
	}
}

func SortMap[T interface{}](options map[string]T) map[string]T {
	// 1. Get all keys into a slice
	keys := make([]string, 0, len(options))
	for k := range options {
		keys = append(keys, k)
	}

	// 2. Sort the keys based on the map values
	slices.SortFunc(keys, func(a, b string) int {
		return cmp.Compare(ToString(options[a]), ToString(options[b]))
	})

	// 3. Print the results in order
	fmt.Println("Sorted by Value:", keys)
	newOptions := make(map[string]T)
	for _, k := range keys {
		fmt.Printf("%s: %v\n", k, options[k])
		newOptions[k] = options[k]
	}

	return newOptions
}

func SortedKeys[T interface{}](options map[string]T) []string {
	// 1. Get all keys into a slice
	keys := make([]string, 0, len(options))
	for k := range options {
		keys = append(keys, k)
	}

	// 2. Sort the keys based on the map values
	slices.SortFunc(keys, func(a, b string) int {
		return cmp.Compare(ToString(options[a]), ToString(options[b]))
	})

	return keys
}

// ToCamelCase converts a string to CamelCase
func ToCamelCase(s string) string {
	s = ToUnderscore(s)
	words := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})

	for i := range words {
		words[i] = strings.Title(strings.ToLower(words[i]))
	}

	return strings.Join(words, "")
}

// ToSlug converts a string to a URL-friendly slug
func ToSlug(s string) string {
	// Convert to lowercase
	s = ToUnderscore(s)

	// Replace non-alphanumeric characters with hyphens
	s = reSlugInvalid.ReplaceAllString(s, "-")

	// Trim leading and trailing hyphens
	s = strings.Trim(s, "-")

	return s
}

func ToString(s interface{}) string {
	return fmt.Sprintf("%v", s)
}

// camelToSnake converts camelCase or PascalCase to snake_case
func CamelToSnake(s string) string {
	var (
		result    []rune
		prevLower bool
		prevUpper bool
		prevDigit bool
	)
	for _, r := range s {

		isLower := unicode.IsLower(r)
		isUpper := unicode.IsUpper(r)
		isDigit := unicode.IsDigit(r)

		if (prevLower && isUpper) ||
			(prevDigit && (isLower || isUpper)) ||
			(isDigit && (prevLower || prevUpper)) {
			result = append(result, '_')
		}

		// if isUpper {
		// 	// Add underscore before uppercase letter (except first char)
		// 	result = append(result, '_')
		// }
		result = append(result, unicode.ToLower(r))

		prevLower = isLower
		prevUpper = isUpper
		prevDigit = isDigit
	}

	if len(result) == 0 {
		_ = fmt.Sprint(prevDigit, prevLower, prevUpper)
	}

	return string(result)
}

// ToUnderscore converts a string into snake_case format.
// It handles camelCase, PascalCase, and kebab-case.
func ToUnderscore(text string) string {
	if text == "" {
		return ""
	}

	// 1. Insert underscore before capital letters (camelCase/PascalCase support)
	t := CamelToSnake(text)

	// 2. Convert to lowercase
	t = strings.ToLower(t)

	// 3. Replace spaces and hyphens with a single underscore
	// Example: "hello-world thing" -> "hello_world_thing"
	t = reSeparator.ReplaceAllString(t, "_")

	// 4. Remove any multiple consecutive underscores resulting from the previous steps
	t = reMultipleUnderscores.ReplaceAllString(t, "_")

	// 5. Remove leading and trailing underscores
	t = strings.Trim(t, "_")

	return t
}

func ToVariable(s string) string {
	s = ToCamelCase(s)

	s = strings.ToLower(s[0:1]) + s[1:]

	return s
}

func ToTitle(s string) string {
	if s == "" {
		return ""
	}

	// Step 1: camelCase → snake_case
	s = ToUnderscore(s)

	// Step 2: collapse multiple underscores → single space
	s = reMultipleUnderscores.ReplaceAllString(s, " ")

	// Step 3: trim and split into words
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	words := strings.Split(s, " ")
	var titleWords []string

	for _, word := range words {
		if word == "" {
			continue
		}
		// Title case: first rune uppercase, rest unchanged
		runes := []rune(word)
		runes[0] = unicode.ToUpper(runes[0])
		titleWords = append(titleWords, string(runes))
	}

	return strings.Join(titleWords, " ")
}

// Compiled once: these run on hot paths (trigger lookups, URL building,
// tenant resolution), and compiling per call costs microseconds each time.
var (
	reSlugInvalid         = regexp.MustCompile(`[^a-z0-9]+`)
	reSeparator           = regexp.MustCompile(`[\s-]+`)
	reMultipleUnderscores = regexp.MustCompile(`_+`)
	reHttpScheme          = regexp.MustCompile(`http(s?)://`)
	reMainDomain          = regexp.MustCompile(`((.*)[.])?([a-zA-Z0-9-_]{3,}[.]([a-z]{2,3}([.][a-z]{2,})?))`)
	reEmail               = regexp.MustCompile(`^(([^<>()[\]\\.,;:\s@"]+(\.[^<>()[\]\\.,;:\s@"]+)*)|(".+"))@((\[[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\])|(([a-zA-Z\-0-9]+\.)+[a-zA-Z]{2,}))$`)
	rePhone               = regexp.MustCompile(`(?im)^[\+]?[(]?[0-9]{3}[)]?[-\s\.]?[0-9]{3}[-\s\.]?[0-9]{4,6}$`)
)

// Pluralization rules
var pluralRules = []struct {
	pattern *regexp.Regexp
	replace string
}{
	{regexp.MustCompile("([^aeiou])y$"), "${1}ies"},  // e.g., city → cities
	{regexp.MustCompile("(f|fe)$"), "ves"},           // e.g., knife → knives
	{regexp.MustCompile("(s|sh|ch|x|z)$"), "${1}es"}, // e.g., bus → buses, box → boxes
	{regexp.MustCompile("$"), "s"},                   // Default rule: add "s"
}

// Singularization rules
var singularRules = []struct {
	pattern *regexp.Regexp
	replace string
}{
	{regexp.MustCompile("ies$"), "y"},                // e.g., cities → city
	{regexp.MustCompile("ves$"), "f"},                // e.g., knives → knife
	{regexp.MustCompile("eases$"), "ease"},           // e.g., releases → release
	{regexp.MustCompile("(s|sh|ch|x|z)es$"), "${1}"}, // e.g., boxes → box
	{regexp.MustCompile("s$"), ""},                   // Default rule: remove "s"
}

// Pluralize converts a singular noun to its plural form
func Pluralize(word string) string {
	word = Singularize(word)

	for _, rule := range pluralRules {
		if rule.pattern.MatchString(word) {
			return rule.pattern.ReplaceAllString(word, rule.replace)
		}
	}
	return word
}

// Singularize converts a plural noun to its singular form
func Singularize(word string) string {
	for _, rule := range singularRules {
		if rule.pattern.MatchString(word) {
			return rule.pattern.ReplaceAllString(word, rule.replace)
		}
	}
	return word
}

func ReadFile(filename string) string {
	return LoadFile(filename)
}

// LoadFile reads a JSON file and converts it to a map
func LoadFile(filename string) string {
	filename = GetPath(filename)

	file, err := os.Open(filename)
	if err != nil {
		return ""
	}
	defer file.Close()

	bytes, err := io.ReadAll(file)
	if err != nil {
		return ""
	}

	return string(bytes)
}

// LoadJSONFile reads a JSON file and converts it to a map
func LoadJSONFile(filename string) (map[string]interface{}, error) {
	filename = GetPath(filename)

	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	bytes, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	var data map[string]interface{}
	if err := json.Unmarshal(bytes, &data); err != nil {
		return nil, err
	}

	return data, nil
}

func GetClientIP(req *http.Request) string {
	ip := req.Header.Get("x-forwarded-for")

	if IsEmpty(ip) {
		ip = req.Header.Get("x-real-ip")
	}

	if IsNotEmpty(ip) {
		ip = strings.Split(ip, ":")[0]
	}

	return ip
}

// GetLocalIP retrieves the first non-loopback local IP address
func GetLocalIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil { // Only return IPv4
				return ipNet.IP.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no local IP found")
}

// GetLocalIP retrieves the first non-loopback local IP address
func GetLocalIPS() ([]string, error) {
	ips := []string{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok {
			if ipNet.IP.To4() != nil { // Only return IPv4
				ips = append(ips, ipNet.IP.String())
			}
		}
	}
	if len(ips) > 0 {
		return ips, nil
	}

	return nil, fmt.Errorf("no local IP found")
}

// GetPublicIP fetches the external IP address from an API
func GetPublicIP() (string, error) {
	resp, err := http.Get("https://api64.ipify.org?format=text")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(ip), nil
}

// GetNetworkIP retrieves the local IP and calculates the network address
func GetNetworkIP() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range interfaces {
		if (iface.Flags&net.FlagUp) == 0 || (iface.Flags&net.FlagLoopback) != 0 {
			continue // Ignore down and loopback interfaces
		}

		addrs, err := iface.Addrs()
		if err != nil {
			return "", err
		}

		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				if ipNet.IP.To4() != nil { // Only consider IPv4
					networkIP := ipNet.IP.Mask(ipNet.Mask) // Calculate network address
					return networkIP.String(), nil
				}
			}
		}
	}

	return "", fmt.Errorf("no network IP found")
}

// writeCounter tracks download progress
type writeCounter struct {
	downloaded int64
	total      int64
	progress   func(downloaded, total int64)
}

func (wc *writeCounter) Write(p []byte) (int, error) {
	n := len(p)
	wc.downloaded += int64(n)
	if wc.progress != nil {
		wc.progress(wc.downloaded, wc.total)
	}
	return n, nil
}

// DownloadFile downloads a file from URL and saves it to destPath
// Supports:
//   - Large files (streaming)
//   - Progress callback
//   - Timeout
//   - Resume (optional, see note below)
//   - Automatic directory creation
func DownloadFile(url, destPath string, progress func(downloaded, total int64)) error {
	// Create directory if not exists
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Minute, // Adjust as needed
	}

	// HEAD request to get file size (optional but useful)
	var totalSize int64
	headResp, err := client.Head(url)
	if err != nil {
		return fmt.Errorf("HEAD request failed: %w", err)
	}
	defer headResp.Body.Close()

	if headResp.StatusCode != http.StatusOK {
		return fmt.Errorf("HEAD request failed with status: %s", headResp.Status)
	}

	if cl := headResp.Header.Get("Content-Length"); cl != "" {
		if size, err := strconv.ParseInt(cl, 10, 64); err == nil {
			totalSize = size
		}
	}

	// GET request to download
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("GET request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	// Create output file
	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer out.Close()

	// Stream with progress
	counter := &writeCounter{
		total:    totalSize,
		progress: progress,
	}
	if _, err = io.Copy(out, io.TeeReader(resp.Body, counter)); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	return nil
}

// FileExists checks if a file exists
func FileExists(filename string) bool {
	_, err := os.Stat(filename)

	if err != nil {
		// console.Error("FileExists", err)
		return false
	}

	return !os.IsNotExist(err)
}

// IsNumeric checks if a value is an int, float, or a numeric string
func IsNumeric(value interface{}) bool {
	switch v := value.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true // Directly numeric types

	case string:
		_, err := strconv.ParseFloat(v, 64)
		return err == nil // Returns true if string is a valid number

	default:
		return false // Not numeric
	}
}

func ConvertCalculatedValue(value interface{}) interface{} {
	if IsNumeric(value) {
		if v, ok := value.(string); ok {
			vi, _ := strconv.ParseFloat(v, 64)
			return vi
		}

		return value
	}

	return GetTimestamp(value)
}

func GetTimestamp(value interface{}) time.Time {
	result := StringToDatetime(value)

	if result != nil {
		return (*result).UTC()
	}

	return time.Now().UTC()
}

func GetTimestampString(value interface{}) string {
	return GetTimestamp(value).Format(time.RFC3339)
}

func ToTimestampString(value interface{}, layout string) time.Time {
	if IsEmpty(layout) {
		layout = "2006"
	}
	if v, ok := value.(string); ok {
		parsedTime, _ := time.Parse(layout, v)

		return parsedTime.UTC()
	} else if v, ok := value.(time.Time); ok {
		return v
	}

	return time.Now().UTC()
}

func StringToDatetime(value interface{}) *time.Time {
	if strValue, ok := value.(string); ok {
		var t time.Time
		var err error

		t, err = time.Parse(time.DateOnly, strValue)
		if err == nil {
			return &t
		}

		t, err = time.Parse(time.DateTime, strValue)
		if err == nil {
			return &t
		}

		t, err = time.Parse(time.UnixDate, strValue)
		if err == nil {
			return &t
		}

		t, err = time.Parse(time.RFC3339, strValue)
		if err == nil {
			return &t
		}

		t, err = time.Parse(time.RFC822, strValue)
		if err == nil {
			return &t
		}

		t, err = time.Parse(time.TimeOnly, strValue)
		if err == nil {
			return &t
		}

		t, err = time.Parse(time.RFC850, strValue)
		if err == nil {
			return &t
		}

		t, err = time.Parse(time.UnixDate, strValue)
		if err == nil {
			return &t
		}

		ISO_8601 := "2006-01-02T15:04:05Z07:00"
		RFC_1123Z := "Tue Dec 30 2025 11:00:59 GMT+0300 (East Africa Time)"

		t, err = time.Parse(ISO_8601, strValue)
		if err == nil {
			return &t
		}
		// 		console.Log("ISO_8601", strValue, t, err)
		t, err = time.Parse(RFC_1123Z, strValue)
		if err == nil {
			return &t
		}

		// Try custom date parsing as a last resort
		t, err = DateParse(strValue, GetTimestamp(nil))
		// console.Log("DateParse", strValue, t, err)
		if err == nil {
			return &t
		}
	} else if strValue, ok := value.(time.Time); ok {
		return &strValue
	} else if strValue, ok := value.(bson.DateTime); ok {
		t := strValue.Time()

		return &t
	}

	return nil
}

func StringToTimeOnly(value interface{}) *time.Time {
	if strValue, ok := value.(string); ok {
		var t time.Time
		var err error

		t, err = time.Parse(time.TimeOnly, strValue)
		if err == nil {
			return &t
		}

		t, err = time.Parse(time.Kitchen, strValue)
		if err == nil {
			return &t
		}

	} else if strValue, ok := value.(time.Time); ok {
		return &strValue
	}

	return nil
}

func Yesterday() time.Time {
	t := time.Now().Add(time.Hour * -24)
	result := StringToDatetime(t.Format(time.DateOnly) + " 00:00:00")
	if result != nil {
		return *result
	}

	return t.UTC()
}

func YesterdayEnd() time.Time {
	t := time.Now().Add(time.Hour * -24)
	result := StringToDatetime(t.Format(time.DateOnly) + " 23:59:59")
	if result != nil {
		return *result
	}

	return t.UTC()
}

func Today() time.Time {
	t := time.Now()
	result := StringToDatetime(t.Format(time.DateOnly) + " 00:00:00")
	if result != nil {
		return *result
	}

	return t.UTC()
}

func TodayEnd() time.Time {
	t := time.Now()
	result := StringToDatetime(t.Format(time.DateOnly) + " 23:59:59")
	if result != nil {
		return *result
	}

	return t.UTC()
}

func Tomorrow() time.Time {
	t := time.Now().Add(time.Hour * 24)
	result := StringToDatetime(t.Format(time.DateOnly) + " 00:00:00")
	if result != nil {
		return *result
	}

	return t.UTC()
}

func TomorrowEnd() time.Time {
	t := time.Now().Add(time.Hour * 24)
	result := StringToDatetime(t.Format(time.DateOnly) + " 23:59:59")
	if result != nil {
		return *result
	}

	return t.UTC()
}

func DateStart(value interface{}) time.Time {
	t := StringToDatetime(value)
	if t == nil {
		_t := time.Now()
		t = &_t
	}

	result := StringToDatetime(t.Format(time.DateOnly) + " 00:00:00")

	if result != nil {
		return *result
	}

	return t.UTC()
}

func DateEnd(value interface{}) time.Time {
	t := StringToDatetime(value)
	if t == nil {
		_t := time.Now()
		t = &_t
	}
	result := StringToDatetime(t.Format(time.DateOnly) + " 23:59:59")

	if result != nil {
		return *result
	}

	return t.UTC()
}

func HourStart(value interface{}) time.Time {
	t := StringToDatetime(value)
	if t == nil {
		_t := time.Now()
		t = &_t
	}

	format := "2006-01-02 15"
	result := StringToDatetime(t.Format(format) + ":00:00")

	if result != nil {
		return *result
	}

	return t.UTC()
}

func HourEnd(value interface{}) time.Time {
	t := StringToDatetime(value)
	if t == nil {
		_t := time.Now()
		t = &_t
	}

	format := "2006-01-02 15"
	result := StringToDatetime(t.Format(format) + ":59:59")

	if result != nil {
		return *result
	}

	return t.UTC()
}

func WeekStart(value interface{}) time.Time {
	t := StringToDatetime(value)
	if t == nil {
		_t := time.Now()
		t = &_t
	}

	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7 // Sunday -> 7
	}

	startOfWeek := time.Date(
		t.Year(),
		t.Month(),
		t.Day()-weekday+1,
		0, 0, 0, 0,
		t.Location(),
	)
	result := StringToDatetime(startOfWeek)

	if result != nil {
		return *result
	}

	return t.UTC()
}

func WeekEnd(value interface{}) time.Time {
	t := StringToDatetime(value)
	if t == nil {
		_t := time.Now()
		t = &_t
	}

	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7
	}

	// Sunday = last day of ISO week
	lastDayOfWeek := time.Date(
		t.Year(),
		t.Month(),
		t.Day()+(7-weekday),
		0, 0, 0, 0,
		t.Location(),
	)
	result := StringToDatetime(lastDayOfWeek.Format(time.DateOnly) + " 23:59:59")

	if result != nil {
		return *result
	}

	return t.UTC()
}

func MonthStart(value interface{}) time.Time {
	t := StringToDatetime(value)
	if t == nil {
		_t := time.Now()
		t = &_t
	}

	format := "2006-01"
	result := StringToDatetime(t.Format(format) + "-01 00:00:00")

	if result != nil {
		return *result
	}

	return t.UTC()
}

func MonthEnd(value interface{}) time.Time {
	t := StringToDatetime(value)
	if t == nil {
		_t := time.Now()
		t = &_t
	}

	_t := time.Date(
		t.Year(),
		t.Month()+1,
		0, // day 0 = last day of previous month
		0, 0, 0, 0,
		t.Location(),
	)
	t = &_t

	result := StringToDatetime(t)

	if result != nil {
		return *result
	}

	return t.UTC()
}

func ObjectID(id interface{}) bson.ObjectID {
	var value bson.ObjectID

	// console.Warn("id.before", id)

	if v, ok := id.(string); ok {
		value, _ = bson.ObjectIDFromHex(v)
	} else if v, ok := id.(int); ok {
		value, _ = bson.ObjectIDFromHex(fmt.Sprint(v))
	} else if v, ok := id.(bson.ObjectID); ok {
		value = v
	} else {
		value = bson.NewObjectID()
	}

	// console.Warn("id.after", value)

	return value
}

func ObjectIDtoString(id bson.ObjectID) string {
	value := id.Hex()

	return value
}

func StringLength(value string) int {
	return utf8.RuneCountInString(value)
}

func GetChildRelativeName(parent string, collection string, primaryKey string, foreignKey string) string {
	var classVariable = ToVariable(collection)
	var testKey = ToUnderscore(foreignKey)

	if strings.HasSuffix(testKey, "_id") {
		newVariable := ToVariable(testKey[0:(StringLength(testKey) - 3)])
		classVariable = ToVariable(collection)

		if ToVariable(parent) != ToVariable(newVariable) {
			classVariable = ToVariable(newVariable + "_" + collection)
		}
	} else {
		classVariable = ToVariable(testKey + "_" + collection)
	}

	classVariable = Pluralize(classVariable)

	return classVariable
}

func GetParentRelativeName(collection string, primaryKey string, foreignKey string) string {

	var classVariable = ToVariable(foreignKey)
	var testKey = ToUnderscore(foreignKey)

	if strings.HasSuffix(testKey, "_id") {
		classVariable = ToVariable(testKey[0:(StringLength(testKey) - 3)])
	}

	if classVariable == foreignKey || classVariable == Singularize(foreignKey) {
		classVariable = ToVariable(ToUnderscore(foreignKey) + "_info")
	}

	return classVariable
}

func TrackTime(start *time.Time, name string) {
	elapsed := time.Since(*start)
	fmt.Printf("%s took %s\n", name, elapsed)
	*start = time.Now()
}

func HomeDirectory(name string) string {
	dir, err := os.UserHomeDir()
	if err != nil {
		logger.Error("HomeDirectory", err.Error())
	}

	if dir == "/" || IsEmpty(dir) {
		dir = "/root"
	}

	appDir := dir + string(os.PathSeparator) + ".yekonga-server" + string(os.PathSeparator) + name

	if info, err := os.Stat(appDir); err != nil {
		if info != nil && !info.IsDir() {
			os.MkdirAll(appDir, 0755)
		} else {
			err := os.MkdirAll(appDir, 0755)
			if err != nil {
				console.Error("HomeDirectory", appDir, err)
			}
		}
	}

	return appDir
}

func GetBaseUrl(str string, domain string) string {
	if reHttpScheme.MatchString(str) {
		return str
	}

	port := 80
	prefix := ""
	if config.Config != nil {
		port = config.Config.Ports.Server
		prefix = config.Config.BaseUrl
	}

	// Only look up the local IP when there's no domain to use: it hits the
	// network interfaces, and this runs once per file field per row.
	if domain == "" {
		ip, _ := GetLocalIP()
		domain = fmt.Sprintf("%s:%d", ip, port)
	}

	return "https://" + domain + strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(strings.TrimSuffix(str, "/"), "/")
}

func GetMainDomain(value string) *string {
	matches := reMainDomain.FindStringSubmatch(value)

	// matches[3] corresponds to the "domain" group in your JS regex
	if len(matches) > 3 && matches[3] != "" {
		domain := matches[3]
		return &domain
	}

	return nil
}

// GetDirectoryPath returns the absolute path of the specified file or executable
func GetDirectoryPath() string {
	// Get the absolute path of the executable
	exePath, err := os.Executable()
	if err != nil {
		return ""
	}
	// Resolve the absolute path
	absPath, err := filepath.Abs(exePath)
	if err != nil {
		return ""
	}
	return absPath
}

// GetPublicPath returns the absolute path of a frontend file (e.g., index.html)
func GetPublicPath() (string, error) {
	// Assuming the frontend files are in the 'frontend/dist' directory
	// Adjust the path based on your project structure
	frontendPath := filepath.Join("public")
	absPath, err := filepath.Abs(frontendPath)
	if err != nil {
		return "", err
	}
	return absPath, nil
}

func GetPath(relativePath string) string {
	// 1. Get the path of the executable
	ex, err := os.Executable()
	if err != nil {
		log.Fatalf("Error getting executable path: %v", err)
	}

	// 2. Get the directory of the executable
	exPath := filepath.Dir(ex)

	// 3. Join the executable's directory with the relative path
	absolutePath := filepath.Join(exPath, relativePath)

	if FileExists(absolutePath) {
		return absolutePath
	}

	if filepath.IsAbs(relativePath) {
		if FileExists(relativePath) {
			return relativePath
		}
	}

	if FileExists(relativePath) {
		absPath, err := filepath.Abs(relativePath)
		if err != nil {
			return relativePath
		}
		return absPath
	}

	return absolutePath
}

func GetValueOfString(data interface{}, key string) string {
	return GetMapString(data, key)
}

func GetMapString(data interface{}, key string) string {
	if value, ok := GetMapValue(data, key).(string); ok {
		return value
	} else if value, ok := GetMapValue(data, key).(bson.ObjectID); ok {
		return ObjectIDtoString(value)
	}

	return ""
}

func GetValueOfInt(data interface{}, key string) int {
	return GetMapInt(data, key)
}

func GetMapInt(data interface{}, key string) int {
	v := GetMapValue(data, key)

	if value, ok := v.(int); ok {
		return value
	} else if IsNumeric(v) {
		return ToInt(v)
	}

	return 0
}

func GetValueOfFloat(data interface{}, key string) float64 {
	return GetMapFloat(data, key)
}

func GetMapFloat(data interface{}, key string) float64 {
	v := GetMapValue(data, key)
	if value, ok := v.(int); ok {
		return ToFloat(value)
	} else if value, ok := v.(float64); ok {
		return value
	} else if IsNumeric(v) {
		return ToFloat64(v)
	}

	return 0
}

func GetValueOfBoolean(data interface{}, key string) bool {
	return GetMapBoolean(data, key)

}

func GetMapBoolean(data interface{}, key string) bool {
	if value, ok := GetMapValue(data, key).(bool); ok {
		return value
	}

	return false
}

func GetValueOfDate(data interface{}, key string) time.Time {
	return GetMapDate(data, key)
}

func GetMapDate(data interface{}, key string) time.Time {
	value := GetMapValue(data, key)

	return GetTimestamp(value)
}

func GetValueOfMap(data interface{}, key string) map[string]interface{} {
	return GetMap(data, key)
}

func GetMap(data interface{}, key string) map[string]interface{} {
	v := GetMapValue(data, key)

	if IsNotEmpty(v) {
		return ToMap[interface{}](v)
	}

	return nil
}

func GetValueOf(data interface{}, key string) interface{} {
	return GetMapValue(data, key)
}

func GetValueOfList(data interface{}, key string) []interface{} {
	if value, ok := GetMapValue(data, key).([]interface{}); ok {
		return value
	}

	return []interface{}{}
}

func GetMapValue(data interface{}, key string) interface{} {
	// Plain maps are read directly. The generic path below copies the whole
	// map via reflection just to read one key, and this runs hundreds of
	// times per request.
	var m map[string]interface{}
	switch x := data.(type) {
	case map[string]interface{}:
		m = x
	case datatype.DataMap:
		m = x
	case *datatype.DataMap:
		if x == nil {
			return nil
		}
		m = *x
	case *map[string]interface{}:
		if x == nil {
			return nil
		}
		m = *x
	}

	if m != nil {
		first, rest, nested := strings.Cut(key, ".")

		if vi, ok := m[first]; ok {
			if !nested {
				return vi
			}
			return GetMapValue(vi, rest)
		}

		return nil
	}

	if str, ok := data.(string); ok {
		var d map[string]interface{}
		if err := json.Unmarshal([]byte(str), &d); err == nil {
			data = d
		} else {
			console.Info(d, err.Error())
			console.Info(data)
		}
	}

	keys := strings.Split(key, ".")
	first := keys[0]
	var localData interface{}

	if IsNotEmpty(data) {
		if IsPointer(data) {
			v := reflect.ValueOf(data).Elem()
			if v.IsValid() {
				localData = v.Interface()
			}
		} else {
			localData = data
		}
	}

	if IsMap(localData) {
		v := ToDataMap(localData)

		if vi, oki := v[first]; oki {
			if len(keys[1:]) == 0 {
				return vi
			} else {
				last := strings.Join(keys[1:], ".")
				return GetMapValue(vi, last)
			}
		}
	} else if v, ok := localData.([]interface{}); ok {
		if IsNumeric(first) {
			pos := ToInt(first)

			if vi := v[pos]; vi != nil {
				if len(keys[1:]) == 0 {
					return vi
				} else {
					last := strings.Join(keys[1:], ".")
					return GetMapValue(vi, last)
				}
			}
		}
	}

	return nil
}

func GetMapArray(data interface{}, source string, keys map[string]string) []interface{} {
	list := []interface{}{}
	dataList := GetMapValue(data, source)

	if v, ok := dataList.([]interface{}); ok {
		for _, vi := range v {
			data := map[string]interface{}{}
			for kii, vii := range keys {
				data[kii] = GetMapValue(vi, vii)
			}

			list = append(list, data)
		}
	}

	return list
}

func GetFirst(data interface{}) interface{} {
	if value, ok := data.(map[string]interface{}); ok {
		for _, v := range value {
			return v
		}
	} else if value, ok := data.([]interface{}); ok {
		for _, v := range value {
			return v
		}
	} else if value, ok := data.([]map[string]interface{}); ok {
		for _, v := range value {
			return v
		}
	}

	return nil
}

func TypeOf(data interface{}) string {
	return fmt.Sprintf("%T", data)
}

func GetType(data interface{}) string {
	return fmt.Sprintf("%T", data)
}

func IsSliceOfMapStringInterface(v interface{}) bool {
	t := reflect.TypeOf(v)
	return t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Map &&
		t.Elem().Key().Kind() == reflect.String && t.Elem().Elem().Kind() == reflect.Interface
}

func GetList(data interface{}, key string) []interface{} {
	var source interface{}
	list := []interface{}{}

	if IsPointer(data) {
		source = reflect.ValueOf(data).Elem().Interface()
	} else {
		source = data
	}

	if source != nil {
		// Use reflection to check if source is a slice
		converted := ToMapList[interface{}](source)

		for _, vi := range converted {
			if vii, okii := vi[key]; okii {
				list = append(list, vii)
			}
		}
	}

	if len(list) == 0 {
		logger.Error("GetList result", list)
	}

	return list
}

func IsPointer(v interface{}) bool {
	if v == nil {
		return false
	}

	return reflect.TypeOf(v).Kind() == reflect.Ptr
}

func CreateFile(data interface{}, filename string) error {
	return SaveToFile(data, filename)
}

func SaveToFile(data interface{}, filename string) error {
	var (
		rowData []byte
		err     error
	)
	// Convert to JSON
	if d, ok := data.(string); ok {
		rowData = []byte(d)
	} else {
		rowData, err = json.MarshalIndent(data, "", "  ") // Pretty print with indentation
		if err != nil {
			return err
		}
	}

	// Extract directory path
	dir := filepath.Dir(filename)

	// Create all folders if they don't exist
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Write to file
	err = os.WriteFile(filename, rowData, 0644) // 0644 is standard file permission
	if err != nil {
		return err
	}

	return nil
}

func CreateDirectory(dir string) error {
	// Create all folders if they don't exist
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return nil
}

func CreateFolder(dir string) error {
	return CreateDirectory(dir)
}

func ExtractGraphqlQuery(data interface{}, level uint) map[uint][]string {
	var result map[uint][]string = map[uint][]string{}
	if d, ok := data.(map[string]interface{}); ok {
		for k := range d {
			var list interface{}
			if k == "Definitions" {
				list = GetMapValue(data, "Definitions")
			} else if k == "SelectionSet" {
				list = GetMapValue(data, "SelectionSet.Selections")
			} else {

			}
			if result[level] == nil {
				result[level] = []string{}
			}

			if l, ok := list.([]interface{}); ok {
				for _, vi := range l {
					newLevel := level + 1
					vn := GetMapValue(vi, "Name.Value")
					if vn == nil {
						newLevel = level
					}

					rs := ExtractGraphqlQuery(vi, newLevel)

					if vii, ok := vn.(string); ok {
						if len(rs[level+1]) > 0 {
							result[level] = append(result[level], "_c_"+vii)
							result[level+1] = append(result[level+1], "_p_"+vii)
						} else {
							result[level] = append(result[level], vii)
						}
					}

					if result[level+1] == nil {
						result[level+1] = []string{}
					}

					for k, v := range rs {
						if len(v) > 0 {
							result[k] = append(result[k], v...)
						}
					}
				}

			}
		}
	} else if d, ok := data.([]interface{}); ok {
		count := len(d)
		for i := 0; i < count; i++ {
			v := d[i]
			logger.Error("3", v)
		}
	}

	return result
}

func Get(url string, headers map[string]string) (status int, responseBody string, err error) {
	return GetRequest(url, headers)
}

// GetRequest performs an HTTP GET request with the specified URL, headers, and optional body.
func GetRequest(url string, headers map[string]string) (status int, responseBody string, err error) {
	// Create a new HTTP client
	client := &http.Client{}

	// Create the request with optional body
	req, err := http.NewRequest("GET", url, nil)

	if err != nil {
		return 0, "", fmt.Errorf("error creating request: %v", err)
	}

	// Add headers
	for key, value := range headers {
		req.Header.Add(key, value)
	}

	// Make the GET request
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("error making request: %v", err)
	}
	defer resp.Body.Close()

	// Read the response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", fmt.Errorf("error reading response: %v", err)
	}

	return resp.StatusCode, string(respBody), nil
}

func Post(url string, headers map[string]string, body interface{}) (status int, responseBody string, err error) {
	return Request("POST", url, headers, body)
}

func Put(url string, headers map[string]string, body interface{}) (status int, responseBody string, err error) {
	return Request("PUT", url, headers, body)
}

func Patch(url string, headers map[string]string, body interface{}) (status int, responseBody string, err error) {
	return Request("PATCH", url, headers, body)
}

func Delete(url string, headers map[string]string, body interface{}) (status int, responseBody string, err error) {
	return Request("DELETE", url, headers, body)
}

func Request(method string, url string, headers map[string]string, body interface{}) (status int, responseBody string, err error) {
	// Create a new HTTP client
	client := &http.Client{}
	// console.Log("PostRequest", url, headers, body)

	// Serialize body to JSON
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return 0, "", fmt.Errorf("error marshaling body: %v", err)
	}

	// Create the request with the serialized body
	req, err := http.NewRequest(strings.ToUpper(method), url, bytes.NewReader(bodyBytes))
	if err != nil {
		return 0, "", fmt.Errorf("error creating request: %v", err)
	}

	// Add headers
	for key, value := range headers {
		req.Header.Add(key, value)
	}

	// Make the POST request
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("error making request: %v", err)
	}
	defer resp.Body.Close()

	// Read the response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", fmt.Errorf("error reading response: %v", err)
	}

	return resp.StatusCode, string(respBody), nil
}

func ValidateEmail(email interface{}) bool {
	if v, ok := email.(string); ok {
		return reEmail.MatchString(strings.ToLower(v))
	}

	return false
}

func IsEmail(email interface{}) bool {
	return ValidateEmail(email)
}

func IsPhone(value interface{}) bool {
	if v, ok := value.(string); ok {
		if v == "" {
			return false
		}
		v = FormatPhone(v) // Assuming formatPhone is defined elsewhere

		return rePhone.MatchString(v)

	}

	return false
}

// modifyString is a placeholder implementation; adjust based on your needs
func ModifyString(value string) string {
	// Example: Randomly shuffle characters (if that's what modifyString does)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	runes := []rune(value)
	r.Shuffle(len(runes), func(i, j int) {
		runes[i], runes[j] = runes[j], runes[i]
	})
	return string(runes)
}

func FormatPhone(phone interface{}) string {
	if value, ok := phone.(string); ok && IsNotEmpty(value) {
		value = strings.ReplaceAll(value, " ", "")
		value = strings.ReplaceAll(value, "-", "")
		value = strings.ReplaceAll(value, "_", "")
		value = strings.ReplaceAll(value, ".", "")
		value = strings.ReplaceAll(value, ",", "")

		if strings.HasPrefix(value, "+") {
			value = value[1:]
		} else if strings.HasPrefix(value, "255") {
			// No change needed
		} else if strings.HasPrefix(value, "0") && len(value) == 10 {
			value = "255" + value[1:]
		} else if !strings.HasPrefix(value, "0") && len(value) == 9 {
			value = "255" + value
		}

		if len(value) < 10 || (strings.HasPrefix(value, "255") && len(value) != 12) {
			return ""
		}

		return value
	}

	return ""
}

func PhoneFormat(phone interface{}) string {
	return FormatPhone(phone)
}

func GetRandomString(length int, mode string) string {
	var chars string
	n := "0123456789" + fmt.Sprintf("%d", time.Now().UnixMilli())
	l := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	h := "ABCDEFabcdef"

	switch mode {
	case "number":
		chars = n
	case "letter":
		chars = l
	case "hex":
		chars = n + h
	default:
		chars = n + l
	}

	result := make([]byte, length)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	for i := 0; i < length; i++ {
		// JavaScript logic: for 'number' type, avoid leading zero
		if mode == "number" && i == 0 && len(chars) > 1 {
			// Select from chars[1:] to skip '0'
			result[i] = chars[1+r.Intn(len(chars)-1)]
		} else {
			result[i] = chars[r.Intn(len(chars))]
		}
	}

	return string(result)
}

func GetRandomInt(length int) string {
	return GetRandomString(length, "number")
}

func GetHexString(length int) string {
	return GetRandomString(length, "hex")
}

func HashRefreshToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// ExtractDomain extracts the domain from a URL string
func ExtractDomain(input string) string {
	// Add scheme if missing
	if !strings.HasPrefix(input, "http://") && !strings.HasPrefix(input, "https://") {
		input = "https://" + input
	}

	// Parse the URL
	u, err := url.Parse(input)
	if err != nil {
		return ""
	}

	return u.Host
}

func RemoveFile(filePath string) {
	err := os.Remove(filePath)

	if err != nil {
		console.Error("Error.removeFile", err.Error())
	}
}

func IsProduction() bool {
	return os.Getenv("APP_ENV") == "production" ||
		os.Getenv("GIN_MODE") == "release" ||
		os.Getenv("ENV") == "prod"
}

func IsDevelopment() bool {
	return !IsProduction()
}

func GenerateQR(content string, outputPath interface{}) error {
	return GenerateQRWithIcon(content, "", outputPath)
}

func GenerateQRWithIcon(content string, iconPath string, outputPath interface{}) error {
	size := 512
	padding := 10
	// 1. Generate QR Code
	// Use High recovery level (qrcode.High) because the icon will obstruct part of the data.
	qr, err := qrcode.New(content, qrcode.High)
	if err != nil {
		return err
	}
	// Remove default border
	qr.DisableBorder = true

	// Create the QR image (e.g., 512x512)
	qrImg := qr.Image(size)

	// 3. Create the Final Canvas
	// finalImg := image.NewRGBA(qrImg.Bounds())
	// draw.Draw(finalImg, qrImg.Bounds(), qrImg, image.Point{}, draw.Src)

	// Custom padding

	// New image with padding
	newSize := size + (padding * 2)
	finalImg := image.NewRGBA(image.Rect(0, 0, newSize, newSize))

	// White background
	draw.Draw(finalImg, finalImg.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)

	// Draw QR code with padding offset
	draw.Draw(
		finalImg,
		image.Rect(padding, padding, padding+size, padding+size),
		qrImg,
		image.Point{},
		draw.Over,
	)

	if IsNotEmpty(iconPath) {
		// 2. Resize Icon (Icon should be ~15-20% of the QR code size)
		iconSize := uint(qrImg.Bounds().Dx() / 5)

		// Calculate center position
		offset := image.Pt(
			(finalImg.Bounds().Dx()-int(iconSize))/2,
			(finalImg.Bounds().Dy()-int(iconSize))/2,
		)

		// 4. Open the Favicon/Icon
		file, err := os.Open(iconPath)
		if err != nil {
			return err
		}
		defer file.Close()

		iconImg, _, err := image.Decode(file)
		if err != nil {
			return err
		}
		// Draw a white background for the icon
		whiteRect := image.Rect(0, 0, int(iconSize)+16, int(iconSize)+16)
		whiteImg := image.NewUniform(image.White)
		whiteOffset := offset.Sub(image.Pt(8, 8))
		draw.Draw(finalImg, whiteRect.Add(whiteOffset), whiteImg, image.Point{}, draw.Src)

		// Using nfnt/resize for high-quality scaling; or use stdlib image.Draw
		scaledIcon := resize.Resize(iconSize, iconSize, iconImg, resize.Lanczos3)
		// 5. Overlay Icon
		draw.Draw(finalImg, scaledIcon.Bounds().Add(offset), scaledIcon, image.Point{}, draw.Over)
	}

	if IsEmpty(outputPath) {
		outputPath = GetPath("./" + UUID() + ".png")
	}

	output := ToString(outputPath)
	os.MkdirAll(filepath.Dir(output), os.ModePerm)

	// 6. Save to file
	out, err := os.Create(output)
	if err != nil {
		console.Warn("Create", output, err.Error())
		return err
	}
	defer out.Close()

	return png.Encode(out, finalImg)
}

func ToBase64Image(filePath string) string {
	if strings.HasPrefix(filePath, "http://") || strings.HasPrefix(filePath, "https://") {
		fileDist := GetPath("./tmp/" + UUID() + path.Ext(filePath))
		DownloadFile(filePath, fileDist, func(downloaded int64, total int64) {})
		defer RemoveFile(fileDist)

		filePath = fileDist
	}

	// Read file
	data, err := os.ReadFile(filePath)
	if err != nil {
		panic(err)
	}

	// Detect mime type from extension
	ext := filepath.Ext(filePath)

	mimeType := "image/png"
	switch ext {
	case ".jpg", ".jpeg":
		mimeType = "image/jpeg"
	case ".gif":
		mimeType = "image/gif"
	case ".svg":
		mimeType = "image/svg+xml"
	case ".webp":
		mimeType = "image/webp"
	}

	// Convert to base64
	base64Data := base64.StdEncoding.EncodeToString(data)

	// Create data URL
	dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data)

	return dataURL
}

func Matches(text string, pattern string) bool {
	return MatchPattern(text, pattern)
}

func MatchPattern(text string, pattern string) bool {
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(text)

	if matches == nil {
		return false
	}

	return true
}

func MatchPath(route string, pattern string) bool {
	matched, err := path.Match(pattern, route)
	if err == nil && matched {
		return true
	}

	return false
}
