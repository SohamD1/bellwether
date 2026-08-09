from __future__ import annotations

from dataclasses import replace

import numpy as np
import pytest

from bellwether_harness.dataset import LabeledDataset
from bellwether_harness.evaluation import evaluate_walk_forward
from bellwether_harness.models import ModelSpec
from bellwether_harness.runs import RunIdentity
from bellwether_harness.splits import WalkForwardConfig

DAY_NS = 86_400_000_000_000


def labeled_dataset(*, sentinel_future_rows: bool = False) -> LabeledDataset:
    row_count = 10
    features = np.column_stack(
        [
            np.linspace(0.1, 0.9, row_count),
            np.linspace(-0.5, 0.5, row_count),
            np.linspace(10.0, 100.0, row_count),
            np.full(row_count, 3600.0),
            np.arange(row_count),
        ]
    )
    if sentinel_future_rows:
        features[4:6, 1] = 999.0
    prediction = np.arange(row_count, dtype=np.int64) * DAY_NS
    resolution = np.array([4, 4, 5, 5, 9, 9, 10, 10, 11, 11], dtype=np.int64) * DAY_NS
    return LabeledDataset(
        market_ids=np.asarray(["market-a"] * row_count, dtype=object),
        block_numbers=np.arange(100, 100 + row_count, dtype=np.uint64),
        log_indices=np.zeros(row_count, dtype=np.uint64),
        block_timestamps_ns=prediction,
        resolution_timestamps_ns=resolution,
        finality=np.full(row_count, 2, dtype=np.int32),
        features=features,
        outcomes=np.asarray([0, 1, 0, 1, 1, 1, 0, 1, 1, 0], dtype=np.int8),
    )


def split_config() -> WalkForwardConfig:
    return WalkForwardConfig(
        initial_train_rows=6,
        embargo_rows=2,
        test_rows=2,
        step_rows=2,
    )


def identity() -> RunIdentity:
    return RunIdentity(git_sha="abc123", config_hash="f" * 64, data_snapshot_id="snapshot-1")


def test_walk_forward_evaluation_stamps_identity_and_scores_models_and_market_baseline() -> None:
    result = evaluate_walk_forward(
        labeled_dataset(),
        split_config(),
        run_identity=identity(),
        seed=7,
        reliability_bin_count=5,
    )

    assert result.run_identity == identity()
    assert len(result.folds) == 1
    assert result.folds[0].eligible_train_rows == (0, 1, 2, 3)
    assert result.folds[0].test_rows == (8, 9)
    assert [item.name for item in result.folds[0].predictors] == [
        "market_baseline",
        "logistic_regression",
        "lightgbm",
    ]
    aggregate = {item.name: item for item in result.aggregate}
    assert aggregate["market_baseline"].scores.brier == pytest.approx(
        ((0.8111111111111111 - 1) ** 2 + (0.9 - 0) ** 2) / 2
    )
    assert all(
        np.isfinite([item.scores.brier, item.scores.log_loss]).all() for item in result.aggregate
    )
    assert sum(item.count for item in aggregate["market_baseline"].reliability) == 2


class SentinelProbe:
    def fit(self, features: np.ndarray, labels: np.ndarray) -> SentinelProbe:
        self.probability = 0.9 if np.any(features[:, 1] == 999.0) else 0.1
        return self

    def predict_proba(self, features: np.ndarray) -> np.ndarray:
        positive = np.full(len(features), self.probability)
        return np.column_stack([1 - positive, positive])


def test_unresolved_future_labels_are_not_admitted_to_fold_training() -> None:
    result = evaluate_walk_forward(
        labeled_dataset(sentinel_future_rows=True),
        split_config(),
        run_identity=identity(),
        seed=7,
        model_specs=(ModelSpec("sentinel_probe", SentinelProbe),),
    )

    probe = {item.name: item for item in result.folds[0].predictors}["sentinel_probe"]
    assert probe.predictions == (0.1, 0.1)


def test_fold_rejects_one_class_after_unknown_labels_are_removed() -> None:
    dataset = labeled_dataset()
    outcomes = dataset.outcomes.copy()
    outcomes[:4] = 0
    dataset = replace(dataset, outcomes=outcomes)

    with pytest.raises(ValueError, match="one class"):
        evaluate_walk_forward(
            dataset,
            split_config(),
            run_identity=identity(),
            seed=7,
            model_specs=(ModelSpec("sentinel_probe", SentinelProbe),),
        )


def test_fold_rejects_non_strict_train_to_embargo_time_order() -> None:
    dataset = labeled_dataset()
    timestamps = dataset.block_timestamps_ns.copy()
    timestamps[6] = timestamps[5]
    dataset = replace(dataset, block_timestamps_ns=timestamps)

    with pytest.raises(ValueError, match="precede"):
        evaluate_walk_forward(
            dataset,
            split_config(),
            run_identity=identity(),
            seed=7,
            model_specs=(ModelSpec("sentinel_probe", SentinelProbe),),
        )
