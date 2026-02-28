package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"

	"github.com/accelbench/accelbench/internal/database"
)

func main() {
	ctx := context.Background()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	regions := strings.Split(getEnv("PRICING_REGIONS", "us-east-2"), ",")
	for i := range regions {
		regions[i] = strings.TrimSpace(regions[i])
	}

	repo, err := database.NewRepository(ctx, dbURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer repo.Close()

	// AWS Pricing API is only available in us-east-1.
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("us-east-1"))
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}
	client := pricing.NewFromConfig(cfg)

	instanceTypes, err := repo.ListInstanceTypes(ctx)
	if err != nil {
		log.Fatalf("list instance types: %v", err)
	}
	log.Printf("Found %d instance types in database", len(instanceTypes))

	today := time.Now().Format("2006-01-02")

	for _, region := range regions {
		var updated int
		for _, it := range instanceTypes {
			onDemand, res1yr, res3yr, err := fetchPricing(ctx, client, it.Name, region)
			if err != nil {
				log.Printf("WARN: %s in %s: %v", it.Name, region, err)
				time.Sleep(200 * time.Millisecond)
				continue
			}

			p := &database.Pricing{
				InstanceTypeID:       it.ID,
				Region:               region,
				OnDemandHourlyUSD:    onDemand,
				Reserved1YrHourlyUSD: res1yr,
				Reserved3YrHourlyUSD: res3yr,
				EffectiveDate:        today,
			}
			if err := repo.UpsertPricing(ctx, p); err != nil {
				log.Printf("WARN: upsert %s in %s: %v", it.Name, region, err)
			} else {
				updated++
			}
			time.Sleep(200 * time.Millisecond)
		}
		log.Printf("Updated pricing for %d/%d instance types in %s", updated, len(instanceTypes), region)
	}

	log.Printf("Pricing refresh complete for regions: %s", strings.Join(regions, ", "))
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// fetchPricing calls the AWS Pricing API for a single instance type and region,
// returning on-demand hourly, 1yr RI (All Upfront), and 3yr RI (All Upfront) rates.
func fetchPricing(ctx context.Context, client *pricing.Client, instanceType, region string) (onDemand float64, res1yr, res3yr *float64, err error) {
	input := &pricing.GetProductsInput{
		ServiceCode: strPtr("AmazonEC2"),
		Filters: []pricingtypes.Filter{
			{Type: pricingtypes.FilterTypeTermMatch, Field: strPtr("instanceType"), Value: strPtr(instanceType)},
			{Type: pricingtypes.FilterTypeTermMatch, Field: strPtr("operatingSystem"), Value: strPtr("Linux")},
			{Type: pricingtypes.FilterTypeTermMatch, Field: strPtr("tenancy"), Value: strPtr("Shared")},
			{Type: pricingtypes.FilterTypeTermMatch, Field: strPtr("preInstalledSw"), Value: strPtr("NA")},
			{Type: pricingtypes.FilterTypeTermMatch, Field: strPtr("capacitystatus"), Value: strPtr("Used")},
			{Type: pricingtypes.FilterTypeTermMatch, Field: strPtr("regionCode"), Value: strPtr(region)},
		},
		MaxResults: int32Ptr(10),
	}

	resp, err := client.GetProducts(ctx, input)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("GetProducts: %w", err)
	}
	if len(resp.PriceList) == 0 {
		return 0, nil, nil, fmt.Errorf("no pricing found for %s in %s", instanceType, region)
	}

	// Parse the first price list entry.
	var product priceDoc
	if err := json.Unmarshal([]byte(resp.PriceList[0]), &product); err != nil {
		return 0, nil, nil, fmt.Errorf("parse price list: %w", err)
	}

	// Extract on-demand price.
	onDemand, err = extractOnDemand(product.Terms.OnDemand)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("on-demand: %w", err)
	}

	// Extract reserved prices.
	res1yr = extractReserved(product.Terms.Reserved, "1yr")
	res3yr = extractReserved(product.Terms.Reserved, "3yr")

	return onDemand, res1yr, res3yr, nil
}

// priceDoc represents the relevant structure of an AWS Pricing API response entry.
type priceDoc struct {
	Terms struct {
		OnDemand map[string]termEntry `json:"OnDemand"`
		Reserved map[string]termEntry `json:"Reserved"`
	} `json:"terms"`
}

type termEntry struct {
	PriceDimensions map[string]priceDimension `json:"priceDimensions"`
	TermAttributes  map[string]string         `json:"termAttributes"`
}

type priceDimension struct {
	Unit         string            `json:"unit"`
	PricePerUnit map[string]string `json:"pricePerUnit"`
}

func extractOnDemand(terms map[string]termEntry) (float64, error) {
	for _, term := range terms {
		for _, pd := range term.PriceDimensions {
			if pd.Unit == "Hrs" {
				usd, ok := pd.PricePerUnit["USD"]
				if !ok {
					continue
				}
				return strconv.ParseFloat(usd, 64)
			}
		}
	}
	return 0, fmt.Errorf("no hourly on-demand price found")
}

// extractReserved finds the All Upfront, Standard reserved price for the given
// lease length ("1yr" or "3yr") and returns the effective hourly rate.
func extractReserved(terms map[string]termEntry, lease string) *float64 {
	for _, term := range terms {
		attrs := term.TermAttributes
		if attrs["LeaseContractLength"] != lease ||
			attrs["PurchaseOption"] != "All Upfront" ||
			attrs["OfferingClass"] != "standard" {
			continue
		}

		// Find the upfront fee (unit = "Quantity").
		for _, pd := range term.PriceDimensions {
			if pd.Unit == "Quantity" {
				usd, ok := pd.PricePerUnit["USD"]
				if !ok {
					continue
				}
				upfront, err := strconv.ParseFloat(usd, 64)
				if err != nil || upfront <= 0 {
					continue
				}
				var hours float64
				switch lease {
				case "1yr":
					hours = 8760
				case "3yr":
					hours = 26280
				}
				hourly := upfront / hours
				return &hourly
			}
		}
	}
	return nil
}

func strPtr(s string) *string   { return &s }
func int32Ptr(i int32) *int32   { return &i }
