package market

import (
	"errors"
	"fmt"

	"github.com/SohamD1/bellwether/internal/base"
)

// ErrUnknownEvent reports a log whose first topic is not in the fixture ABI.
var ErrUnknownEvent = errors.New("market: unknown event")

// ErrInvalidContract reports a zero fixture contract address.
var ErrInvalidContract = errors.New("market: invalid contract")

// ErrWrongContract reports a log emitted by a different contract.
var ErrWrongContract = errors.New("market: wrong contract")

// ErrMalformedLog reports invalid indexed topics or ABI event data.
var ErrMalformedLog = errors.New("market: malformed log")

// Decoder decodes logs emitted by one Bellwether fixture contract.
type Decoder struct {
	contract base.Address
}

// NewDecoder constructs a decoder for contract.
func NewDecoder(contract base.Address) (*Decoder, error) {
	if contract == (base.Address{}) {
		return nil, ErrInvalidContract
	}
	if fixtureABIErr != nil {
		return nil, fmt.Errorf("market: parse fixture ABI: %w", fixtureABIErr)
	}
	return &Decoder{contract: contract}, nil
}

// Decode turns a raw Base log into one typed fixture event.
func (d *Decoder) Decode(log base.Log) (Event, error) {
	if log.Address != d.contract {
		return nil, ErrWrongContract
	}
	if len(log.Topics) == 0 {
		return nil, ErrUnknownEvent
	}
	switch log.Topics[0] {
	case eventTopic("Trade"):
		if err := requireTopicCount(log, "Trade", 3); err != nil {
			return nil, err
		}
		return checkedEvent(decodeTradeABI(log))
	case eventTopic("LiquidityChanged"):
		if err := requireTopicCount(log, "LiquidityChanged", 2); err != nil {
			return nil, err
		}
		return checkedEvent(decodeLiquidityChangedABI(log))
	case eventTopic("MarketResolved"):
		if err := requireTopicCount(log, "MarketResolved", 2); err != nil {
			return nil, err
		}
		return checkedEvent(decodeMarketResolvedABI(log))
	default:
		return nil, ErrUnknownEvent
	}
}

func requireTopicCount(log base.Log, event string, want int) error {
	if len(log.Topics) == want {
		return nil
	}
	return fmt.Errorf("%w: %s has %d topics, want %d", ErrMalformedLog, event, len(log.Topics), want)
}

func checkedEvent(event Event, err error) (Event, error) {
	if err != nil {
		return nil, errors.Join(ErrMalformedLog, err)
	}
	return event, nil
}
