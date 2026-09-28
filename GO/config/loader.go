package config

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

var Config *YekongaConfig

func NewYekongaConfig(file string) *YekongaConfig {
	// config holds the Yekonga configuration parsed from the provided file.
	var config YekongaConfig
	if !fileExists(file) {
		file = getPath(file)
	}

	data, err := loadJSONFile(file)
	if err != nil {
		fmt.Println(err)
	}
	Config = &config

	json.Unmarshal(toByte(data), Config)

	return Config
}

func SetYekongaConfig(c *YekongaConfig) {
	Config = c
}

func toByte(data interface{}) []byte {
	jsonData, _ := json.Marshal(data)

	return jsonData
}

func loadJSONFile(filename string) (map[string]interface{}, error) {
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

// fileExists checks if a file exists
func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	return !os.IsNotExist(err)
}

func getPath(relativePath string) string {
	ex, err := os.Executable()
	if err != nil {
		log.Fatalf("Error getting executable path: %v", err)
	}

	// 2. Get the directory of the executable
	exPath := filepath.Dir(ex)

	// 3. Join the executable's directory with the relative path
	absolutePath := filepath.Join(exPath, relativePath)

	if err != nil {
		log.Fatalf("Error getting absolute path: %v", err)
	}

	return absolutePath
}
