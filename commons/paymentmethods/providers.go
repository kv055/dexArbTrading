package paymentmethods

// Provider describes one canonical payment method and how raw labels map to it.
type Provider struct {
	ID        string
	Canonical string
	Category  string
	Aliases   []string
	Patterns  []string
}

// providers is ordered for matching priority: longer / more specific entries first.
var providers = []Provider{
	// Bank rails — specific before generic
	{ID: "sepa_instant", Canonical: "SEPA Instant", Category: CategoryBankTransfer, Aliases: []string{
		"sepa instant", "instant sepa", "sepainstant",
	}, Patterns: []string{`(?i)\bsepa[\s\-/]*instant\b`}},
	{ID: "sepa", Canonical: "SEPA", Category: CategoryBankTransfer, Aliases: []string{"sepa"}},
	{ID: "national_bank", Canonical: CanonicalNationalBank, Category: CategoryBankTransfer, Aliases: []string{
		"national bank", "national banks", "national_bank", "national transfer",
		"any national bank",
	}},
	{ID: "same_bank", Canonical: "Same Bank", Category: CategoryBankTransfer, Aliases: []string{"same bank"}},
	{ID: "swift", Canonical: "SWIFT", Category: CategoryBankTransfer, Aliases: []string{"swift", "wire", "bank transfer"}},
	{ID: "faster_payments", Canonical: "Faster Payments", Category: CategoryBankTransfer, Aliases: []string{
		"faster payments", "faster payment",
	}},
	{ID: "cash_deposit", Canonical: "Cash Deposit", Category: CategoryBankTransfer, Aliases: []string{"cash deposit"}},
	{ID: "ach", Canonical: "ACH", Category: CategoryBankTransfer, Aliases: []string{"ach"}},
	{ID: "interac", Canonical: "Interac", Category: CategoryBankTransfer, Aliases: []string{"interac"}},

	// PayPal F&F before plain PayPal
	{ID: "paypal_ff", Canonical: "PayPal F&F", Category: CategoryFiatTransmitter, Aliases: []string{
		"paypal friends & family", "paypal friends and family", "paypal f&f",
		"f&f", "frnds&fam", "frnd&fam", "(frnds&fam)", "(frnd&fam)",
		"paypal(frnd&fam)", "paypal(frnds&fam)",
	}, Patterns: []string{`(?i)paypal\s*(f&?f|friends)`}},
	{ID: "paypal", Canonical: "PayPal", Category: CategoryFiatTransmitter, Aliases: []string{
		"paypal", "pay pal", "paypall",
	}, Patterns: []string{`(?i)\bpay\s*pal+l?\b`}},

	// Money transmitters / e-wallets (multi-rail)
	{ID: "revolut", Canonical: "Revolut", Category: CategoryFiatTransmitter, Aliases: []string{
		"revolut",
	}, Patterns: []string{`(?i)\brevou?l?t\b`}},
	{ID: "wise", Canonical: "Wise", Category: CategoryFiatTransmitter, Aliases: []string{
		"wise", "transferwise", "transfer wise",
	}},
	{ID: "n26", Canonical: "N26", Category: CategoryFiatTransmitter, Aliases: []string{"n26"}},
	{ID: "bizum", Canonical: "Bizum", Category: CategoryFiatTransmitter, Aliases: []string{"bizum"}},
	{ID: "strike", Canonical: "Strike", Category: CategoryFiatTransmitter, Aliases: []string{"strike"}},
	{ID: "mb_way", Canonical: "MB Way", Category: CategoryFiatTransmitter, Aliases: []string{"mb way", "mbway"}},
	{ID: "paysera", Canonical: "Paysera", Category: CategoryFiatTransmitter, Aliases: []string{"paysera"}},
	{ID: "vivid", Canonical: "Vivid", Category: CategoryFiatTransmitter, Aliases: []string{
		"vivid", "vivid money",
	}},
	{ID: "skrill", Canonical: "Skrill", Category: CategoryFiatTransmitter, Aliases: []string{"skrill"}},
	{ID: "neteller", Canonical: "Neteller", Category: CategoryFiatTransmitter, Aliases: []string{"neteller"}},
	{ID: "satispay", Canonical: "Satispay", Category: CategoryFiatTransmitter, Aliases: []string{"satispay"}},
	{ID: "blik", Canonical: "BLIK", Category: CategoryFiatTransmitter, Aliases: []string{"blik"}},
	{ID: "lydia", Canonical: "Lydia", Category: CategoryFiatTransmitter, Aliases: []string{"lydia"}},
	{ID: "bunq", Canonical: "Bunq", Category: CategoryFiatTransmitter, Aliases: []string{"bunq"}},
	{ID: "monese", Canonical: "Monese", Category: CategoryFiatTransmitter, Aliases: []string{"monese"}},
	{ID: "wero", Canonical: "Wero", Category: CategoryFiatTransmitter, Aliases: []string{"wero"}},
	{ID: "postepay", Canonical: "Postepay", Category: CategoryFiatTransmitter, Aliases: []string{"postepay"}},
	{ID: "moneybeam", Canonical: "MoneyBeam", Category: CategoryFiatTransmitter, Aliases: []string{
		"moneybeam", "money beam", "moneybeam (n26)",
	}},
	{ID: "western_union", Canonical: "Western Union", Category: CategoryFiatTransmitter, Aliases: []string{
		"western union", "western union (cash)",
	}},
	{ID: "moneygram", Canonical: "Moneygram", Category: CategoryFiatTransmitter, Aliases: []string{
		"moneygram", "money gram",
	}},
	{ID: "remitly", Canonical: "Remitly", Category: CategoryFiatTransmitter, Aliases: []string{"remitly"}},
	{ID: "worldremit", Canonical: "Worldremit", Category: CategoryFiatTransmitter, Aliases: []string{"worldremit"}},
	{ID: "payoneer", Canonical: "Payoneer", Category: CategoryFiatTransmitter, Aliases: []string{"payoneer"}},
	{ID: "venmo", Canonical: "Venmo", Category: CategoryFiatTransmitter, Aliases: []string{"venmo"}},
	{ID: "cash_app", Canonical: "Cash App", Category: CategoryFiatTransmitter, Aliases: []string{
		"cash app", "cashapp",
	}},
	{ID: "zelle", Canonical: "Zelle", Category: CategoryFiatTransmitter, Aliases: []string{"zelle"}},
	{ID: "twint", Canonical: "Twint", Category: CategoryFiatTransmitter, Aliases: []string{"twint"}},
	{ID: "pix", Canonical: "Pix", Category: CategoryFiatTransmitter, Aliases: []string{"pix"}},
	{ID: "promptpay", Canonical: "PromptPay", Category: CategoryFiatTransmitter, Aliases: []string{"promptpay"}},
	{ID: "gcash", Canonical: "GCash", Category: CategoryFiatTransmitter, Aliases: []string{"gcash"}},
	{ID: "vipps", Canonical: "Vipps", Category: CategoryFiatTransmitter, Aliases: []string{"vipps"}},
	{ID: "apple_pay", Canonical: "Apple Pay", Category: CategoryFiatTransmitter, Aliases: []string{"apple pay"}},
	{ID: "google_pay", Canonical: "Google Pay", Category: CategoryFiatTransmitter, Aliases: []string{"google pay"}},
	{ID: "monzo", Canonical: "Monzo", Category: CategoryFiatTransmitter, Aliases: []string{
		"monzo", "monzo bank",
	}},

	// Gift cards / prepaid
	{ID: "amazon_gift_card", Canonical: "Amazon Gift Card", Category: CategoryGiftCards, Aliases: []string{
		"amazon gift card", "amazon giftcard", "amazon egift card",
		"amazon es giftcard", "amazon it giftcard", "amazon de giftcard",
		"amazon fr giftcard", "amazon uk giftcard", "amazon us giftcard",
	}, Patterns: []string{`(?i)\bamazon\b.*\bgift\s*card`}},
	{ID: "paysafecard", Canonical: "Paysafecard", Category: CategoryGiftCards, Aliases: []string{
		"paysafecard", "paysafe card", "paysafe",
	}, Patterns: []string{`(?i)paysafe[\s-]*card?`}},
	{ID: "neosurf", Canonical: "Neosurf", Category: CategoryGiftCards, Aliases: []string{"neosurf"}},
	{ID: "steam", Canonical: "Steam", Category: CategoryGiftCards, Aliases: []string{"steam"}},
	{ID: "itunes", Canonical: "iTunes", Category: CategoryGiftCards, Aliases: []string{"itunes"}},
	{ID: "google_play", Canonical: "Google Play Card", Category: CategoryGiftCards, Aliases: []string{
		"google play card",
	}},

	// Cash
	{ID: "halcash", Canonical: "HalCash", Category: CategoryCash, Aliases: []string{"halcash", "hal cash"}},
	{ID: "cash_by_mail", Canonical: "Cash by Mail", Category: CategoryCash, Aliases: []string{
		"cash by mail", "cashbymail",
	}},
	{ID: "f2f", Canonical: "F2F", Category: CategoryCash, Aliases: []string{
		"f2f", "face to face", "face-to-face", "in person",
	}},
	{ID: "cash_trade", Canonical: "Cash Trade", Category: CategoryCash, Aliases: []string{"cash trade"}},
}

// allProviders returns fiat/cash/gift providers followed by crypto providers.
// Crypto entries are last so specific coin aliases win over generic labels.
func allProviders() []Provider {
	out := make([]Provider, 0, len(providers)+len(cryptoProviders))
	out = append(out, providers...)
	out = append(out, cryptoProviders...)
	return out
}
