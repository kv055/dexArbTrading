package paymentmethods

// Payment method category names used by dexMonitor grouping.
const (
	CategoryCash             = "cash"
	CategoryBankTransfer     = "bank_transfer"
	CategoryMoneyTransmitter = "money_transmitter"
	CategoryGiftCards        = "gift_cards"
	CategoryCrypto           = "crypto"
	CategoryOther            = "other"
)

// CanonicalNationalBank is the single canonical name for traditional domestic
// bank accounts (implicit SEPA for EUR).
const CanonicalNationalBank = "National Bank"

// categoryOrder defines display order for grouped categories.
var categoryOrder = []string{
	CategoryCash,
	CategoryBankTransfer,
	CategoryMoneyTransmitter,
	CategoryGiftCards,
	CategoryCrypto,
	CategoryOther,
}

// categoryDisplayNames maps category IDs to human-readable labels.
var categoryDisplayNames = map[string]string{
	CategoryCash:             "Cash",
	CategoryBankTransfer:     "Bank Transfer",
	CategoryMoneyTransmitter: "Money Transmitter",
	CategoryGiftCards:        "Gift Cards",
	CategoryCrypto:           "Crypto",
	CategoryOther:            "Other",
}

// CategoryDisplayName returns the UI label for a category ID.
func CategoryDisplayName(category string) string {
	if name, ok := categoryDisplayNames[category]; ok {
		return name
	}
	return category
}

// CategoryOrder returns the stable category ordering for API responses.
func CategoryOrder() []string {
	out := make([]string, len(categoryOrder))
	copy(out, categoryOrder)
	return out
}
