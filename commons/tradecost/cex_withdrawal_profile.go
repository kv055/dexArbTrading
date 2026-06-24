package tradecost

import routing "commons/tradecost/txrouting"

// CexWithdrawalProfile describes the on-chain withdrawal characteristics of a CEX,
// used to build accurate transaction-size estimates for the bitcoin fee predictor.
// The CEX no longer gates which routing method is used — that is decided per DEX and
// per offer side by the routing package — so this profile only carries tx shape.
type CexWithdrawalProfile struct {
	// OnchainTxType is the Bitcoin script type the exchange uses for on-chain
	// withdrawals. Drives the virtual-size calculation in the fee predictor.
	// Supported values: "P2WPKH", "P2WSH", "P2TR", "P2PKH", "P2SH".
	OnchainTxType string
	// TypicalInputs is the expected number of inputs in a withdrawal transaction.
	// Most hot-wallet withdrawals use a single consolidated UTXO.
	TypicalInputs int
	// TypicalOutputs is the expected number of outputs (destination + change).
	// Standard CEX withdrawals produce exactly 2 outputs.
	TypicalOutputs int
}

// txShape converts the profile into the routing package's tx-shape input.
func (p CexWithdrawalProfile) txShape() routing.TxShape {
	return routing.TxShape{
		TxType:  p.OnchainTxType,
		Inputs:  p.TypicalInputs,
		Outputs: p.TypicalOutputs,
	}
}

// cexProfiles maps exchange name → on-chain withdrawal tx shape.
// Add new exchanges here — no other code changes are needed.
var cexProfiles = map[string]CexWithdrawalProfile{
	// Kraken: native SegWit (bech32).
	"kraken": {OnchainTxType: "P2WPKH", TypicalInputs: 1, TypicalOutputs: 2},
	// Binance: native SegWit (bech32).
	"binance": {OnchainTxType: "P2WPKH", TypicalInputs: 1, TypicalOutputs: 2},
	// Coinbase: native SegWit (bech32).
	"coinbase": {OnchainTxType: "P2WPKH", TypicalInputs: 1, TypicalOutputs: 2},
	// Bitstamp: native SegWit (bech32).
	"bitstamp": {OnchainTxType: "P2WPKH", TypicalInputs: 1, TypicalOutputs: 2},
}

// defaultCexProfile is used for exchanges not in cexProfiles.
var defaultCexProfile = CexWithdrawalProfile{OnchainTxType: "P2WPKH", TypicalInputs: 1, TypicalOutputs: 2}

// cexProfileFor returns the withdrawal profile for the given exchange,
// falling back to defaultCexProfile for unknown exchanges.
func cexProfileFor(exchange string) CexWithdrawalProfile {
	if p, ok := cexProfiles[exchange]; ok {
		return p
	}
	return defaultCexProfile
}

// ProfileFor returns the withdrawal profile for the given exchange, falling
// back to the default profile for unknown exchanges.
func ProfileFor(exchange string) CexWithdrawalProfile {
	return cexProfileFor(exchange)
}

// DexRouting describes the routing method used for each side of a DEX's offers.
type DexRouting struct {
	DEX string `json:"dex"`
	Bid string `json:"bid"`
	Ask string `json:"ask"`
}

// DexRoutingProfiles returns the per-DEX bid/ask routing method mapping, for display.
func DexRoutingProfiles() []DexRouting {
	dexes := routing.SupportedDEXes()
	out := make([]DexRouting, 0, len(dexes))
	for _, dex := range dexes {
		p := routing.ProfileFor(dex)
		out = append(out, DexRouting{DEX: dex, Bid: string(p.Bid), Ask: string(p.Ask)})
	}
	return out
}
