package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/opencost/opencost/core/pkg/log"
)

// FastlyClient provides methods to interact with Fastly API
type FastlyClient struct {
	APIToken   string
	HTTPClient *http.Client
	BaseURL    string
}

// NewFastlyClient creates a new Fastly API client
func NewFastlyClient(apiToken string) *FastlyClient {
	return &FastlyClient{
		APIToken: apiToken,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		BaseURL: "https://api.fastly.com",
	}
}

// FetchInvoices retrieves invoices for the specified time range
func (c *FastlyClient) FetchInvoices(ctx context.Context, start, end time.Time) ([]Invoice, error) {
	allInvoices := []Invoice{}
	cursor := ""

	for {
		url := fmt.Sprintf("%s/billing/v3/invoices?from=%s&to=%s&limit=100",
			c.BaseURL,
			start.Format("2006-01-02"),
			end.Format("2006-01-02"))

		if cursor != "" {
			url += "&cursor=" + cursor
		}

		invoices, meta, err := c.fetchInvoicePage(ctx, url)
		if err != nil {
			return nil, err
		}

		allInvoices = append(allInvoices, invoices...)

		if meta.NextCursor == "" {
			break
		}
		cursor = meta.NextCursor
	}

	return allInvoices, nil
}

func (c *FastlyClient) fetchInvoicePage(ctx context.Context, url string) ([]Invoice, Meta, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, Meta{}, err
	}

	req.Header.Add("Fastly-Key", c.APIToken)
	req.Header.Add("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, Meta{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, Meta{}, fmt.Errorf("API request failed with status %d", resp.StatusCode)
	}

	var response InvoiceResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, Meta{}, err
	}

	return response.Data, response.Meta, nil
}

// FetchUsage retrieves usage data for the specified time range
func (c *FastlyClient) FetchUsage(ctx context.Context, start, end time.Time, serviceID string) ([]Usage, error) {
	allUsage := []Usage{}

	// Fastly usage API typically provides hourly granularity
	current := start
	for current.Before(end) {
		nextHour := current.Add(time.Hour)
		if nextHour.After(end) {
			nextHour = end
		}

		usage, err := c.fetchUsageForPeriod(ctx, current, nextHour, serviceID)
		if err != nil {
			log.Warnf("error fetching usage for period %v-%v: %v", current, nextHour, err)
			// Continue with next period instead of failing completely
			current = nextHour
			continue
		}

		allUsage = append(allUsage, usage...)
		current = nextHour
	}

	return allUsage, nil
}

func (c *FastlyClient) fetchUsageForPeriod(ctx context.Context, start, end time.Time, serviceID string) ([]Usage, error) {
	url := fmt.Sprintf("%s/stats/usage_by_service?from=%s&to=%s&by=hour",
		c.BaseURL,
		start.Format("2006-01-02T15:04:05Z"),
		end.Format("2006-01-02T15:04:05Z"))

	if serviceID != "" {
		url += "&service_id=" + serviceID
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Fastly-Key", c.APIToken)
	req.Header.Add("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage API request failed with status %d", resp.StatusCode)
	}

	var response UsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	return response.Data, nil
}

// FetchServices retrieves all services for the account
func (c *FastlyClient) FetchServices(ctx context.Context) ([]ServiceDetail, error) {
	url := fmt.Sprintf("%s/service", c.BaseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Fastly-Key", c.APIToken)
	req.Header.Add("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("services API request failed with status %d", resp.StatusCode)
	}

	var services []ServiceDetail
	if err := json.NewDecoder(resp.Body).Decode(&services); err != nil {
		return nil, err
	}

	return services, nil
}

// CostCalculator provides methods for calculating costs from usage data
type CostCalculator struct {
	// Pricing rates can be configured here
	BandwidthRate float32 // Cost per GB
	RequestRate   float32 // Cost per 10k requests
	ComputeRate   float32 // Cost per compute second
	ShieldingRate float32 // Cost per GB for origin shielding
}

// NewCostCalculator creates a new cost calculator with default rates
func NewCostCalculator() *CostCalculator {
	return &CostCalculator{
		BandwidthRate: 0.05,   // $0.05 per GB
		RequestRate:   0.001,  // $0.001 per 10k requests
		ComputeRate:   0.0001, // $0.0001 per compute second
		ShieldingRate: 0.02,   // $0.02 per GB for shielding
	}
}

// CalculateUsageCost calculates cost from usage metrics
func (calc *CostCalculator) CalculateUsageCost(usage Usage) []CostAllocation {
	allocations := []CostAllocation{}

	for region, metrics := range usage.Usage {
		// Bandwidth cost
		if metrics.Bandwidth > 0 {
			bandwidthGB := float32(metrics.Bandwidth) / (1024 * 1024 * 1024)
			allocations = append(allocations, CostAllocation{
				ServiceID:   usage.ServiceID,
				ServiceName: usage.ServiceName,
				Region:      region,
				CostType:    "bandwidth",
				Amount:      bandwidthGB * calc.BandwidthRate,
				Units:       bandwidthGB,
				UnitType:    "GB",
				StartTime:   usage.StartTime,
				EndTime:     usage.EndTime,
			})
		}

		// Request cost
		if metrics.Requests > 0 {
			requestsIn10k := float32(metrics.Requests) / 10000
			allocations = append(allocations, CostAllocation{
				ServiceID:   usage.ServiceID,
				ServiceName: usage.ServiceName,
				Region:      region,
				CostType:    "requests",
				Amount:      requestsIn10k * calc.RequestRate,
				Units:       requestsIn10k,
				UnitType:    "10k_requests",
				StartTime:   usage.StartTime,
				EndTime:     usage.EndTime,
			})
		}

		// Compute cost
		if metrics.ComputeDuration > 0 {
			computeSeconds := float32(metrics.ComputeDuration) / 1000
			allocations = append(allocations, CostAllocation{
				ServiceID:   usage.ServiceID,
				ServiceName: usage.ServiceName,
				Region:      region,
				CostType:    "compute",
				Amount:      computeSeconds * calc.ComputeRate,
				Units:       computeSeconds,
				UnitType:    "compute_seconds",
				StartTime:   usage.StartTime,
				EndTime:     usage.EndTime,
			})
		}

		// Origin shielding cost (cached bandwidth)
		if metrics.BandwidthCached > 0 {
			cachedGB := float32(metrics.BandwidthCached) / (1024 * 1024 * 1024)
			allocations = append(allocations, CostAllocation{
				ServiceID:   usage.ServiceID,
				ServiceName: usage.ServiceName,
				Region:      region,
				CostType:    "origin_shielding",
				Amount:      cachedGB * calc.ShieldingRate,
				Units:       cachedGB,
				UnitType:    "GB",
				StartTime:   usage.StartTime,
				EndTime:     usage.EndTime,
			})
		}
	}

	return allocations
}

// ServiceMapper provides methods for enriching cost data with service information
type ServiceMapper struct {
	services map[string]ServiceDetail
}

// NewServiceMapper creates a new service mapper
func NewServiceMapper(services []ServiceDetail) *ServiceMapper {
	serviceMap := make(map[string]ServiceDetail)
	for _, service := range services {
		serviceMap[service.ID] = service
	}

	return &ServiceMapper{
		services: serviceMap,
	}
}

// GetServiceName returns the service name for a given service ID
func (sm *ServiceMapper) GetServiceName(serviceID string) string {
	if service, ok := sm.services[serviceID]; ok {
		return service.Name
	}
	return serviceID
}

// GetServiceAttributes returns the attributes for a given service ID
func (sm *ServiceMapper) GetServiceAttributes(serviceID string) map[string]string {
	if service, ok := sm.services[serviceID]; ok {
		return service.Attributes
	}
	return map[string]string{}
}

// GetServices returns all services
func (sm *ServiceMapper) GetServices() []ServiceDetail {
	services := make([]ServiceDetail, 0, len(sm.services))
	for _, service := range sm.services {
		services = append(services, service)
	}
	return services
}
