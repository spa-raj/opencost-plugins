package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetFastlyConfig(t *testing.T) {
	// Arrange
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	configData := `{
        "fastly_api_key": "test-api-key",
        "log_level": "debug"
    }`
	err := os.WriteFile(configPath, []byte(configData), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	// Act
	config, err := GetFastlyConfig(configPath)

	// Assert
	if err != nil {
		t.Fatalf("GetFastlyConfig returned error: %v", err)
	}
	if config == nil {
		t.Fatal("Expected config to be non-nil")
	}
	if config.FastlyAPIKey != "test-api-key" {
		t.Errorf("Expected API key 'test-api-key', got '%s'", config.FastlyAPIKey)
	}
	if config.LogLevel != "debug" {
		t.Errorf("Expected log level 'debug', got '%s'", config.LogLevel)
	}
}

func TestGetFastlyConfigDefaultLogLevel(t *testing.T) {
	// Arrange
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	configData := `{
        "fastly_api_key": "test-api-key"
    }`
	err := os.WriteFile(configPath, []byte(configData), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	// Act
	config, err := GetFastlyConfig(configPath)

	// Assert
	if err != nil {
		t.Fatalf("GetFastlyConfig returned error: %v", err)
	}
	if config.LogLevel != "info" {
		t.Errorf("Expected default log level 'info', got '%s'", config.LogLevel)
	}
}

func TestGetFastlyConfigInvalidPath(t *testing.T) {
	// Arrange
	configPath := "/nonexistent/path/to/config.json"

	// Act
	config, err := GetFastlyConfig(configPath)

	// Assert
	if err == nil {
		t.Error("Expected error for nonexistent config file, got nil")
	}
	if config != nil {
		t.Error("Expected nil config for invalid path")
	}
}

func TestGetFastlyConfigInvalidJSON(t *testing.T) {
	// Arrange
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	invalidJSON := `{ invalid json }`
	err := os.WriteFile(configPath, []byte(invalidJSON), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	// Act
	config, err := GetFastlyConfig(configPath)

	// Assert
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
	if config != nil {
		t.Error("Expected nil config for invalid JSON")
	}
}

func TestGetFastlyConfigMissingAPIKey(t *testing.T) {
	// Arrange
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	configData := `{
        "log_level": "debug"
    }`
	err := os.WriteFile(configPath, []byte(configData), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	// Act
	config, err := GetFastlyConfig(configPath)

	// Assert
	if err == nil {
		t.Error("Expected error for missing API key, got nil")
	}
	if config != nil {
		t.Error("Expected nil config when API key is missing")
	}
	if err != nil && err.Error() != "Fastly API key is required but not provided in config" {
		t.Errorf("Expected specific error message, got: %v", err)
	}
}

func TestGetFastlyConfigEmptyAPIKey(t *testing.T) {
	// Arrange
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	configData := `{
        "fastly_api_key": "",
        "log_level": "debug"
    }`
	err := os.WriteFile(configPath, []byte(configData), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	// Act
	config, err := GetFastlyConfig(configPath)

	// Assert
	if err == nil {
		t.Error("Expected error for empty API key, got nil")
	}
	if config != nil {
		t.Error("Expected nil config when API key is empty")
	}
}