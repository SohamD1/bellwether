package market

import "testing"

func TestFixtureABIContract(t *testing.T) {
	t.Parallel()

	type inputContract struct {
		name    string
		typ     string
		indexed bool
	}
	tests := []struct {
		name      string
		signature string
		inputs    []inputContract
	}{
		{
			name: "Trade", signature: "Trade(bytes32,address,bool,uint256,uint256,uint256,uint64)",
			inputs: []inputContract{
				{"marketId", "bytes32", true}, {"trader", "address", true},
				{"yes", "bool", false}, {"amount", "uint256", false},
				{"yesReserve", "uint256", false}, {"noReserve", "uint256", false},
				{"resolutionTime", "uint64", false},
			},
		},
		{
			name: "LiquidityChanged", signature: "LiquidityChanged(bytes32,uint256,uint256,uint64)",
			inputs: []inputContract{
				{"marketId", "bytes32", true}, {"yesReserve", "uint256", false},
				{"noReserve", "uint256", false}, {"resolutionTime", "uint64", false},
			},
		},
		{
			name: "MarketResolved", signature: "MarketResolved(bytes32,bool)",
			inputs: []inputContract{{"marketId", "bytes32", true}, {"outcome", "bool", false}},
		},
	}

	if got := len(fixtureABI.Events); got != 3 {
		t.Fatalf("fixture ABI event count = %d, want 3", got)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, ok := fixtureABI.Events[tt.name]
			if !ok {
				t.Fatalf("fixture ABI missing event %q", tt.name)
			}
			if event.Anonymous {
				t.Error("event is anonymous, want non-anonymous")
			}
			if event.Sig != tt.signature {
				t.Errorf("signature = %q, want %q", event.Sig, tt.signature)
			}
			if len(event.Inputs) != len(tt.inputs) {
				t.Fatalf("input count = %d, want %d", len(event.Inputs), len(tt.inputs))
			}
			for i, want := range tt.inputs {
				got := event.Inputs[i]
				if got.Name != want.name || got.Type.String() != want.typ || got.Indexed != want.indexed {
					t.Errorf("input %d = {%q, %q, indexed=%t}, want {%q, %q, indexed=%t}", i, got.Name, got.Type.String(), got.Indexed, want.name, want.typ, want.indexed)
				}
			}
		})
	}
}
