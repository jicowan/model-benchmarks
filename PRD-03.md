# PRD-03: EC2 Pricing Integration

## Problem

The Compare page calculates cost-efficiency metrics (hourly cost, cost/request, cost/1M tokens) but relies on hardcoded placeholder pricing for only 5 of 26 instance types. Most instances — including commonly used ones like g5.2xlarge, g6.xlarge, and all trn1/trn2 types — show "--" for all cost columns. The pricing data also grows stale because there is no automated refresh.

The database schema (`pricing` table), Helm CronJob template (`cronjob-pricing.yaml`), and the frontend PricingToggle component already exist, but the pipeline from AWS Pricing API to the frontend is not wired up.

## Goals

1. **Automated daily refresh** of EC2 on-demand and reserved instance pricing from the AWS Pricing API into the `pricing` table
2. **API endpoint** to serve pricing data to the frontend, filtered by region
3. **Frontend integration** so the Compare page shows real pricing for all 26 instance types across all three tiers (on-demand, 1yr RI, 3yr RI)
4. **Upsert semantics** so the CronJob is idempotent and can safely re-run

## Non-Goals

- Spot pricing (volatile, not suitable for benchmark cost comparisons)
- Pricing for non-accelerated instance types
- Historical pricing trends or price-change notifications

## AWS Pricing API

### GetProducts endpoint

```
Region: us-east-1 (Pricing API is only available in us-east-1 and ap-south-1)
Service: AmazonEC2
```

Filters used per instance type:
- `instanceType` — e.g. "g5.2xlarge"
- `operatingSystem` — "Linux"
- `tenancy` — "Shared"
- `preInstalledSw` — "NA"
- `capacitystatus` — "Used"
- `regionCode` — target region (e.g. "us-east-2")

Response contains a JSON-encoded `PriceList` with terms:
- `terms.OnDemand` — on-demand hourly USD
- `terms.Reserved` — reserved pricing with `LeaseContractLength` ("1yr" or "3yr"), `PurchaseOption` ("All Upfront"), and `OfferingClass` ("standard")

### Reserved pricing calculation

For "All Upfront" reserved instances, the effective hourly rate is:

```
effective_hourly = (upfront_fee / (365 * hours_per_year)) / contract_years
```

Where the upfront fee is in `priceDimensions` with `unit: "Quantity"` and the hourly rate component is `$0.00` (since it's all prepaid).

More precisely:
- 1yr All Upfront: `upfront / 8760`
- 3yr All Upfront: `upfront / 26280`

### Rate limiting

The Pricing API has no published rate limit but is throttled. The CronJob should process instance types sequentially with a brief pause between calls (e.g. 200ms) to avoid throttling.

## Database

### Existing schema (001_initial.sql)

```sql
CREATE TABLE pricing (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_type_id UUID REFERENCES instance_types(id) NOT NULL,
    region TEXT NOT NULL,
    on_demand_hourly_usd NUMERIC NOT NULL,
    reserved_1yr_hourly_usd NUMERIC,
    reserved_3yr_hourly_usd NUMERIC,
    effective_date DATE NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now()
);
```

### New migration: 003_pricing_unique_constraint.sql

Add a unique constraint to support `ON CONFLICT` upserts:

```sql
CREATE UNIQUE INDEX idx_pricing_instance_region_date
    ON pricing (instance_type_id, region, effective_date);
```

### New repository methods

| Method | Signature | Description |
|--------|-----------|-------------|
| `UpsertPricing` | `(ctx, p *Pricing) error` | INSERT ... ON CONFLICT (instance_type_id, region, effective_date) DO UPDATE |
| `ListPricing` | `(ctx, region string) ([]PricingRow, error)` | JOIN pricing + instance_types, returns instance name + all 3 price tiers |
| `ListInstanceTypes` | `(ctx) ([]InstanceType, error)` | Returns all instance types (needed by the CronJob to iterate) |

### PricingRow (new type for API response)

```go
type PricingRow struct {
    InstanceTypeName     string   `json:"instance_type_name"`
    OnDemandHourlyUSD    float64  `json:"on_demand_hourly_usd"`
    Reserved1YrHourlyUSD *float64 `json:"reserved_1yr_hourly_usd,omitempty"`
    Reserved3YrHourlyUSD *float64 `json:"reserved_3yr_hourly_usd,omitempty"`
    EffectiveDate        string   `json:"effective_date"`
}
```

## API

### GET /api/v1/pricing

Query parameters:
- `region` (optional, default: `us-east-2`)

Response: `200 OK` with `PricingRow[]`

```json
[
  {
    "instance_type_name": "g5.xlarge",
    "on_demand_hourly_usd": 1.006,
    "reserved_1yr_hourly_usd": 0.64,
    "reserved_3yr_hourly_usd": 0.43,
    "effective_date": "2026-02-27"
  },
  ...
]
```

## CronJob: pricingrefresh

### Binary: cmd/pricingrefresh/main.go

Behavior:
1. Load AWS config (default credential chain) with region `us-east-1` (Pricing API region)
2. Connect to PostgreSQL via `DATABASE_URL`
3. Read `PRICING_REGIONS` env var (default: `us-east-2`) — comma-separated list of EC2 regions to fetch pricing for
4. Fetch all instance types from the DB via `ListInstanceTypes()`
5. For each region in the list:
   a. For each instance type:
      - Call `pricing.GetProducts()` with filters for that instance type + target region
      - Parse the on-demand hourly price from `terms.OnDemand`
      - Parse reserved 1yr and 3yr All Upfront prices from `terms.Reserved`
      - Call `UpsertPricing()` with today's date as effective_date
      - Sleep 200ms between API calls
   b. Log summary: "Updated pricing for N instance types in region R"
6. Log total summary across all regions

### Helm CronJob (already exists)

`helm/accelbench/templates/cronjob-pricing.yaml` — runs daily at 03:00 UTC with the `tools` image. No template changes needed; just the binary implementation.

### Environment variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `DATABASE_URL` | Yes | — | PostgreSQL connection string |
| `PRICING_REGIONS` | No | `us-east-2` | Comma-separated list of EC2 regions to fetch pricing for |

## Frontend

### New type: PricingRow (types.ts)

```typescript
export interface PricingRow {
  instance_type_name: string;
  on_demand_hourly_usd: number;
  reserved_1yr_hourly_usd?: number;
  reserved_3yr_hourly_usd?: number;
  effective_date: string;
}
```

### New API function: listPricing (api.ts)

```typescript
export async function listPricing(region?: string): Promise<PricingRow[]>
```

### Compare.tsx changes

1. Remove the `PLACEHOLDER_PRICING` constant and the `getPrice()` helper
2. Add a **region selector dropdown** (default: `us-east-2`) alongside the existing PricingToggle
3. Add state for `region` and `pricingMap: Map<string, PricingRow>`
4. Add a `useEffect` that calls `listPricing(region)` on mount and whenever `region` changes, storing results in state
5. Replace `getPrice(instance, tier)` with a lookup into the pricing map:
   - `on_demand` → `row.on_demand_hourly_usd`
   - `reserved_1yr` → `row.reserved_1yr_hourly_usd`
   - `reserved_3yr` → `row.reserved_3yr_hourly_usd`
6. The existing `PricingToggle`, cost table rows, and formulas remain unchanged

### Region selector

A simple `<select>` dropdown with a predefined list of common AWS regions. The selected region is passed to `listPricing()`. Default: `us-east-2`.

## Dependencies

### Go modules to add

- `github.com/aws/aws-sdk-go-v2/config` — AWS credential and config loading
- `github.com/aws/aws-sdk-go-v2/service/pricing` — AWS Pricing API client

### No new frontend dependencies

The frontend already uses `fetch` for API calls; no additional packages needed.

## Files to create/modify

| File | Action |
|------|--------|
| `go.mod` / `go.sum` | Edit — add aws-sdk-go-v2 deps |
| `db/migrations/003_pricing_unique_constraint.sql` | **Create** — unique index for upsert |
| `internal/database/pricing.go` | **Create** — UpsertPricing, ListPricing, ListInstanceTypes |
| `internal/database/iface.go` | Edit — add 3 pricing methods to Repo interface |
| `internal/database/mock_repo.go` | Edit — add mock implementations |
| `cmd/pricingrefresh/main.go` | Rewrite — AWS Pricing API integration |
| `internal/api/handlers.go` | Edit — add GET /api/v1/pricing route and handler |
| `frontend/src/types.ts` | Edit — add PricingRow interface |
| `frontend/src/api.ts` | Edit — add listPricing() function |
| `frontend/src/pages/Compare.tsx` | Edit — replace PLACEHOLDER_PRICING with API call |

## Verification

1. Run `pricingrefresh` locally with valid AWS credentials → pricing table has rows for all 26 instance types
2. `curl /api/v1/pricing?region=us-east-2` returns JSON with all instance types and prices
3. Compare page shows dollar values for all instance types across all 3 pricing tiers
4. Re-running `pricingrefresh` updates existing rows (upsert) rather than creating duplicates
5. `go build ./...` succeeds with no errors
