package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-plugin"
	"github.com/opencost/opencost-plugins/pkg/plugins/fastly/fastlyplugin"
	"github.com/opencost/opencost/core/pkg/log"
	"github.com/opencost/opencost/core/pkg/model/pb"
	"github.com/opencost/opencost/core/pkg/opencost"
	ocplugin "github.com/opencost/opencost/core/pkg/plugin"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	fastlyAPIBaseURL = "https://api.fastly.com"
)

// handshakeConfigs are used to just do a basic handshake between
// a plugin and host. If the handshake fails, a user friendly error is shown.
// This prevents users from executing bad plugins or executing a plugin
// directory. It is a UX feature, not a security feature.
var handshakeConfig = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "PLUGIN_NAME",
	MagicCookieValue: "fastly",
}

// Implementation of CustomCostSource
type FastlyCostSource struct {
	apiKey          string
	httpClient      *http.Client
	rateLimiter     *rate.Limiter
	invoiceCache    map[string][]fastlyplugin.Invoice
	invoiceCacheMux sync.Mutex
}

func (f *FastlyCostSource) GetCustomCosts(req *pb.CustomCostRequest) []*pb.CustomCostResponse {
	results := []*pb.CustomCostResponse{}

	targets, err := opencost.GetWindows(req.Start.AsTime(), req.End.AsTime(), req.Resolution.AsDuration())

	// Fetch all invoices at once for the entire period
	startTime := req.Start.AsTime()
	endTime := req.End.AsTime()
	allInvoices, err := f.getInvoicesForPeriod(&startTime, &endTime)

	if err != nil {
		log.Errorf("error getting windows: %v", err)
		startTime := req.Start.AsTime()
		endTime := req.End.AsTime()
		errResp := boilerplateFastlyCustomCost(opencost.NewWindow(&startTime, &endTime))
		errResp.Errors = []string{fmt.Sprintf("error getting windows: %v", err)}
		results = append(results, &errResp)
		return results
	}

	// Store in cache
	cacheKey := fmt.Sprintf("%s-%s", req.Start.AsTime().Format("2006-01"), req.End.AsTime().Format("2006-01"))
	f.invoiceCacheMux.Lock()
	f.invoiceCache[cacheKey] = allInvoices
	f.invoiceCacheMux.Unlock()

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
	ccResp := boilerplateFastlyCustomCost(window)
	costs := []*pb.CustomCost{}

	// Get invoices for the window period
	invoices, err := f.getInvoicesForPeriod(window.Start(), window.End())
	if err != nil {
		log.Errorf("error fetching invoices: %v", err)
		ccResp.Errors = append(ccResp.Errors, err.Error())
		return &ccResp
	}

	// Convert invoices to custom costs
	for _, invoice := range invoices {
		invoiceCosts := f.convertInvoiceToCosts(invoice, window)
		costs = append(costs, invoiceCosts...)
	}

	ccResp.Costs = costs
	return &ccResp
}

func (f *FastlyCostSource) getInvoicesForPeriod(start, end *time.Time) ([]fastlyplugin.Invoice, error) {
	allInvoices := []fastlyplugin.Invoice{}
	cursor := ""
	hasMore := true

	for hasMore {
		// Rate limiting
		if f.rateLimiter.Tokens() < 1.0 {
			log.Infof("fastly rate limit reached. holding request until rate capacity is back")
		}
		err := f.rateLimiter.WaitN(context.TODO(), 1)
		if err != nil {
			return nil, fmt.Errorf("error waiting on rate limiter: %v", err)
		}

		// Build request URL
		reqURL := fmt.Sprintf("%s/billing/v3/invoices", fastlyAPIBaseURL)
		params := url.Values{}
		params.Add("billing_start_date", start.Format("2006-01-02"))
		params.Add("billing_end_date", end.Format("2006-01-02"))
		params.Add("limit", "200")
		if cursor != "" {
			params.Add("cursor", cursor)
		}
		reqURL = fmt.Sprintf("%s?%s", reqURL, params.Encode())

		// Make request
		req, err := http.NewRequest("GET", reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("error creating request: %v", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Fastly-Key", f.apiKey)
		req.Header.Set("Host", "api.fastly.com")

		resp, err := f.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("error making request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
		}

		// Parse response
		var invoiceResp fastlyplugin.InvoiceListResponse
		if err := json.NewDecoder(resp.Body).Decode(&invoiceResp); err != nil {
			return nil, fmt.Errorf("error decoding response: %v", err)
		}

		allInvoices = append(allInvoices, invoiceResp.Data...)

		// Check for more pages
		if invoiceResp.Meta.NextCursor == "" {
			hasMore = false
		} else {
			cursor = invoiceResp.Meta.NextCursor
		}
	}

	// Also get month-to-date if the window includes current month
	now := time.Now()
	if start.Before(now) && end.After(time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)) {
		mtdInvoice, err := f.getMonthToDateInvoice()
		if err != nil {
			log.Warnf("error fetching month-to-date invoice: %v", err)
		} else if mtdInvoice != nil {
			allInvoices = append(allInvoices, *mtdInvoice)
		}
	}

	return allInvoices, nil
}

func (f *FastlyCostSource) getMonthToDateInvoice() (*fastlyplugin.Invoice, error) {
	// Rate limiting
	if f.rateLimiter.Tokens() < 1.0 {
		log.Infof("fastly rate limit reached. holding request until rate capacity is back")
	}
	err := f.rateLimiter.WaitN(context.TODO(), 1)
	if err != nil {
		return nil, fmt.Errorf("error waiting on rate limiter: %v", err)
	}

	reqURL := fmt.Sprintf("%s/billing/v3/invoices/month-to-date", fastlyAPIBaseURL)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Fastly-Key", f.apiKey)
	req.Header.Set("Host", "api.fastly.com")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var invoice fastlyplugin.Invoice
	if err := json.NewDecoder(resp.Body).Decode(&invoice); err != nil {
		return nil, fmt.Errorf("error decoding response: %v", err)
	}

	return &invoice, nil
}

func (f *FastlyCostSource) getInvoiceByID(invoiceID string) (*fastlyplugin.Invoice, error) {
	// Rate limiting
	if f.rateLimiter.Tokens() < 1.0 {
		log.Infof("fastly rate limit reached. holding request until rate capacity is back")
	}
	err := f.rateLimiter.WaitN(context.TODO(), 1)
	if err != nil {
		return nil, fmt.Errorf("error waiting on rate limiter: %v", err)
	}

	reqURL := fmt.Sprintf("%s/billing/v3/invoices/%s", fastlyAPIBaseURL, invoiceID)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Fastly-Key", f.apiKey)
	req.Header.Set("Host", "api.fastly.com")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var invoice fastlyplugin.Invoice
	if err := json.NewDecoder(resp.Body).Decode(&invoice); err != nil {
		return nil, fmt.Errorf("error decoding response: %v", err)
	}

	return &invoice, nil
}

func (f *FastlyCostSource) convertInvoiceToCosts(invoice fastlyplugin.Invoice, window opencost.Window) []*pb.CustomCost {
	costs := []*pb.CustomCost{}

	// Parse invoice dates
	startDate, err := fastlyplugin.ParseFastlyDate(invoice.BillingStartDate)
	if err != nil {
		log.Errorf("error parsing billing start date: %v", err)
		return costs
	}
	endDate, err := fastlyplugin.ParseFastlyDate(invoice.BillingEndDate)
	if err != nil {
		log.Errorf("error parsing billing end date: %v", err)
		return costs
	}

	// Check if invoice overlaps with window
	invoiceStart := startDate
	invoiceEnd := endDate
	windowStart := *window.Start()
	windowEnd := *window.End()

	if invoiceEnd.Before(windowStart) || invoiceStart.After(windowEnd) {
		return costs
	}

	// Calculate the overlap duration for prorating
	overlapStart := invoiceStart
	if windowStart.After(invoiceStart) {
		overlapStart = windowStart
	}
	overlapEnd := invoiceEnd
	if windowEnd.Before(invoiceEnd) {
		overlapEnd = windowEnd
	}

	overlapHours := overlapEnd.Sub(overlapStart).Hours()
	totalInvoiceHours := invoiceEnd.Sub(invoiceStart).Hours()
	prorateRatio := float32(1.0)
	if totalInvoiceHours > 0 {
		prorateRatio = float32(overlapHours / totalInvoiceHours)
	}

	// Convert each line item to a cost
	for _, item := range invoice.TransactionLineItems {
		// Skip zero-amount items
		if item.Amount == 0 {
			continue
		}

		// Create provider ID
		providerID := fmt.Sprintf("%s/%s/%s", invoice.InvoiceID, item.ProductName, item.UsageType)

		// Prorate the cost based on window overlap
		billedCost := float32(item.Amount) * prorateRatio
		usageQuantity := float32(item.Units) * prorateRatio

		cost := &pb.CustomCost{
			Zone:           item.Region,
			AccountName:    invoice.CustomerID,
			ChargeCategory: "usage",
			Description:    item.Description,
			ResourceName:   item.UsageType,
			ResourceType:   item.ProductGroup,
			Id:             invoice.InvoiceID,
			ProviderId:     providerID,
			Labels: map[string]string{
				"product_line":       item.ProductLine,
				"product_name":       item.ProductName,
				"credit_coupon_code": item.CreditCouponCode,
			},
			ListCost:      billedCost,
			ListUnitPrice: float32(item.Rate),
			BilledCost:    billedCost,
			UsageQuantity: usageQuantity,
			UsageUnit:     getUsageUnit(item.UsageType),
		}

		costs = append(costs, cost)
	}

	return costs
}

func getUsageUnit(usageType string) string {
	// Map common usage types to units
	usageType = strings.ToLower(usageType)
	if strings.Contains(usageType, "bandwidth") {
		return "GB"
	}
	if strings.Contains(usageType, "request") {
		return "requests"
	}
	if strings.Contains(usageType, "compute") {
		return "hours"
	}
	return "units"
}

func boilerplateFastlyCustomCost(win opencost.Window) pb.CustomCostResponse {
	return pb.CustomCostResponse{
		Metadata:   map[string]string{"api_client_version": "v3"},
		CostSource: "billing",
		Domain:     "fastly",
		Version:    "v1",
		Currency:   "USD",
		Start:      timestamppb.New(*win.Start()),
		End:        timestamppb.New(*win.End()),
		Errors:     []string{},
		Costs:      []*pb.CustomCost{},
	}
}

func main() {
	// Check command line args first
	configFile := ""
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	// If no command line args, try environment variable or default
	if configFile == "" {
		configFile = os.Getenv("FASTLY_PLUGIN_CONFIG_FILE")
		if configFile == "" {
			configFile = "/opt/opencost/plugin/fastlyconfig.json"
		}
	}

	fastlyConfig, err := getFastlyConfig(configFile)
	if err != nil {
		log.Fatalf("error building Fastly config: %v", err)
	}
	log.SetLogLevel(fastlyConfig.LogLevel)

	// Fastly rate limiting - be conservative
	rateLimiter := rate.NewLimiter(rate.Every(time.Second), 10)

	fastlyCostSrc := FastlyCostSource{
		apiKey:       fastlyConfig.FastlyAPIKey,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		rateLimiter:  rateLimiter,
		invoiceCache: make(map[string][]fastlyplugin.Invoice),
	}

	// pluginMap is the map of plugins we can dispense.
	var pluginMap = map[string]plugin.Plugin{
		"CustomCostSource": &ocplugin.CustomCostPlugin{Impl: &fastlyCostSrc},
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
		return nil, fmt.Errorf("error marshaling json into Fastly config %v", err)
	}

	if result.LogLevel == "" {
		result.LogLevel = "info"
	}

	return &result, nil
}
