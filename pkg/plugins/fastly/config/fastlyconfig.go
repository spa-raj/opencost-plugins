package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type FastlyConfig struct {
	FastlyAPIKey string `json:"fastly_api_key"`
	LogLevel     string `json:"log_level"`
}

func GetFastlyConfig(configFilePath string) (*FastlyConfig, error) {
	var result FastlyConfig
	bytes, err := os.ReadFile(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("error reading config file for Fastly config @ %s: %v", configFilePath, err)
	}
	err = json.Unmarshal(bytes, &result)
	if err != nil {
		return nil, fmt.Errorf("error marshaling json into Fastly config: %v", err)
	}

	// Set default log level if not specified
	if result.LogLevel == "" {
		result.LogLevel = "info"
	}

	// Validate required fields
	if result.FastlyAPIKey == "" {
		return nil, fmt.Errorf("Fastly API key is required but not provided in config")
	}

	return &result, nil
}