// Package bisqroute defines the private, server-to-server Bisq re-quote input.
package bisqroute

type Request struct {
	OfferID             string `json:"offer_id"`
	Provider            string `json:"provider"`
	TradeMode           string `json:"trade_mode"`
	FiatCurrency        string `json:"fiat_currency"`
	TradeAmountSats     uint64 `json:"trade_amount_sats"`
	TotalToPaySats      uint64 `json:"total_to_pay_sats"`
	SecurityDepositSats uint64 `json:"security_deposit_sats"`
	TakerFeeSats        uint64 `json:"taker_fee_sats"`
	MiningFeeSats       uint64 `json:"mining_fee_sats"`
	VolumeAsLong        int64  `json:"volume_as_long"`
	FundingFeeCapSats   uint64 `json:"funding_fee_cap_sats"`
}
