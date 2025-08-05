package plugin

import "fmt"

// FastlyConfig represents the configuration needed to authenticate with Fastly API
type FastlyConfig struct {
	// FastlyAPIToken is the API token for authenticating with Fastly
	FastlyAPIToken string `json:"fastly_api_token"`

	// FastlyAccountID is the account ID for the Fastly account (optional)
	FastlyAccountID string `json:"fastly_account_id,omitempty"`

	// LogLevel controls the verbosity of logging
	LogLevel string `json:"log_level,omitempty"`

	// RateLimitPerSecond controls API request rate limiting
	RateLimitPerSecond float64 `json:"rate_limit_per_second,omitempty"`

	// EnableUsageDetail enables fetching detailed usage data for cost allocation
	EnableUsageDetail bool `json:"enable_usage_detail,omitempty"`

	// ServiceFilters limits cost retrieval to specific services
	ServiceFilters []string `json:"service_filters,omitempty"`

	// ExcludeServices excludes specific services from cost retrieval
	ExcludeServices []string `json:"exclude_services,omitempty"`
}

// Validate checks if the configuration is valid
func (c *FastlyConfig) Validate() error {
	if c.FastlyAPIToken == "" {
		return fmt.Errorf("fastly_api_token is required")
	}

	if c.LogLevel == "" {
		c.LogLevel = "info"
	}

	if c.RateLimitPerSecond <= 0 {
		c.RateLimitPerSecond = 1.0 // Default to 1 request per second
	}

	return nil
}
