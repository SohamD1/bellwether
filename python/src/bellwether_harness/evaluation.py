"""Leakage-safe walk-forward training and calibrated probability evaluation."""

from __future__ import annotations

from dataclasses import dataclass

import numpy as np

from bellwether_harness.dataset import LabeledDataset
from bellwether_harness.metrics import (
    LOG_LOSS_EPSILON,
    BinaryScores,
    ReliabilityBin,
    binary_scores,
    reliability_bins,
)
from bellwether_harness.models import ModelSpec, default_model_specs
from bellwether_harness.runs import RunIdentity
from bellwether_harness.splits import (
    WalkForwardConfig,
    WalkForwardFold,
    expanding_walk_forward_splits,
)

MARKET_BASELINE_NAME = "market_baseline"


@dataclass(frozen=True)
class PredictorEvaluation:
    name: str
    scores: BinaryScores
    reliability: tuple[ReliabilityBin, ...]
    predictions: tuple[float, ...]


@dataclass(frozen=True)
class FoldEvaluation:
    fold_index: int
    boundaries: WalkForwardFold
    training_cutoff_ns: int
    eligible_train_rows: tuple[int, ...]
    test_rows: tuple[int, ...]
    predictors: tuple[PredictorEvaluation, ...]


@dataclass(frozen=True)
class EvaluationResult:
    run_identity: RunIdentity
    folds: tuple[FoldEvaluation, ...]
    aggregate: tuple[PredictorEvaluation, ...]
    log_loss_epsilon: float = LOG_LOSS_EPSILON


def evaluate_walk_forward(
    dataset: LabeledDataset,
    split_config: WalkForwardConfig,
    *,
    run_identity: RunIdentity,
    seed: int,
    reliability_bin_count: int = 10,
    model_specs: tuple[ModelSpec, ...] | None = None,
) -> EvaluationResult:
    """Fit fresh fold-local models and score future rows.

    At each fold, the training cutoff is the final training row's decision
    timestamp. A candidate row is trainable only when its label resolution time
    is no later than that cutoff. Test rows begin only after the configured
    embargo. Features are consumed exactly as loaded; this function performs no
    feature calculations, joins, calibration, or test-set fitting.
    """

    specs = default_model_specs(seed=seed) if model_specs is None else model_specs
    _validate_model_specs(specs)
    folds = tuple(expanding_walk_forward_splits(dataset.block_timestamps_ns.tolist(), split_config))
    if not folds:
        raise ValueError("walk-forward configuration produced no complete folds")

    fold_results: list[FoldEvaluation] = []
    aggregate_labels: list[int] = []
    aggregate_predictions: dict[str, list[float]] = {
        name: [] for name in (MARKET_BASELINE_NAME, *(spec.name for spec in specs))
    }

    for fold_index, fold in enumerate(folds):
        cutoff_ns = int(dataset.block_timestamps_ns[fold.train_end - 1])
        first_embargo_ns = int(dataset.block_timestamps_ns[fold.embargo_start])
        if cutoff_ns >= first_embargo_ns:
            raise ValueError(
                f"fold {fold_index} training decision timestamps must precede "
                "the embargo and test windows"
            )

        candidates = np.arange(fold.train_start, fold.train_end, dtype=np.int64)
        known = dataset.resolution_timestamps_ns[candidates] <= cutoff_ns
        eligible = candidates[known]
        training_labels = dataset.outcomes[eligible]
        if len(np.unique(training_labels)) < 2:
            raise ValueError(
                f"fold {fold_index} has one class after removing labels unknown at cutoff"
            )

        test_rows = np.arange(fold.test_start, fold.test_end, dtype=np.int64)
        test_labels = dataset.outcomes[test_rows]
        fold_predictions: list[tuple[str, np.ndarray]] = [
            (MARKET_BASELINE_NAME, dataset.features[test_rows, 0])
        ]
        for spec in specs:
            estimator = spec.build()
            estimator.fit(dataset.features[eligible], training_labels)
            probabilities = np.asarray(
                estimator.predict_proba(dataset.features[test_rows]), dtype=np.float64
            )
            if probabilities.shape != (len(test_rows), 2):
                raise ValueError(
                    f"model {spec.name!r} predict_proba must return two class probabilities"
                )
            fold_predictions.append((spec.name, probabilities[:, 1]))

        predictors = tuple(
            _evaluate_predictor(
                name,
                test_labels,
                predictions,
                reliability_bin_count=reliability_bin_count,
            )
            for name, predictions in fold_predictions
        )
        aggregate_labels.extend(int(value) for value in test_labels)
        for predictor in predictors:
            aggregate_predictions[predictor.name].extend(predictor.predictions)
        fold_results.append(
            FoldEvaluation(
                fold_index=fold_index,
                boundaries=fold,
                training_cutoff_ns=cutoff_ns,
                eligible_train_rows=tuple(int(index) for index in eligible),
                test_rows=tuple(int(index) for index in test_rows),
                predictors=predictors,
            )
        )

    aggregate = tuple(
        _evaluate_predictor(
            name,
            np.asarray(aggregate_labels, dtype=np.int8),
            np.asarray(predictions, dtype=np.float64),
            reliability_bin_count=reliability_bin_count,
        )
        for name, predictions in aggregate_predictions.items()
    )
    return EvaluationResult(
        run_identity=run_identity,
        folds=tuple(fold_results),
        aggregate=aggregate,
    )


def _validate_model_specs(specs: tuple[ModelSpec, ...]) -> None:
    names = [spec.name for spec in specs]
    if any(not name.strip() for name in names):
        raise ValueError("model names must not be empty")
    if MARKET_BASELINE_NAME in names or len(names) != len(set(names)):
        raise ValueError("model names must be unique and must not shadow the market baseline")


def _evaluate_predictor(
    name: str,
    labels: np.ndarray,
    predictions: np.ndarray,
    *,
    reliability_bin_count: int,
) -> PredictorEvaluation:
    scores = binary_scores(labels, predictions)
    reliability = reliability_bins(labels, predictions, bin_count=reliability_bin_count)
    return PredictorEvaluation(
        name=name,
        scores=scores,
        reliability=reliability,
        predictions=tuple(float(value) for value in predictions),
    )
