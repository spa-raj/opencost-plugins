# Fastly Invoice – FOCUS Attribute Mapping

| FOCUS custom‑cost attribute | Fastly field | Notes |
|---|---|---|
| BillingPeriodStart | `billing_start_date` | Billing cycle start (ISO‑8601). |
| BillingPeriodEnd | `billing_end_date` | Billing cycle end (ISO‑8601). |
| AccountId | `customer_id` | Fastly customer (account) ID. |
| Subcategory | `product_line` | Functional grouping, e.g. “Network Services”. |
| CommitmentDiscountId | `credit_coupon_code` | Discount / credit coupon code. |
| EffectiveCost | `amount` (line‑item) or `monthly_transaction_amount` (invoice total) | Map depending on desired granularity. |
| ServiceCategory | `product_group` | Broad service family, e.g. “Compute”. |
| ServiceName | `product_name` | Specific product name. |
| PricingQuantity | `units` | Usage quantity. |
| PricingUnit | `usage_type` | Unit of measure (requests, bandwidth, etc.). |
| PricingCategory | `product_group` | Geographic vs. functional category. |
