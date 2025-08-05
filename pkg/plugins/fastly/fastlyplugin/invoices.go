package fastlyplugin

import "time"

type InvoiceListResponse struct {
	Data []Invoice          `json:"data"`
	Meta PaginationMetadata `json:"meta"`
}

type Invoice struct {
	CustomerID               string                `json:"customer_id"`
	InvoiceID                string                `json:"invoice_id"`
	InvoicePostedOn          string                `json:"invoice_posted_on"`
	BillingStartDate         string                `json:"billing_start_date"`
	BillingEndDate           string                `json:"billing_end_date"`
	StatementNumber          string                `json:"statement_number"`
	CurrencyCode             string                `json:"currency_code"`
	MonthlyTransactionAmount string                `json:"monthly_transaction_amount"`
	PaymentStatus            string                `json:"payment_status,omitempty"`
	TransactionLineItems     []TransactionLineItem `json:"transaction_line_items"`
}

type TransactionLineItem struct {
	Description      string  `json:"description"`
	Amount           float64 `json:"amount"`
	CreditCouponCode string  `json:"credit_coupon_code"`
	Rate             float64 `json:"rate"`
	Units            float64 `json:"units"`
	ProductName      string  `json:"product_name"`
	ProductGroup     string  `json:"product_group"`
	ProductLine      string  `json:"product_line"`
	Region           string  `json:"region"`
	UsageType        string  `json:"usage_type"`
}

type PaginationMetadata struct {
	NextCursor string `json:"next_cursor"`
	Limit      int    `json:"limit"`
	Total      int    `json:"total"`
	Sort       string `json:"sort"`
}

// Helper function to parse date strings
func ParseFastlyDate(dateStr string) (time.Time, error) {
	return time.Parse(time.RFC3339, dateStr)
}
