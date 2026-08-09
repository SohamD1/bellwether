"""Load labeled, precomputed feature snapshots from Parquet."""

from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path

import numpy as np
import numpy.typing as npt
import pyarrow as pa
import pyarrow.parquet as pq

FEATURE_COLUMNS = (
    "yes_implied_probability",
    "recent_trade_flow_imbalance",
    "liquidity_depth",
    "seconds_to_resolution",
    "block_lag",
)
REQUIRED_COLUMNS = (
    "market_id",
    "block_number",
    "log_index",
    "block_timestamp",
    "resolution_timestamp",
    "finality",
    *FEATURE_COLUMNS,
    "outcome",
)


@dataclass(frozen=True)
class LabeledDataset:
    market_ids: npt.NDArray[np.object_]
    block_numbers: npt.NDArray[np.uint64]
    log_indices: npt.NDArray[np.uint64]
    block_timestamps_ns: npt.NDArray[np.int64]
    resolution_timestamps_ns: npt.NDArray[np.int64]
    finality: npt.NDArray[np.int32]
    features: npt.NDArray[np.float64]
    outcomes: npt.NDArray[np.int8]
    feature_names: tuple[str, ...] = FEATURE_COLUMNS

    def __len__(self) -> int:
        return len(self.outcomes)


def load_labeled_parquet(path: str | Path) -> LabeledDataset:
    """Read an exact labeled snapshot; never derive features or join labels."""

    table = pq.read_table(path)
    _validate_schema(table)
    _validate_no_nulls(table)

    block_numbers = _integers(table, "block_number", np.uint64)
    log_indices = _integers(table, "log_index", np.uint64)
    block_timestamps_ns = _timestamps_ns(table["block_timestamp"])
    resolution_timestamps_ns = _timestamps_ns(table["resolution_timestamp"])
    finality = _integers(table, "finality", np.int32, allowed=(0, 1, 2))
    features = np.column_stack(
        [table[name].combine_chunks().to_numpy(zero_copy_only=False) for name in FEATURE_COLUMNS]
    ).astype(np.float64, copy=False)
    outcomes = _integers(table, "outcome", np.int8, allowed=(0, 1))

    _validate_values(
        block_numbers=block_numbers,
        log_indices=log_indices,
        block_timestamps_ns=block_timestamps_ns,
        resolution_timestamps_ns=resolution_timestamps_ns,
        finality=finality,
        features=features,
        outcomes=outcomes,
    )
    return LabeledDataset(
        market_ids=table["market_id"].combine_chunks().to_numpy(zero_copy_only=False),
        block_numbers=block_numbers,
        log_indices=log_indices,
        block_timestamps_ns=block_timestamps_ns,
        resolution_timestamps_ns=resolution_timestamps_ns,
        finality=finality,
        features=features,
        outcomes=outcomes,
    )


def _validate_schema(table: pa.Table) -> None:
    names = table.column_names
    missing = sorted(set(REQUIRED_COLUMNS) - set(names))
    unexpected = sorted(set(names) - set(REQUIRED_COLUMNS))
    duplicates = sorted({name for name in names if names.count(name) > 1})
    if missing or unexpected or duplicates:
        raise ValueError(
            f"dataset schema must contain exactly the labeled snapshot columns; "
            f"missing={missing}, unexpected={unexpected}, duplicates={duplicates}"
        )
    if table.num_rows == 0:
        raise ValueError("dataset must contain at least one row")

    schema = table.schema
    if not pa.types.is_string(schema.field("market_id").type):
        raise ValueError("dataset schema market_id must be string")
    for name in ("block_number", "log_index", "finality", "block_lag"):
        if not pa.types.is_integer(schema.field(name).type):
            raise ValueError(f"dataset schema {name} must be integer")
    for name in ("block_timestamp", "resolution_timestamp"):
        field_type = schema.field(name).type
        if not pa.types.is_timestamp(field_type) or field_type.unit != "ns":
            raise ValueError(f"dataset schema {name} must be a nanosecond timestamp")
    for name in FEATURE_COLUMNS[:-1]:
        if not pa.types.is_floating(schema.field(name).type):
            raise ValueError(f"dataset schema {name} must be floating point")
    outcome_type = schema.field("outcome").type
    if not (pa.types.is_boolean(outcome_type) or pa.types.is_integer(outcome_type)):
        raise ValueError("dataset schema outcome must be boolean or integer")


def _validate_no_nulls(table: pa.Table) -> None:
    null_columns = [name for name in REQUIRED_COLUMNS if table[name].null_count]
    if null_columns:
        raise ValueError(f"dataset columns must not contain nulls: {null_columns}")


def _validate_values(
    *,
    block_numbers: npt.NDArray[np.uint64],
    log_indices: npt.NDArray[np.uint64],
    block_timestamps_ns: npt.NDArray[np.int64],
    resolution_timestamps_ns: npt.NDArray[np.int64],
    finality: npt.NDArray[np.int32],
    features: npt.NDArray[np.float64],
    outcomes: npt.NDArray[np.int8],
) -> None:
    if not np.isfinite(features).all():
        raise ValueError("feature values must be finite")
    probabilities = features[:, 0]
    if ((probabilities < 0) | (probabilities > 1)).any():
        raise ValueError("yes implied probability must be within [0, 1]")
    if not np.isin(finality, (0, 1, 2)).all():
        raise ValueError("unknown finality; expected 0 (seen), 1 (safe), or 2 (finalized)")
    if not np.isin(outcomes, (0, 1)).all():
        raise ValueError("outcome must be boolean, 0, or 1")
    if (resolution_timestamps_ns < block_timestamps_ns).any():
        raise ValueError("resolution timestamp must not precede prediction timestamp")

    for index in range(1, len(block_numbers)):
        previous = (int(block_numbers[index - 1]), int(log_indices[index - 1]))
        current = (int(block_numbers[index]), int(log_indices[index]))
        if current <= previous:
            raise ValueError(f"duplicate or unordered position at row {index}")
        if block_timestamps_ns[index] < block_timestamps_ns[index - 1]:
            raise ValueError(f"prediction timestamps out of order at row {index}")


def _integers(
    table: pa.Table,
    name: str,
    dtype: type[np.generic],
    *,
    allowed: tuple[int, ...] | None = None,
) -> npt.NDArray[np.generic]:
    values = table[name].combine_chunks().to_numpy(zero_copy_only=False)
    if pa.types.is_signed_integer(table.schema.field(name).type) and (values < 0).any():
        raise ValueError(f"{name} must not be negative")
    if allowed is not None and not np.isin(values, allowed).all():
        raise ValueError(f"invalid {name}; expected one of {allowed}")
    return values.astype(dtype, copy=False)


def _timestamps_ns(column: pa.ChunkedArray) -> npt.NDArray[np.int64]:
    return column.combine_chunks().cast(pa.int64()).to_numpy(zero_copy_only=False)
