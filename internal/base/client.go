// Package base fetches raw prediction-market event logs from Base.
package base

import (
	"context"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

// LogClient is the narrow RPC surface used by Backfiller.
type LogClient interface {
	FilterLogs(context.Context, ethereum.FilterQuery) ([]types.Log, error)
}
