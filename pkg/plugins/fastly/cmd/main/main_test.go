package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencost/opencost-plugins/pkg/plugins/fastly/fastlyplugin"
	"github.com/opencost/opencost/core/pkg/opencost"
	"golang.org/x/time/rate"
)

// mockHTTPTransport implements http.RoundTripper for testing
type mockHTTPTransport struct {
	responses map[string]*http.Response
}

// URL query parameter order-insensitive matching
func (m *mockHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Try exact match first
	if response, ok := m.responses[req.URL.String()]; ok {
		return response, nil
	}

	// If exact match fails, try matching by normalized URL
	// (comparing host, path and query parameters regardless of order)
	requestURL := req.URL.String()
	for mockURL, response := range m.responses {
		if haveSameParams(mockURL, requestURL) {
			return response, nil
		}
	}

	return nil, fmt.Errorf("no mock response found for URL: %s", req.URL.String())
}

// Helper function to check if two URLs have the same parameters regardless of order
func haveSameParams(url1, url2 string) bool {
	u1, err := url.Parse(url1)
	if err != nil {
		return false
	}
	u2, err := url.Parse(url2)
	if err != nil {
		return false
	}

	// Check if host and path match
	if u1.Host != u2.Host || u1.Path != u2.Path {
		return false
	}

	// Check if query parameters are the same (regardless of order)
	q1 := u1.Query()
	q2 := u2.Query()

	if len(q1) != len(q2) {
		return false
	}

	for k, v1 := range q1 {
		v2, ok := q2[k]
		if !ok {
			return false
		}
		if len(v1) != len(v2) {
			return false
		}
		for i := range v1 {
			if v1[i] != v2[i] {
				return false
			}
		}
	}

	return true
}

// createMockResponse creates a mock HTTP response with given status code and body
func createMockResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

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
	config, err := getFastlyConfig(configPath)

	// Assert
	if err != nil {
		t.Fatalf("getFastlyConfig returned error: %v", err)
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
	config, err := getFastlyConfig(configPath)

	// Assert
	if err != nil {
		t.Fatalf("getFastlyConfig returned error: %v", err)
	}
	if config.LogLevel != "info" {
		t.Errorf("Expected default log level 'info', got '%s'", config.LogLevel)
	}
}

func TestGetFastlyConfigInvalidPath(t *testing.T) {
	// Arrange
	configPath := "/nonexistent/path/to/config.json"

	// Act
	config, err := getFastlyConfig(configPath)

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
	config, err := getFastlyConfig(configPath)

	// Assert
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
	if config != nil {
		t.Error("Expected nil config for invalid JSON")
	}
}

func TestGetMonthToDateInvoice(t *testing.T) {
	// Arrange
	mockTransport := &mockHTTPTransport{
		responses: map[string]*http.Response{
			"https://api.fastly.com/billing/v3/invoices/month-to-date": createMockResponse(200, `{
                "customer_id": "test-customer",
                "invoice_id": "mtd-12345",
                "billing_start_date": "2024-01-01T00:00:00Z",
                "billing_end_date": "2024-01-31T00:00:00Z",
                "currency_code": "USD",
                "monthly_transaction_amount": 50.75,
                "transaction_line_items": [
                    {
                        "description": "CDN Bandwidth",
                        "amount": 50.75,
                        "rate": 0.05,
                        "units": 1015,
                        "product_name": "CDN",
                        "product_group": "Full Site Delivery",
                        "product_line": "Network Services",
                        "region": "Global",
                        "usage_type": "bandwidth"
                    }
                ]
            }`),
		},
	}

	rateLimiter := rate.NewLimiter(rate.Every(time.Second), 10)
	fastlyCostSrc := FastlyCostSource{
		apiKey:       "test-api-key",
		httpClient:   &http.Client{Transport: mockTransport},
		rateLimiter:  rateLimiter,
		invoiceCache: make(map[string][]fastlyplugin.Invoice),
	}

	// Act
	invoice, err := fastlyCostSrc.getMonthToDateInvoice()

	// Assert
	if err != nil {
		t.Fatalf("Failed to get month-to-date invoice: %v", err)
	}
	if invoice == nil {
		t.Fatal("Received nil invoice")
	}
	if invoice.InvoiceID != "mtd-12345" {
		t.Errorf("Expected invoice ID mtd-12345, got %s", invoice.InvoiceID)
	}
	if len(invoice.TransactionLineItems) != 1 {
		t.Errorf("Expected 1 transaction line item, got %d", len(invoice.TransactionLineItems))
	}
}

func TestGetMonthToDateInvoiceError(t *testing.T) {
	// Arrange
	mockTransport := &mockHTTPTransport{
		responses: map[string]*http.Response{
			"https://api.fastly.com/billing/v3/invoices/month-to-date": createMockResponse(403, `{
                "msg": "Unauthorized",
                "detail": "Invalid API key"
            }`),
		},
	}

	rateLimiter := rate.NewLimiter(rate.Every(time.Second), 10)
	fastlyCostSrc := FastlyCostSource{
		apiKey:       "invalid-api-key",
		httpClient:   &http.Client{Transport: mockTransport},
		rateLimiter:  rateLimiter,
		invoiceCache: make(map[string][]fastlyplugin.Invoice),
	}

	// Act
	invoice, err := fastlyCostSrc.getMonthToDateInvoice()

	// Assert
	if err == nil {
		t.Error("Expected error for unauthorized request, got nil")
	}
	if invoice != nil {
		t.Error("Expected nil invoice for error response")
	}
}

func TestGetInvoicesForPeriod(t *testing.T) {
	// Arrange
	mockTransport := &mockHTTPTransport{
		responses: map[string]*http.Response{
			"https://api.fastly.com/billing/v3/invoices?billing_start_date=2024-01-01&billing_end_date=2024-01-31&limit=200": createMockResponse(200, `{
                "data": [
                    {
                        "customer_id": "test-customer",
                        "invoice_id": "inv-12345",
                        "billing_start_date": "2024-01-01T00:00:00Z",
                        "billing_end_date": "2024-01-31T00:00:00Z",
                        "currency_code": "USD",
                        "monthly_transaction_amount": 100.50,
                        "transaction_line_items": [
                            {
                                "description": "CDN Bandwidth",
                                "amount": 100.50,
                                "rate": 0.05,
                                "units": 2010,
                                "product_name": "CDN",
                                "product_group": "Full Site Delivery",
                                "product_line": "Network Services",
                                "region": "Global",
                                "usage_type": "bandwidth"
                            }
                        ]
                    }
                ],
                "meta": {
                    "next_cursor": "",
                    "limit": 200,
                    "total": 1,
                    "sort": "billing_start_date"
                }
            }`),
		},
	}

	rateLimiter := rate.NewLimiter(rate.Every(time.Second), 10)
	fastlyCostSrc := FastlyCostSource{
		apiKey:       "test-api-key",
		httpClient:   &http.Client{Transport: mockTransport},
		rateLimiter:  rateLimiter,
		invoiceCache: make(map[string][]fastlyplugin.Invoice),
	}

	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC)

	// Act
	invoices, err := fastlyCostSrc.getInvoicesForPeriod(&startDate, &endDate)

	// Assert
	if err != nil {
		t.Fatalf("Failed to get invoices for period: %v", err)
	}
	if len(invoices) != 1 {
		t.Errorf("Expected 1 invoice, got %d", len(invoices))
	}
	if invoices[0].InvoiceID != "inv-12345" {
		t.Errorf("Expected invoice ID inv-12345, got %s", invoices[0].InvoiceID)
	}
}

func TestGetInvoicesForPeriodWithPagination(t *testing.T) {
	// Arrange
	mockTransport := &mockHTTPTransport{
		responses: map[string]*http.Response{
			// First page
			"https://api.fastly.com/billing/v3/invoices?billing_start_date=2024-01-01&billing_end_date=2024-01-31&limit=200": createMockResponse(200, `{
                "data": [
                    {
                        "customer_id": "test-customer",
                        "invoice_id": "inv-12345",
                        "billing_start_date": "2024-01-01T00:00:00Z",
                        "billing_end_date": "2024-01-31T00:00:00Z",
                        "transaction_line_items": [
                            {
                                "description": "CDN Bandwidth",
                                "amount": 100.50,
                                "rate": 0.05,
                                "units": 2010,
                                "product_name": "CDN",
                                "product_group": "Full Site Delivery",
                                "usage_type": "bandwidth"
                            }
                        ]
                    }
                ],
                "meta": {
                    "next_cursor": "page2",
                    "limit": 200,
                    "total": 2
                }
            }`),
			// Second page
			"https://api.fastly.com/billing/v3/invoices?billing_start_date=2024-01-01&billing_end_date=2024-01-31&limit=200&cursor=page2": createMockResponse(200, `{
                "data": [
                    {
                        "customer_id": "test-customer",
                        "invoice_id": "inv-67890",
                        "billing_start_date": "2024-01-01T00:00:00Z",
                        "billing_end_date": "2024-01-31T00:00:00Z",
                        "transaction_line_items": [
                            {
                                "description": "Compute Requests",
                                "amount": 75.25,
                                "rate": 0.001,
                                "units": 75250,
                                "product_name": "Compute",
                                "product_group": "Compute",
                                "usage_type": "requests"
                            }
                        ]
                    }
                ],
                "meta": {
                    "next_cursor": "",
                    "limit": 200,
                    "total": 2
                }
            }`),
		},
	}

	rateLimiter := rate.NewLimiter(rate.Every(time.Second), 10)
	fastlyCostSrc := FastlyCostSource{
		apiKey:       "test-api-key",
		httpClient:   &http.Client{Transport: mockTransport},
		rateLimiter:  rateLimiter,
		invoiceCache: make(map[string][]fastlyplugin.Invoice),
	}

	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC)

	// Act
	invoices, err := fastlyCostSrc.getInvoicesForPeriod(&startDate, &endDate)

	// Assert
	if err != nil {
		t.Fatalf("Failed to get invoices for period: %v", err)
	}
	if len(invoices) != 2 {
		t.Errorf("Expected 2 invoices from pagination, got %d", len(invoices))
	}

	foundFirst := false
	foundSecond := false
	for _, inv := range invoices {
		if inv.InvoiceID == "inv-12345" {
			foundFirst = true
		} else if inv.InvoiceID == "inv-67890" {
			foundSecond = true
		}
	}

	if !foundFirst {
		t.Error("First invoice not found in results")
	}
	if !foundSecond {
		t.Error("Second invoice not found in results")
	}
}

func TestGetFastlyCostsForWindow(t *testing.T) {
	// Arrange
	mockTransport := &mockHTTPTransport{
		responses: map[string]*http.Response{
			"https://api.fastly.com/billing/v3/invoices?billing_start_date=2024-01-01&billing_end_date=2024-01-31&limit=200": createMockResponse(200, `{
                "data": [
                    {
                        "customer_id": "test-customer",
                        "invoice_id": "inv-12345",
                        "billing_start_date": "2024-01-01T00:00:00Z",
                        "billing_end_date": "2024-01-31T00:00:00Z",
                        "transaction_line_items": [
                            {
                                "description": "CDN Bandwidth",
                                "amount": 100.50,
                                "rate": 0.05,
                                "units": 2010,
                                "product_name": "CDN",
                                "product_group": "Full Site Delivery",
                                "region": "Global",
                                "usage_type": "bandwidth"
                            }
                        ]
                    }
                ],
                "meta": {
                    "next_cursor": "",
                    "limit": 200,
                    "total": 1
                }
            }`),
		},
	}

	rateLimiter := rate.NewLimiter(rate.Every(time.Second), 10)
	fastlyCostSrc := FastlyCostSource{
		apiKey:       "test-api-key",
		httpClient:   &http.Client{Transport: mockTransport},
		rateLimiter:  rateLimiter,
		invoiceCache: make(map[string][]fastlyplugin.Invoice),
	}

	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 31, 23, 59, 59, 0, time.UTC)
	window := opencost.NewWindow(&startDate, &endDate)

	// Act
	result := fastlyCostSrc.getFastlyCostsForWindow(window)

	// Assert
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if len(result.Costs) != 1 {
		t.Errorf("Expected 1 cost item, got %d", len(result.Costs))
	}
	if len(result.Errors) > 0 {
		t.Errorf("Unexpected errors in response: %v", result.Errors)
	}

	// Check cost properties
	if len(result.Costs) > 0 {
		cost := result.Costs[0]
		if cost.ResourceType != "Full Site Delivery" {
			t.Errorf("Expected resource type 'Full Site Delivery', got '%s'", cost.ResourceType)
		}
		if cost.UsageUnit != "GB" {
			t.Errorf("Expected usage unit 'GB' for bandwidth, got '%s'", cost.UsageUnit)
		}
	}
}

func TestBoilerplateFastlyCustomCost(t *testing.T) {
	// Arrange
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC)
	window := opencost.NewWindow(&startDate, &endDate)

	// Act
	result := boilerplateFastlyCustomCost(window)

	// Assert
	if result.Domain != "fastly" {
		t.Errorf("Expected domain 'fastly', got '%s'", result.Domain)
	}
	if result.CostSource != "billing" {
		t.Errorf("Expected cost source 'billing', got '%s'", result.CostSource)
	}
	if result.Currency != "USD" {
		t.Errorf("Expected currency 'USD', got '%s'", result.Currency)
	}
	if result.Start.AsTime().Format(time.RFC3339) != startDate.Format(time.RFC3339) {
		t.Errorf("Expected start time %s, got %s", startDate.Format(time.RFC3339), result.Start.AsTime().Format(time.RFC3339))
	}
	if result.End.AsTime().Format(time.RFC3339) != endDate.Format(time.RFC3339) {
		t.Errorf("Expected end time %s, got %s", endDate.Format(time.RFC3339), result.End.AsTime().Format(time.RFC3339))
	}
	if len(result.Errors) != 0 {
		t.Errorf("Expected empty errors, got %v", result.Errors)
	}
	if len(result.Costs) != 0 {
		t.Errorf("Expected empty costs, got %d costs", len(result.Costs))
	}
}
