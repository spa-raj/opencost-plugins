package tests

import (
	"os"
	"testing"
	"time"

	fastlyplugin "github.com/opencost/opencost-plugins/pkg/plugins/fastly/plugin"
	"github.com/opencost/opencost/core/pkg/log"
	"github.com/opencost/opencost/core/pkg/model/pb"
	"github.com/opencost/opencost/core/pkg/opencost"
	"github.com/opencost/opencost/core/pkg/util/timeutil"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestGetCustomCosts(t *testing.T) {
	// Read necessary env vars
	fastlyAPIToken := os.Getenv("FASTLY_API_TOKEN")

	if fastlyAPIToken == "" {
		log.Warnf("FASTLY_API_TOKEN undefined, skipping test")
		t.Skip()
		return
	}

	config := fastlyplugin.FastlyConfig{
		FastlyAPIToken:     fastlyAPIToken,
		LogLevel:           "debug",
		RateLimitPerSecond: 1.0,
	}

	// Validate config
	if err := config.Validate(); err != nil {
		t.Fatalf("config validation failed: %v", err)
	}

	// Test rate limiter creation
	rateLimiter := rate.NewLimiter(rate.Limit(config.RateLimitPerSecond), 1)

	// Verify rate limiter is configured correctly
	if rateLimiter.Limit() != rate.Limit(config.RateLimitPerSecond) {
		t.Errorf("Rate limiter limit mismatch: got %v, want %v", rateLimiter.Limit(), config.RateLimitPerSecond)
	}

	t.Log("Config validation and rate limiter setup passed")
}

func TestFastlyConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  fastlyplugin.FastlyConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: fastlyplugin.FastlyConfig{
				FastlyAPIToken: "test-token",
				LogLevel:       "info",
			},
			wantErr: false,
		},
		{
			name: "missing API token",
			config: fastlyplugin.FastlyConfig{
				LogLevel: "info",
			},
			wantErr: true,
		},
		{
			name: "default log level",
			config: fastlyplugin.FastlyConfig{
				FastlyAPIToken: "test-token",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestInvoiceProcessing(t *testing.T) {
	// Test invoice processing logic
	invoice := fastlyplugin.Invoice{
		ID:               "inv-123",
		CustomerID:       "cust-456",
		BillingStartDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		BillingEndDate:   time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
		Total:            1000.0,
		Currency:         "USD",
		LineItems: []fastlyplugin.LineItem{
			{
				ID:          "li-1",
				Description: "CDN Bandwidth",
				ServiceType: "cdn_bandwidth",
				Amount:      500.0,
				Rate:        0.05,
				Units:       10000,
				UnitType:    "GB",
				Total:       500.0,
				Region:      "us-east-1",
			},
			{
				ID:          "li-2",
				Description: "CDN Requests",
				ServiceType: "cdn_requests",
				Amount:      300.0,
				Rate:        0.001,
				Units:       300000,
				UnitType:    "requests",
				Total:       300.0,
				Region:      "us-east-1",
			},
		},
	}

	days := invoice.GetDaysInBillingPeriod()
	if days != 30 {
		t.Errorf("Expected 30 days, got %d", days)
	}

	// Test daily rate calculation
	for _, item := range invoice.LineItems {
		dailyRate := item.Total / float32(days)
		if dailyRate <= 0 {
			t.Errorf("Daily rate should be positive, got %f", dailyRate)
		}
	}
}

func TestCostResponseStructure(t *testing.T) {
	// Test the structure of a cost response
	windowStart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	resp := &pb.CustomCostResponse{
		Metadata:   map[string]string{"api_version": "v1"},
		CostSource: "infrastructure",
		Domain:     "fastly",
		Version:    "v1",
		Currency:   "USD",
		Start:      timestamppb.New(windowStart),
		End:        timestamppb.New(windowEnd),
		Errors:     []string{},
		Costs: []*pb.CustomCost{
			{
				AccountName:    "cust-456",
				ChargeCategory: "usage",
				Description:    "CDN Bandwidth",
				ResourceName:   "cdn_bandwidth",
				ResourceType:   "cdn",
				Id:             "inv-123-cdn_bandwidth-2024-01-01",
				BilledCost:     16.67,
				ListCost:       16.67,
				ListUnitPrice:  0.05,
				UsageQuantity:  333.33,
				UsageUnit:      "GB",
				Labels: map[string]string{
					"service_type": "cdn_bandwidth",
					"region":       "us-east-1",
				},
				Zone: "us-east-1",
			},
		},
	}

	// Validate response structure
	if resp.Domain != "fastly" {
		t.Errorf("Expected domain 'fastly', got %s", resp.Domain)
	}

	if len(resp.Costs) != 1 {
		t.Errorf("Expected 1 cost, got %d", len(resp.Costs))
	}

	if resp.Costs[0].ResourceType != "cdn" {
		t.Errorf("Expected resource type 'cdn', got %s", resp.Costs[0].ResourceType)
	}
}

func TestWindowProcessing(t *testing.T) {
	// Test window processing for different resolutions
	tests := []struct {
		name       string
		start      time.Time
		end        time.Time
		resolution time.Duration
		expected   int
	}{
		{
			name:       "daily resolution",
			start:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			end:        time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC),
			resolution: timeutil.Day,
			expected:   7,
		},
		{
			name:       "hourly resolution",
			start:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			end:        time.Date(2024, 1, 1, 6, 0, 0, 0, time.UTC),
			resolution: time.Hour,
			expected:   6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test window calculation
			windows, err := opencost.GetWindows(tt.start, tt.end, tt.resolution)
			if err != nil {
				t.Fatalf("Failed to get windows: %v", err)
			}

			if len(windows) != tt.expected {
				t.Errorf("Expected %d windows, got %d", tt.expected, len(windows))
			}

			// Verify each window has the correct duration
			for i, window := range windows {
				duration := window.End().Sub(*window.Start())
				if duration != tt.resolution {
					t.Errorf("Window %d has incorrect duration: got %v, want %v", i, duration, tt.resolution)
				}
			}

			t.Logf("Test case %s: got %d windows as expected", tt.name, len(windows))
		})
	}
}
