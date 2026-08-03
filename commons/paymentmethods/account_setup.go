package paymentmethods

// RequiredAccountFields maps an accounts.provider value (CHECK-constrained in
// the DB) to the account columns that must be non-empty before that provider
// can be used to pay for an offer. This is account-shape policy; the mapping
// from a payment method to its account lives in the payment_methods table
// (account_id foreign key), managed through the categorization panel.
var RequiredAccountFields = map[string][]string{
	"Revolut": {"payment_tag"},
	"WISE":    {"email"},
	"PayPal":  {"email"},
	"N26":     {},
}
