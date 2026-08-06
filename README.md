# Bellwether

Bellwether watches a prediction market on Base, records what happened, and stays correct when the chain changes its recent history.

More technically: it is a reorg-safe Go indexer feeding point-in-time features into a reproducible Python forecasting harness. The goal is to test whether a small model can forecast market resolution better than the market's own price.

It is a data project, not a trading bot. 

> **Status:** early build

## Why this exists

Catching a trade event is the easy part. The interesting part is knowing the record is still right after the chain replaces one of its recent blocks.

Bellwether treats that as the main problem. Every stored block keeps its hash and parent hash. When a new block does not build on the current tip, the indexer finds the common ancestor, removes the orphaned branch, and replays the canonical chain. If the reorg is deeper than the local buffer, it resyncs from the last finalized block.

The correctness check is deliberately simple to say: after any injected reorg, the resulting state must be byte-for-byte identical to a clean replay of the final canonical chain.

## What it does

- indexes trades, liquidity changes, and market resolutions from one prediction-market contract on Base
- tracks data as seen at the tip, safe, or finalized
- computes features as of a specific block without reading the future
- writes feature snapshots to Parquet for repeatable experiments
- runs walk-forward evaluation with an embargo gap
- compares forecasts against market-implied probability using calibration metrics

## How it fits together

```text
Base RPC / WebSocket
        |
        v
  reorg-safe indexer  --->  canonical event log
                                  |
                                  v
                         Go feature engine
                         /               \
                        v                 v
                live inference      feature Parquet
                                          |
                                          v
                                Python experiment harness
                         walk-forward splits + calibration
```

Go owns indexing, feature computation, and serving. The same feature functions are called by the streaming and batch drivers. Python reads the resulting Parquet snapshots. That 

## Features and evaluation

The first version keeps the feature set intentionally small:

- market-implied probability from pool state
- trade-flow imbalance
- liquidity depth
- time to resolution
- blocks since the last market update

Each feature row includes its block and finality level. Appending future blocks must never change a feature row that was already emitted.

The model ladder is also small: logistic regression first, then LightGBM. Evaluation uses walk-forward folds with an embargo between training and test windows. Random k-fold is not useful here because it would mix past and future observations.

The market price is the baseline. Results will be reported with Brier score, log loss, and a reliability curve. If the models do not beat the baseline, that is still the result. Accuracy, Sharpe, and hypothetical returns are not the point of this project.

## Build plan

1. **Prove the chain state is correct.** Start with a deterministic replay model, an event log, rollback/replay, and synthetic reorgs of depth 1-5. Idempotency and clean-replay equivalence come before live RPC work.
2. **Connect it to Base.** Add batched backfills, WebSocket ingestion, finality tracking, bounded queues, retry logic, and a clear resync path for reorgs beyond the local buffer.
3. **Build the research loop.** Compute point-in-time features in Go, export versioned Parquet snapshots, and add walk-forward training, calibration, and leakage checks in Python.
4. **Measure and package it.** Add in-process LightGBM inference, real benchmarks, soak tests, the small Base Sepolia commit-reveal contract, and an honest write-up of results and limitations.

## Stack

| Part | Choice |
| --- | --- |
| Indexer, features, serving | Go |
| Chain access | `ethclient` and `abigen` |
| Experiment harness | Python, scikit-learn, LightGBM |
| Storage | Parquet via `parquet-go` |
| Configuration | TOML |
| Model serving | `leaves` in-process in Go |
| Forecast timestamping | Solidity on Base Sepolia |

The Solidity contract is intentionally small. It timestamps a forecast with commit-reveal; it does not make the forecasting system trustless, and a testnet deployment is not proof of decentralization.

## What will be measured

The first hard gates are correctness gates:

- state after injected reorgs matches a clean canonical replay exactly
- reprocessing any block range is a no-op
- streaming and batch feature matrices differ by no more than `1e-9`
- goroutine count stays flat during a long-running soak test
- past features stay unchanged when future data is appended
- every experiment can be reproduced from its Git SHA, config hash, and data snapshot ID

Latency and throughput will be measured with `go test -bench`, `benchstat`, `pprof`, runtime metrics, and latency histograms. Backfill throughput will be reported beside the RPC provider's concurrency and rate limits, since that ceiling is mostly external.

## Scope

For the first complete version:

- one chain: Base
- one prediction-market contract
- five onchain features
- two model families
- one small settlement contract on Base Sepolia

No multi-chain framework, neural network, self-hosted Base node, mainnet deployment, or automated trading strategy. A Coinbase order-book feed only makes sense if the indexed markets resolve on crypto prices; it will stay out otherwise.

## Known limits

- reorg recovery has a configured depth ceiling; deeper reorgs require resyncing from finalized state
- data availability and backfill speed depend on an external RPC provider
- unfinalized feature rows can be replaced when the chain reorganizes
- Go GC can still affect tail latency and will be measured rather than hand-waved away
- the testnet contract proves timestamping, not that forecasts are correct or independently produced

One chain and one contract is enough scope
