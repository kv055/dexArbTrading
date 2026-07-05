package paymentmethods

// Payment method category IDs used in API payloads and dexMonitor grouping.
// The display names below are also the exact values stored in the
// payment_methods.category column and hardcoded in the frontend.
const (
	CategoryCash            = "cash"
	CategoryBankTransfer    = "bank_transfer"
	CategoryFiatTransmitter = "fiat_transmitter"
	CategoryGiftCards       = "gift_cards"
	CategoryCrypto          = "crypto"
	CategoryOthers          = "others"
)

// CanonicalNationalBank is the single canonical name for traditional domestic
// bank accounts (implicit SEPA for EUR).
const CanonicalNationalBank = "National Bank"

// categoryOrder defines display order for grouped categories.
var categoryOrder = []string{
	CategoryCash,
	CategoryBankTransfer,
	CategoryFiatTransmitter,
	CategoryGiftCards,
	CategoryCrypto,
	CategoryOthers,
}

// categoryDisplayNames maps category IDs to human-readable labels. These
// labels are the DB values of payment_methods.category, byte-for-byte.
var categoryDisplayNames = map[string]string{
	CategoryCash:            "Cash",
	CategoryBankTransfer:    "Bank Transfer",
	CategoryFiatTransmitter: "Fiat Transmitter",
	CategoryGiftCards:       "Gift Cards",
	CategoryCrypto:          "Crypto",
	CategoryOthers:          "Others",
}

// categoryIDsByDisplay is the inverse of categoryDisplayNames.
var categoryIDsByDisplay = func() map[string]string {
	m := make(map[string]string, len(categoryDisplayNames))
	for id, display := range categoryDisplayNames {
		m[display] = id
	}
	return m
}()

// CategoryDisplayName returns the UI/DB label for a category ID.
func CategoryDisplayName(category string) string {
	if name, ok := categoryDisplayNames[category]; ok {
		return name
	}
	return category
}

// CategoryIDFromDisplay maps a payment_methods.category value back to its
// category ID. Unknown values map to CategoryOthers.
func CategoryIDFromDisplay(display string) string {
	if id, ok := categoryIDsByDisplay[display]; ok {
		return id
	}
	return CategoryOthers
}

// CategoryDisplayNames returns the six category labels in display order —
// the allowed values of payment_methods.category.
func CategoryDisplayNames() []string {
	out := make([]string, 0, len(categoryOrder))
	for _, id := range categoryOrder {
		out = append(out, categoryDisplayNames[id])
	}
	return out
}

// CategoryOrder returns the stable category ordering for API responses.
func CategoryOrder() []string {
	out := make([]string, len(categoryOrder))
	copy(out, categoryOrder)
	return out
}
