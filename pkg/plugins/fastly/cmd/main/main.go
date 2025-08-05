package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/hashicorp/go-plugin"
	commonconfig "github.com/opencost/opencost-plugins/pkg/common/config"
	fastlyplugin "github.com/opencost/opencost-plugins/pkg/plugins/fastly/plugin"
	"github.com/opencost/opencost/core/pkg/log"
	"github.com/opencost/opencost/core/pkg/model/pb"
	"github.com/opencost/opencost/core/pkg/opencost"
	ocplugin "github.com/opencost/opencost/core/pkg/plugin"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// handshakeConfigs are used to just do a basic handshake between
// a plugin and host. If the handshake fails, a user-friendly error is shown.
var handshakeConfig = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "PLUGIN_NAME",
	MagicCookieValue: "fastly",
}

// FastlyCostSource implements the CustomCostSource interface
type FastlyCostSource struct {
	config         *fastlyplugin.FastlyConfig
	client         *fastlyplugin.FastlyClient
	rateLimiter    *rate.Limiter
	ctx            context.Context
	costCalculator *fastlyplugin.CostCalculator
	serviceMapper  *fastlyplugin.ServiceMapper
}

// GetCustomCosts retrieves custom costs from Fastly for the given time window
func (f *FastlyCostSource) GetCustomCosts(req *pb.CustomCostRequest) []*pb.CustomCostResponse {
	results := []*pb.CustomCostResponse{}

	// Get windows based on resolution
	targets, err := opencost.GetWindows(req.Start.AsTime(), req.End.AsTime(), req.Resolution.AsDuration())
	if err != nil {
		log.Errorf("error getting windows: %v", err)
		errResp := pb.CustomCostResponse{
			Errors: []string{fmt.Sprintf("error getting windows: %v", err)},
		}
		results = append(results, &errResp)
		return results
	}

	for _, target := range targets {
		// Skip future windows
		if target.Start().After(time.Now().UTC()) {
			log.Debugf("skipping future window %v", target)
			continue
		}

		log.Debugf("fetching Fastly costs for window %v", target)
		result := f.getFastlyCostsForWindow(target)
		results = append(results, result)
	}

	return results
}

func (f *FastlyCostSource) getFastlyCostsForWindow(window opencost.Window) *pb.CustomCostResponse {
	ccResp := &pb.CustomCostResponse{
		Metadata:   map[string]string{"api_version": "v1"},
		CostSource: "infrastructure",
		Domain:     "fastly",
		Version:    "v1",
		Currency:   "USD",
		Start:      timestamppb.New(*window.Start()),
		End:        timestamppb.New(*window.End()),
		Errors:     []string{},
		Costs:      []*pb.CustomCost{},
	}

	// Rate limit check
	if err := f.rateLimiter.Wait(f.ctx); err != nil {
		ccResp.Errors = append(ccResp.Errors, fmt.Sprintf("rate limiter error: %v", err))
		return ccResp
	}

	// Fetch invoices from Fastly API
	invoices, err := f.client.FetchInvoices(f.ctx, *window.Start(), *window.End())
	if err != nil {
		ccResp.Errors = append(ccResp.Errors, fmt.Sprintf("error fetching invoices: %v", err))
		return ccResp
	}

	// Process invoices into costs
	costs := f.processInvoices(invoices, window)

	// Optionally fetch usage data for more detailed cost allocation
	if f.config.EnableUsageDetail {
		usageCosts, err := f.fetchAndProcessUsage(window)
		if err != nil {
			log.Warnf("error fetching usage details: %v", err)
			// Continue with invoice-based costs only
		} else {
			costs = f.reconcileCosts(costs, usageCosts)
		}
	}

	ccResp.Costs = costs
	return ccResp
}

func (f *FastlyCostSource) processInvoices(invoices []fastlyplugin.Invoice, window opencost.Window) []*pb.CustomCost {
	costs := []*pb.CustomCost{}

	for _, invoice := range invoices {
		// Process each line item in the invoice
		for _, lineItem := range invoice.LineItems {
			cost := f.createCostFromLineItem(invoice, lineItem, window)
			if cost != nil {
				costs = append(costs, cost)
			}
		}
	}

	return costs
}

func (f *FastlyCostSource) fetchAndProcessUsage(window opencost.Window) ([]*pb.CustomCost, error) {
	costs := []*pb.CustomCost{}

	// Fetch all services if not already cached
	if f.serviceMapper == nil {
		services, err := f.client.FetchServices(f.ctx)
		if err != nil {
			return nil, fmt.Errorf("error fetching services: %v", err)
		}
		f.serviceMapper = fastlyplugin.NewServiceMapper(services)
	}

	// Get service IDs to process
	serviceIDs := []string{}
	for _, service := range f.serviceMapper.GetServices() {
		// Apply filters if configured
		if len(f.config.ServiceFilters) > 0 {
			found := false
			for _, filter := range f.config.ServiceFilters {
				if service.ID == filter {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Apply exclusions if configured
		excluded := false
		for _, exclude := range f.config.ExcludeServices {
			if service.ID == exclude {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}

		serviceIDs = append(serviceIDs, service.ID)
	}

	// Fetch usage for each service
	for _, serviceID := range serviceIDs {
		usage, err := f.client.FetchUsage(f.ctx, *window.Start(), *window.End(), serviceID)
		if err != nil {
			log.Warnf("error fetching usage for service %s: %v", serviceID, err)
			continue
		}

		// Calculate costs from usage
		for _, u := range usage {
			allocations := f.costCalculator.CalculateUsageCost(u)
			for _, alloc := range allocations {
				cost := f.createCostFromAllocation(alloc)
				costs = append(costs, cost)
			}
		}
	}

	return costs, nil
}

func (f *FastlyCostSource) createCostFromAllocation(alloc fastlyplugin.CostAllocation) *pb.CustomCost {
	serviceName := f.serviceMapper.GetServiceName(alloc.ServiceID)
	attrs := f.serviceMapper.GetServiceAttributes(alloc.ServiceID)

	return &pb.CustomCost{
		AccountName:    f.config.FastlyAccountID,
		ChargeCategory: "usage",
		Description:    fmt.Sprintf("%s - %s", serviceName, alloc.CostType),
		ResourceName:   alloc.CostType,
		ResourceType:   "cdn",
		Id:             fmt.Sprintf("%s-%s-%s-%s", alloc.ServiceID, alloc.CostType, alloc.Region, alloc.StartTime.Format("2006-01-02-15")),
		ProviderId:     alloc.ServiceID,
		BilledCost:     alloc.Amount,
		ListCost:       alloc.Amount,
		UsageQuantity:  alloc.Units,
		UsageUnit:      alloc.UnitType,
		Zone:           alloc.Region,
		Labels: map[string]string{
			"service_id":         alloc.ServiceID,
			"service_name":       serviceName,
			"cost_type":          alloc.CostType,
			"region":             alloc.Region,
			"service_attributes": fmt.Sprintf("%v", attrs),
		},
		ExtendedAttributes: &pb.CustomCostExtendedAttributes{
			ServiceName:     stringPtr("fastly-cdn"),
			ServiceCategory: stringPtr("infrastructure"),
			Provider:        stringPtr("fastly"),
			AccountId:       stringPtr(f.config.FastlyAccountID),
		},
	}
}

func (f *FastlyCostSource) reconcileCosts(invoiceCosts, usageCosts []*pb.CustomCost) []*pb.CustomCost {
	// If we have detailed usage costs, prefer those over invoice line items
	// This provides more granular cost allocation
	if len(usageCosts) > 0 {
		return usageCosts
	}
	return invoiceCosts
}

func (f *FastlyCostSource) createCostFromLineItem(invoice fastlyplugin.Invoice, item fastlyplugin.LineItem, window opencost.Window) *pb.CustomCost {
	// Calculate the daily rate for the line item
	dailyRate := item.Total / float32(invoice.GetDaysInBillingPeriod())

	// Calculate the cost for this specific window
	windowDuration := window.End().Sub(*window.Start()).Hours() / 24.0
	windowCost := dailyRate * float32(windowDuration)

	return &pb.CustomCost{
		AccountName:    invoice.CustomerID,
		ChargeCategory: "usage",
		Description:    item.Description,
		ResourceName:   item.ServiceType,
		ResourceType:   "cdn",
		Id:             fmt.Sprintf("%s-%s-%s", invoice.ID, item.ServiceType, window.Start().Format("2006-01-02")),
		ProviderId:     invoice.ID,
		BilledCost:     windowCost,
		ListCost:       windowCost,
		ListUnitPrice:  item.Rate,
		UsageQuantity:  item.Units,
		UsageUnit:      item.UnitType,
		Labels: map[string]string{
			"service_type": item.ServiceType,
			"region":       item.Region,
		},
		Zone: item.Region,
		ExtendedAttributes: &pb.CustomCostExtendedAttributes{
			BillingPeriodStart: timestamppb.New(invoice.BillingStartDate),
			BillingPeriodEnd:   timestamppb.New(invoice.BillingEndDate),
			ServiceName:        stringPtr("fastly-cdn"),
			ServiceCategory:    stringPtr("infrastructure"),
			Provider:           stringPtr("fastly"),
			AccountId:          stringPtr(invoice.CustomerID),
		},
	}
}

func main() {
	configFile, err := commonconfig.GetConfigFilePath()
	if err != nil {
		log.Fatalf("error opening config file: %v", err)
	}

	fastlyConfig, err := getFastlyConfig(configFile)
	if err != nil {
		log.Fatalf("error building Fastly config: %v", err)
	}

	log.SetLogLevel(fastlyConfig.LogLevel)

	// Create rate limiter
	rateLimiter := rate.NewLimiter(rate.Limit(fastlyConfig.RateLimitPerSecond), 1)

	// Create Fastly API client
	client := fastlyplugin.NewFastlyClient(fastlyConfig.FastlyAPIToken)

	// Create cost calculator
	costCalculator := fastlyplugin.NewCostCalculator()

	// Create Fastly cost source
	fastlyCostSrc := &FastlyCostSource{
		config:         fastlyConfig,
		client:         client,
		rateLimiter:    rateLimiter,
		ctx:            context.Background(),
		costCalculator: costCalculator,
	}

	// Plugin map for the CustomCostSource
	var pluginMap = map[string]plugin.Plugin{
		"CustomCostSource": &ocplugin.CustomCostPlugin{Impl: fastlyCostSrc},
	}

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: handshakeConfig,
		Plugins:         pluginMap,
		GRPCServer:      plugin.DefaultGRPCServer,
	})
}

func getFastlyConfig(configFilePath string) (*fastlyplugin.FastlyConfig, error) {
	var result fastlyplugin.FastlyConfig

	bytes, err := os.ReadFile(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("error reading config file for Fastly config @ %s: %v", configFilePath, err)
	}

	err = json.Unmarshal(bytes, &result)
	if err != nil {
		return nil, fmt.Errorf("error marshaling json into Fastly config: %v", err)
	}

	// Validate config
	if err := result.Validate(); err != nil {
		return nil, err
	}

	return &result, nil
}

// Helper function to convert a string to a string pointer
func stringPtr(s string) *string {
	return &s
}
