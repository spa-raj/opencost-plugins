package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/opencost/opencost/core/pkg/log"
	"github.com/opencost/opencost/core/pkg/model/pb"
	"google.golang.org/protobuf/encoding/protojson"
)

// Validator for Fastly plugin integration tests
func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: validator <path-to-daily-protobuf-file> <path-to-hourly-protobuf-file>")
		os.Exit(1)
	}

	dailyProtobufFilePath := os.Args[1]
	hourlyProtobufFilePath := os.Args[2]

	// Read and validate daily data
	dailyData, err := os.ReadFile(dailyProtobufFilePath)
	if err != nil {
		fmt.Printf("Error reading daily protobuf file: %v\n", err)
		os.Exit(1)
	}

	dailyCustomCostResponses, err := Unmarshal(dailyData)
	if err != nil {
		fmt.Printf("Error unmarshalling daily protobuf data: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully unmarshalled %d daily custom cost responses\n", len(dailyCustomCostResponses))

	// Read and validate hourly data
	hourlyData, err := os.ReadFile(hourlyProtobufFilePath)
	if err != nil {
		fmt.Printf("Error reading hourly protobuf file: %v\n", err)
		os.Exit(1)
	}

	hourlyCustomCostResponses, err := Unmarshal(hourlyData)
	if err != nil {
		fmt.Printf("Error unmarshalling hourly protobuf data: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully unmarshalled %d hourly custom cost responses\n", len(hourlyCustomCostResponses))

	// Validate the responses
	isValid := validate(dailyCustomCostResponses, hourlyCustomCostResponses)
	if !isValid {
		os.Exit(1)
	} else {
		fmt.Println("Validation successful")
	}
}

func validate(respDaily, respHourly []*pb.CustomCostResponse) bool {
	if len(respDaily) == 0 {
		log.Errorf("no daily response received from fastly plugin")
		return false
	}

	if len(respHourly) == 0 {
		log.Errorf("no hourly response received from fastly plugin")
		return false
	}

	var multiErr error

	// Check for errors in responses
	for _, resp := range respDaily {
		if len(resp.Errors) > 0 {
			multiErr = multierror.Append(multiErr, fmt.Errorf("errors occurred in daily response: %v", resp.Errors))
		}
	}

	for _, resp := range respHourly {
		if len(resp.Errors) > 0 {
			multiErr = multierror.Append(multiErr, fmt.Errorf("errors occurred in hourly response: %v", resp.Errors))
		}
	}

	if multiErr != nil {
		log.Errorf("Errors occurred during plugin testing for fastly: %v", multiErr)
		return false
	}

	// Validate daily costs
	seenCosts := map[string]bool{}
	totalDailyCost := float32(0.0)

	for _, resp := range respDaily {
		// Skip empty responses for recent dates
		if len(resp.Costs) == 0 && resp.Start.AsTime().After(time.Now().Truncate(24*time.Hour).Add(-1*time.Minute)) {
			log.Debugf("today's daily costs returned by plugin fastly are empty, skipping: %v", resp)
			continue
		}

		for _, cost := range resp.Costs {
			totalDailyCost += cost.GetBilledCost()
			seenCosts[cost.GetResourceName()] = true

			// Validate cost sanity
			if cost.GetBilledCost() < 0 {
				log.Errorf("negative cost found for %v", cost)
				return false
			}

			// Check for reasonable cost values (adjust based on your expected ranges)
			if cost.GetBilledCost() > 10000 {
				log.Errorf("unexpectedly high daily cost for %v: %f", cost.GetResourceName(), cost.GetBilledCost())
				return false
			}
		}
	}

	// Validate we have some expected cost types
	expectedCosts := []string{
		"cdn_bandwidth",
		"cdn_requests",
		// Add more expected cost types as needed
	}

	foundExpectedCost := false
	for _, expectedCost := range expectedCosts {
		if seenCosts[expectedCost] {
			foundExpectedCost = true
			break
		}
	}

	if !foundExpectedCost && totalDailyCost > 0 {
		log.Warnf("None of the expected cost types found, but costs exist. Seen costs: %v", seenCosts)
	}

	// Validate domain
	for _, resp := range respDaily {
		if resp.Domain != "fastly" {
			log.Errorf("daily domain returned by plugin does not match expected 'fastly': %s", resp.Domain)
			return false
		}
	}

	// Validate hourly costs
	seenHourlyCosts := map[string]bool{}
	totalHourlyCost := float32(0.0)

	for _, resp := range respHourly {
		for _, cost := range resp.Costs {
			seenHourlyCosts[cost.GetResourceName()] = true
			totalHourlyCost += cost.GetBilledCost()

			if cost.GetBilledCost() < 0 {
				log.Errorf("negative hourly cost found for %v", cost)
				return false
			}

			// Hourly costs should be smaller than daily
			if cost.GetBilledCost() > 1000 {
				log.Errorf("unexpectedly high hourly cost for %v: %f", cost.GetResourceName(), cost.GetBilledCost())
				return false
			}
		}
	}

	// Basic sanity check - if we have daily costs, we should have hourly costs
	if totalDailyCost > 0 && totalHourlyCost == 0 {
		log.Errorf("daily costs exist but no hourly costs found")
		return false
	}

	log.Infof("Validation passed. Daily costs: %f, Hourly costs: %f", totalDailyCost, totalHourlyCost)
	log.Infof("Cost types seen - Daily: %v, Hourly: %v", seenCosts, seenHourlyCosts)

	return true
}

func Unmarshal(data []byte) ([]*pb.CustomCostResponse, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	protoResps := make([]*pb.CustomCostResponse, len(raw))
	for i, r := range raw {
		p := &pb.CustomCostResponse{}
		if err := protojson.Unmarshal(r, p); err != nil {
			return nil, err
		}
		protoResps[i] = p
	}

	return protoResps, nil
}
