package plugin

import (
	"fmt"
	"time"
)

// InvoiceResponse represents the response from Fastly's invoice API
type InvoiceResponse struct {
	Data []Invoice `json:"data"`
	Meta Meta      `json:"meta"`
}

// Invoice represents a Fastly invoice
type Invoice struct {
	ID               string     `json:"id"`
	CustomerID       string     `json:"customer_id"`
	InvoiceNumber    string     `json:"invoice_number"`
	State            string     `json:"state"`
	Total            float32    `json:"total"`
	Region           string     `json:"region"`
	BillingStartDate time.Time  `json:"billing_start_date"`
	BillingEndDate   time.Time  `json:"billing_end_date"`
	IssuedOn         time.Time  `json:"issued_on"`
	DueOn            time.Time  `json:"due_on"`
	Currency         string     `json:"currency_code"`
	LineItems        []LineItem `json:"line_items"`
}

// LineItem represents a line item in a Fastly invoice
type LineItem struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	Amount      float32 `json:"amount"`
	Rate        float32 `json:"rate"`
	Units       float32 `json:"units"`
	UnitType    string  `json:"unit_type"`
	ServiceType string  `json:"service_type"`
	ServiceID   string  `json:"service_id"`
	Total       float32 `json:"total"`
	Region      string  `json:"region"`
	ProductLine string  `json:"product_line"`
}

// Meta represents pagination metadata
type Meta struct {
	NextCursor string `json:"next_cursor"`
	Limit      int    `json:"limit"`
}

// GetDaysInBillingPeriod calculates the number of days in the billing period
func (i *Invoice) GetDaysInBillingPeriod() int {
	return int(i.BillingEndDate.Sub(i.BillingStartDate).Hours() / 24)
}

// UsageResponse represents the response from Fastly's usage API
type UsageResponse struct {
	Data []Usage `json:"data"`
	Meta Meta    `json:"meta"`
}

// Usage represents usage data from Fastly
type Usage struct {
	ServiceID   string                 `json:"service_id"`
	ServiceName string                 `json:"service_name"`
	StartTime   time.Time              `json:"start_time"`
	EndTime     time.Time              `json:"end_time"`
	Region      string                 `json:"region"`
	Usage       map[string]UsageMetric `json:"usage"`
}

// UsageMetric represents a specific usage metric
type UsageMetric struct {
	Requests        int64   `json:"requests"`
	Bandwidth       int64   `json:"bandwidth"`
	BandwidthCached int64   `json:"bandwidth_cached"`
	ComputeRequests int64   `json:"compute_requests"`
	ComputeDuration float64 `json:"compute_duration_ms"`
}

// ServiceDetail represents detailed information about a Fastly service
type ServiceDetail struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	CustomerID    string            `json:"customer_id"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
	ActiveVersion int               `json:"active_version"`
	Type          string            `json:"type"`
	Attributes    map[string]string `json:"attributes"`
}

// CostAllocation helps with allocating costs across different services and regions
type CostAllocation struct {
	ServiceID   string
	ServiceName string
	Region      string
	CostType    string
	Amount      float32
	Units       float32
	UnitType    string
	StartTime   time.Time
	EndTime     time.Time
}

// GetCostCategory returns the cost category based on the service type
func GetCostCategory(serviceType string) string {
	switch serviceType {
	case "cdn_bandwidth", "cdn_requests", "origin_shielding":
		return "networking"
	case "compute", "compute_requests":
		return "compute"
	case "logs", "real_time_analytics":
		return "observability"
	case "waf", "ddos_protection":
		return "security"
	default:
		return "other"
	}
}

// IsWithinTimeRange checks if an invoice covers the specified time range
func (i *Invoice) IsWithinTimeRange(start, end time.Time) bool {
	// Check if invoice period overlaps with requested time range
	return !(i.BillingEndDate.Before(start) || i.BillingStartDate.After(end))
}

// GetProportionalCost calculates the proportional cost for a specific time window
func (i *Invoice) GetProportionalCost(windowStart, windowEnd time.Time) float32 {
	// Calculate overlap between invoice period and window
	overlapStart := i.BillingStartDate
	if windowStart.After(overlapStart) {
		overlapStart = windowStart
	}

	overlapEnd := i.BillingEndDate
	if windowEnd.Before(overlapEnd) {
		overlapEnd = windowEnd
	}

	// If no overlap, return 0
	if overlapEnd.Before(overlapStart) || overlapEnd.Equal(overlapStart) {
		return 0
	}

	// Calculate proportion
	overlapDays := overlapEnd.Sub(overlapStart).Hours() / 24.0
	totalDays := float64(i.GetDaysInBillingPeriod())

	if totalDays == 0 {
		return 0
	}

	proportion := float32(overlapDays / totalDays)
	return i.Total * proportion
}

// Validate checks if the invoice data is valid
func (i *Invoice) Validate() error {
	if i.ID == "" {
		return fmt.Errorf("invoice ID is empty")
	}

	if i.BillingEndDate.Before(i.BillingStartDate) {
		return fmt.Errorf("billing end date is before start date")
	}

	if i.Total < 0 {
		return fmt.Errorf("invoice total is negative")
	}

	return nil
}
