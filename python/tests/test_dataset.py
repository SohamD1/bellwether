from datetime import UTC, datetime, timedelta
from pathlib import Path

import pyarrow as pa
import pyarrow.parquet as pq
import pytest

from bellwether_harness.dataset import FEATURE_COLUMNS, load_labeled_parquet


def valid_columns(row_count: int = 4) -> dict[str, pa.Array]:
    start = datetime(2026, 1, 1, tzinfo=UTC)
    return {
        "market_id": pa.array(["market-a"] * row_count, type=pa.string()),
        "block_number": pa.array(range(100, 100 + row_count), type=pa.uint64()),
        "log_index": pa.array([0] * row_count, type=pa.uint64()),
        "block_timestamp": pa.array(
            [start + timedelta(days=index) for index in range(row_count)],
            type=pa.timestamp("ns"),
        ),
        "resolution_timestamp": pa.array(
            [start + timedelta(days=index + 1) for index in range(row_count)],
            type=pa.timestamp("ns"),
        ),
        "finality": pa.array([0, 1, 2, 2][:row_count], type=pa.int32()),
        "yes_implied_probability": pa.array([0.2, 0.4, 0.6, 0.8][:row_count]),
        "recent_trade_flow_imbalance": pa.array([-0.5, 0.0, 0.25, 0.5][:row_count]),
        "liquidity_depth": pa.array([10.0, 20.0, 30.0, 40.0][:row_count]),
        "seconds_to_resolution": pa.array([86400.0] * row_count),
        "block_lag": pa.array([0, 1, 0, 2][:row_count], type=pa.uint64()),
        "outcome": pa.array([False, True, False, True][:row_count]),
    }


def write_columns(path: Path, columns: dict[str, pa.Array]) -> None:
    pq.write_table(pa.table(columns), path)


def test_loader_returns_only_precomputed_features_and_separate_labels(tmp_path: Path) -> None:
    path = tmp_path / "labeled.parquet"
    write_columns(path, valid_columns())

    dataset = load_labeled_parquet(path)

    assert dataset.feature_names == FEATURE_COLUMNS
    assert dataset.features.shape == (4, 5)
    assert dataset.features[0].tolist() == [0.2, -0.5, 10.0, 86400.0, 0.0]
    assert dataset.outcomes.tolist() == [0, 1, 0, 1]
    assert "outcome" not in dataset.feature_names


@pytest.mark.parametrize("schema_change", ["missing", "extra"])
def test_loader_rejects_any_schema_other_than_the_explicit_contract(
    tmp_path: Path, schema_change: str
) -> None:
    columns = valid_columns()
    if schema_change == "missing":
        del columns["liquidity_depth"]
    else:
        columns["future_outcome_hint"] = pa.array([0, 0, 1, 1])
    path = tmp_path / f"{schema_change}.parquet"
    write_columns(path, columns)

    with pytest.raises(ValueError, match="schema"):
        load_labeled_parquet(path)


@pytest.mark.parametrize(
    ("column", "replacement", "message"),
    [
        ("yes_implied_probability", [0.2, -0.1, 0.6, 0.8], "probability"),
        ("yes_implied_probability", [0.2, float("inf"), 0.6, 0.8], "finite"),
        ("recent_trade_flow_imbalance", [-0.5, float("nan"), 0.25, 0.5], "finite"),
        ("finality", [0, 1, 3, 2], "finality"),
        ("outcome", [0, 1, 2, 1], "outcome"),
        ("outcome", [0, 1, 256, 1], "outcome"),
        ("finality", [0, 1, 4_294_967_298, 2], "finality"),
    ],
)
def test_loader_rejects_invalid_values(
    tmp_path: Path, column: str, replacement: list[float | int], message: str
) -> None:
    columns = valid_columns()
    columns[column] = pa.array(replacement)
    path = tmp_path / f"invalid-{column}.parquet"
    write_columns(path, columns)

    with pytest.raises(ValueError, match=message):
        load_labeled_parquet(path)


def test_loader_rejects_resolution_before_prediction(tmp_path: Path) -> None:
    columns = valid_columns()
    prediction = columns["block_timestamp"][1].as_py()
    resolution = columns["resolution_timestamp"].to_pylist()
    resolution[1] = prediction - timedelta(seconds=1)
    columns["resolution_timestamp"] = pa.array(resolution, type=pa.timestamp("ns"))
    path = tmp_path / "bad-time.parquet"
    write_columns(path, columns)

    with pytest.raises(ValueError, match="resolution"):
        load_labeled_parquet(path)


@pytest.mark.parametrize(
    ("blocks", "logs"),
    [
        ([100, 100, 102, 103], [0, 0, 0, 0]),
        ([100, 99, 102, 103], [0, 1, 0, 0]),
        ([100, 101, 102, 103], [0, 0, 0, 0]),
    ],
)
def test_loader_rejects_duplicate_or_unordered_positions_and_timestamps(
    tmp_path: Path, blocks: list[int], logs: list[int]
) -> None:
    columns = valid_columns()
    columns["block_number"] = pa.array(blocks, type=pa.uint64())
    columns["log_index"] = pa.array(logs, type=pa.uint64())
    if blocks == [100, 101, 102, 103]:
        times = columns["block_timestamp"].to_pylist()
        times[2], times[3] = times[3], times[2]
        columns["block_timestamp"] = pa.array(times, type=pa.timestamp("ns"))
    path = tmp_path / "unordered.parquet"
    write_columns(path, columns)

    with pytest.raises(ValueError, match="order|duplicate"):
        load_labeled_parquet(path)
