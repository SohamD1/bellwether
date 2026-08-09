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
    scheduled_resolution = np.array([2, 2, 4, 4, 5, 5, 7, 8, 10, 10]) * DAY_NS
    actual_label_availability = np.array([2, 2, 4, 4, 8, 8, 8, 9, 11, 12]) * DAY_NS
    return LabeledDataset(
        market_ids=np.asarray(
            [
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
            ],
            dtype=object,
        ),
        block_numbers=np.arange(100, 100 + row_count, dtype=np.uint64),
        log_indices=np.zeros(row_count, dtype=np.uint64),
        block_timestamps_ns=prediction,
        resolution_timestamps_ns=scheduled_resolution,
        label_available_timestamps_ns=actual_label_availability,
        finality=np.full(row_count, 2, dtype=np.int32),
        features=features,
        outcomes=np.asarray([0, 0, 1, 1, 1, 1, 0, 1, 1, 0], dtype=np.int8),
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
    assert result.folds[0].eligible_train_market_ids == ("market-a", "market-b")
    assert result.folds[0].test_rows == (8, 9)
    assert result.folds[0].test_market_ids == ("market-d", "market-e")
    assert set(result.folds[0].eligible_train_market_ids).isdisjoint(
        result.folds[0].test_market_ids
    )
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
        self.classes_ = np.array([0, 1])
        self.probability = 0.9 if np.any(features[:, 1] == 999.0) else 0.1
        return self

    def predict_proba(self, features: np.ndarray) -> np.ndarray:
        positive = np.full(len(features), self.probability)
        return np.column_stack([1 - positive, positive])


def test_scheduled_resolution_does_not_make_an_unresolved_label_trainable() -> None:
    dataset = labeled_dataset(sentinel_future_rows=True)
    cutoff = dataset.block_timestamps_ns[5]
    assert dataset.resolution_timestamps_ns[4] <= cutoff
    assert dataset.label_available_timestamps_ns[4] > cutoff

    result = evaluate_walk_forward(
        dataset,
        split_config(),
        run_identity=identity(),
        seed=7,
        model_specs=(ModelSpec("sentinel_probe", SentinelProbe),),
    )

    fold = result.folds[0]
    assert fold.eligible_train_rows == (0, 1, 2, 3)
    probe = {item.name: item for item in fold.predictors}["sentinel_probe"]
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


def test_fold_rejects_any_eligible_train_and_test_market_overlap() -> None:
    dataset = labeled_dataset()
    market_ids = dataset.market_ids.copy()
    market_ids[8] = "market-a"
    dataset = replace(dataset, market_ids=market_ids)

    with pytest.raises(ValueError, match="market overlap"):
        evaluate_walk_forward(
            dataset,
            split_config(),
            run_identity=identity(),
            seed=7,
            model_specs=(ModelSpec("sentinel_probe", SentinelProbe),),
        )


class ReversedClassProbe:
    def fit(self, features: np.ndarray, labels: np.ndarray) -> ReversedClassProbe:
        self.classes_ = np.array([1, 0])
        return self

    def predict_proba(self, features: np.ndarray) -> np.ndarray:
        positive = np.full(len(features), 0.25)
        return np.column_stack([positive, 1 - positive])


def test_predictor_finds_the_positive_probability_when_class_order_is_reversed() -> None:
    result = evaluate_walk_forward(
        labeled_dataset(),
        split_config(),
        run_identity=identity(),
        seed=7,
        model_specs=(ModelSpec("reversed", ReversedClassProbe),),
    )

    predictor = {item.name: item for item in result.folds[0].predictors}["reversed"]
    assert predictor.predictions == (0.25, 0.25)


class InvalidClassProbe:
    def __init__(self, classes: list[int]) -> None:
        self._classes = classes

    def fit(self, features: np.ndarray, labels: np.ndarray) -> InvalidClassProbe:
        self.classes_ = np.asarray(self._classes)
        return self

    def predict_proba(self, features: np.ndarray) -> np.ndarray:
        return np.full((len(features), len(self.classes_)), 1 / len(self.classes_))


@pytest.mark.parametrize("classes", [[0], [0, 2], [0, 1, 1]])
def test_predictor_rejects_missing_unexpected_or_duplicate_binary_classes(
    classes: list[int],
) -> None:
    with pytest.raises(ValueError, match="classes"):
        evaluate_walk_forward(
            labeled_dataset(),
            split_config(),
            run_identity=identity(),
            seed=7,
            model_specs=(ModelSpec("invalid", lambda: InvalidClassProbe(classes)),),
        )
