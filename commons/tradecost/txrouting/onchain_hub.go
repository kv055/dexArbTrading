package routing

// onChainHubWallet withdraws BTC from the CEX into our own LND on-chain wallet (a
// hub we control and can whitelist at the CEX), then forwards it on-chain to the
// final destination. Used when that destination changes every trade and cannot be
// pre-whitelisted at the CEX (Bisq2 / HodlHodl / Peach counterparty/escrow address).
//
// Two on-chain transactions: the CEX withdrawal and our forwarding spend. Both are
// native-segwit single-hop sends, so they are priced as 2× the per-tx fee.
type onChainHubWallet struct{ eng *Engine }

func (onChainHubWallet) Kind() Kind { return MethodOnChainHubWallet }

func (m onChainHubWallet) Cost(in Input) Cost {
	return m.eng.onchainCost(MethodOnChainHubWallet, in, 2)
}
