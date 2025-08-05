package main

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/opencost/opencost-plugins/pkg/plugins/fastly/fastlyplugin"
	"github.com/opencost/opencost/core/pkg/log"
	"github.com/opencost/opencost/core/pkg/model/pb"
	"github.com/opencost/opencost/core/pkg/opencost"
	"github.com/opencost/opencost/core/pkg/util/timeutil"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestGetCustomCosts(t *testing.T) {
	// read necessary env vars. If any are missing, log warning and skip test
	fastlyAPIKey := os.Getenv("FASTLY_API_KEY")

	if fastlyAPIKey == "" {
		log.Warnf("FASTLY_API_KEY undefined, skipping test")
		t.Skip()
		return
	}

	// write out config
	config := fastlyplugin.FastlyConfig{
		FastlyAPIKey: fastlyAPIKey,
		LogLevel:     "debug",
	}

	rateLimiter := rate.NewLimiter(rate.Every(time.Second), 10)
	fastlyCostSrc := FastlyCostSource{
		apiKey:       config.FastlyAPIKey,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		rateLimiter:  rateLimiter,
		invoiceCache: make(map[string][]fastlyplugin.Invoice),
	}

	// Query for last month's data
	now := time.Now()
	windowStart := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	req := &pb.CustomCostRequest{
		Start:      timestamppb.New(windowStart),
		End:        timestamppb.New(windowEnd),
		Resolution: durationpb.New(timeutil.Day),
	}

	log.SetLogLevel("trace")
	resp := fastlyCostSrc.GetCustomCosts(req)

	if len(resp) == 0 {
		t.Fatalf("empty response")
	}

	// Check for errors
	for _, r := range resp {
		if len(r.Errors) > 0 {
			t.Errorf("errors in response: %v", r.Errors)
		}
	}

	// Verify domain
	for _, r := range resp {
		if r.Domain != "fastly" {
			t.Errorf("expected domain 'fastly', got %s", r.Domain)
		}
	}

	// Log some results for debugging
	totalCosts := 0
	totalBilled := float32(0)
	for _, r := range resp {
		totalCosts += len(r.Costs)
		for _, cost := range r.Costs {
			totalBilled += cost.BilledCost
		}
	}
	t.Logf("Total responses: %d, Total costs: %d, Total billed: %.2f", len(resp), totalCosts, totalBilled)
}

func TestConvertInvoiceToCosts(t *testing.T) {
	fastlyCostSrc := FastlyCostSource{}

	// Create a test invoice
	invoice := fastlyplugin.Invoice{
		CustomerID:       "test-customer-123",
		InvoiceID:        "inv-123",
		BillingStartDate: "2024-01-01T00:00:00Z",
		BillingEndDate:   "2024-01-31T23:59:59Z",
		CurrencyCode:     "USD",
		TransactionLineItems: []fastlyplugin.TransactionLineItem{
			{
				Description:  "CDN Bandwidth",
				Amount:       100.50,
				Rate:         0.05,
				Units:        2010,
				ProductName:  "CDN",
				ProductGroup: "Full Site Delivery",
				ProductLine:  "Network Services",
				Region:       "Global",
				UsageType:    "bandwidth",
			},
			{
				Description:  "Compute Requests",
				Amount:       50.25,
				Rate:         0.001,
				Units:        50250,
				ProductName:  "Compute@Edge",
				ProductGroup: "Compute",
				ProductLine:  "Edge Computing",
				Region:       "US-East",
				UsageType:    "requests",
			},
		},
	}

	// Create a window that overlaps with the invoice
	windowStart := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC)
	window := opencost.NewWindow(&windowStart, &windowEnd)

	costs := fastlyCostSrc.convertInvoiceToCosts(invoice, window)

	if len(costs) != 2 {
		t.Errorf("expected 2 costs, got %d", len(costs))
	}

	// Verify costs are prorated correctly
	for _, cost := range costs {
		if cost.BilledCost <= 0 {
			t.Errorf("expected positive billed cost, got %f", cost.BilledCost)
		}
		if cost.UsageQuantity <= 0 {
			t.Errorf("expected positive usage quantity, got %f", cost.UsageQuantity)
		}
		// Since we're getting 1 day out of 31, costs should be roughly 1/31 of the total
		if cost.ResourceName == "bandwidth" && cost.BilledCost > 5 {
			t.Errorf("bandwidth cost seems too high for 1-day proration: %f", cost.BilledCost)
		}
	}
}

func TestGetUsageUnit(t *testing.T) {
	tests := []struct {
		usageType string
		expected  string
	}{
		{"bandwidth", "GB"},
		{"requests", "requests"},
		{"compute_hours", "hours"},
		{"Bandwidth_GB", "GB"},
		{"API_Requests", "requests"},
		{"custom_metric", "units"},
	}

	for _, test := range tests {
		result := getUsageUnit(test.usageType)
		if result != test.expected {
			t.Errorf("getUsageUnit(%s) = %s, expected %s", test.usageType, result, test.expected)
		}
	}
}
