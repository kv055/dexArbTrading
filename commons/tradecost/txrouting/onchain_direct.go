package routing

// onChainDirect withdraws BTC straight from the CEX to a final destination that we
// control or can whitelist (our Bisq daemon wallet on a bid, or our own CEX deposit
// address on an ask). A single on-chain transaction, no hub, no swap.
type onChainDirect struct{ eng *Engine }

func (onChainDirect) Kind() Kind { return MethodOnChainDirect }

func (m onChainDirect) Cost(in Input) Cost {
	return m.eng.onchainCost(MethodOnChainDirect, in, 1)
}
