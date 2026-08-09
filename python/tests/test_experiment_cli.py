from __future__ import annotations

import json
from datetime import UTC, datetime, timedelta
from importlib.metadata import entry_points
from pathlib import Path
from xml.etree import ElementTree

import pyarrow as pa
import pyarrow.parquet as pq

from bellwether_harness.dataset import FEATURE_COLUMNS


def test_package_registers_experiment_console_command() -> None:
    commands = {entry.name: entry.value for entry in entry_points(group="console_scripts")}

    assert commands["bellwether-experiment"] == "bellwether_harness.experiment:main"


def _write_labeled_snapshot(path: Path) -> None:
    start = datetime(2026, 1, 1, tzinfo=UTC)
    market_ids = [
        "market-a",
        "market-a",
        "market-b",
        "market-b",
        "market-c",
        "market-c",
        "market-f",
        "market-g",
        "market-d",
        "market-e",
    ]
    table = pa.table(
        {
            "market_id": pa.array(market_ids),
            "block_number": pa.array(range(100, 110), type=pa.uint64()),
            "log_index": pa.array([0] * 10, type=pa.uint64()),
            "block_timestamp": pa.array(
                [start + timedelta(days=index) for index in range(10)],
                type=pa.timestamp("ns"),
            ),
            "resolution_timestamp": pa.array(
                [start + timedelta(days=day) for day in (2, 2, 4, 4, 5, 5, 7, 8, 10, 10)],
                type=pa.timestamp("ns"),
            ),
            "label_available_timestamp": pa.array(
                [start + timedelta(days=day) for day in (2, 2, 4, 4, 8, 8, 8, 9, 11, 12)],
                type=pa.timestamp("ns"),
            ),
            "finality": pa.array([2] * 10, type=pa.int32()),
            "yes_implied_probability": pa.array([0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 0.2]),
            "recent_trade_flow_imbalance": pa.array(
                [-0.5, -0.4, -0.3, -0.2, -0.1, 0.0, 0.1, 0.2, 0.3, 0.4]
            ),
            "liquidity_depth": pa.array([10.0 * index for index in range(1, 11)]),
            "seconds_to_resolution": pa.array([3600.0] * 10),
            "block_lag": pa.array(range(10), type=pa.uint64()),
            "outcome": pa.array([0, 0, 1, 1, 1, 1, 0, 1, 1, 0], type=pa.int8()),
        }
    )
    pq.write_table(table, path)


def test_experiment_command_writes_reproducible_evaluation_and_model_artifacts(
    tmp_path: Path,
) -> None:
    data_path = tmp_path / "snapshot.parquet"
    config_path = tmp_path / "experiment.toml"
    _write_labeled_snapshot(data_path)
    config_path.write_text(
        """seed = 0
reliability_bin_count = 5

[split]
initial_train_rows = 6
embargo_rows = 2
test_rows = 2
step_rows = 2
""",
        encoding="utf-8",
    )
    command = {entry.name: entry.load() for entry in entry_points(group="console_scripts")}[
        "bellwether-experiment"
    ]

    output_dirs = [tmp_path / "first", tmp_path / "second"]
    for output_dir in output_dirs:
        exit_code = command(
            [
                "--data",
                str(data_path),
                "--config",
                str(config_path),
                "--output-dir",
                str(output_dir),
                "--git-sha",
                "15dcfc0",
                "--data-snapshot-id",
                "snapshot-2026-01-01",
            ]
        )
        assert exit_code == 0

    first_metrics_bytes = (output_dirs[0] / "metrics.json").read_bytes()
    assert first_metrics_bytes == (output_dirs[1] / "metrics.json").read_bytes()
    assert (output_dirs[0] / "reliability.svg").read_bytes() == (
        output_dirs[1] / "reliability.svg"
    ).read_bytes()
    assert (output_dirs[0] / "model.txt").read_bytes() == (
        output_dirs[1] / "model.txt"
    ).read_bytes()

    metrics = json.loads(first_metrics_bytes)
    assert set(metrics) == {
        "aggregate",
        "config",
        "folds",
        "log_loss_epsilon",
        "run_identity",
    }
    assert metrics["config"] == {
        "reliability_bin_count": 5,
        "seed": 0,
        "split": {
            "embargo_rows": 2,
            "initial_train_rows": 6,
            "step_rows": 2,
            "test_rows": 2,
        },
    }
    assert metrics["run_identity"]["git_sha"] == "15dcfc0"
    assert metrics["run_identity"]["data_snapshot_id"] == "snapshot-2026-01-01"
    assert len(metrics["run_identity"]["config_hash"]) == 64
    assert [item["name"] for item in metrics["aggregate"]] == [
        "market_baseline",
        "logistic_regression",
        "lightgbm",
    ]
    assert len(metrics["folds"]) == 1
    assert metrics["folds"][0]["eligible_train_rows"] == [0, 1, 2, 3]
    for predictor in [*metrics["aggregate"], *metrics["folds"][0]["predictors"]]:
        assert set(predictor["scores"]) == {"brier", "log_loss"}
        assert len(predictor["reliability"]) == 5

    ElementTree.fromstring((output_dirs[0] / "reliability.svg").read_text(encoding="utf-8"))
    model = (output_dirs[0] / "model.txt").read_text(encoding="utf-8")
    assert "feature_names=" + " ".join(FEATURE_COLUMNS) in model
    assert "Tree=0" in model
    assert sorted(path.name for path in output_dirs[0].iterdir()) == [
        "metrics.json",
        "model.txt",
        "reliability.svg",
    ]
