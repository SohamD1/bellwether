"""Probability metrics used by the walk-forward evaluator."""

from __future__ import annotations

from dataclasses import dataclass

import numpy as np
import numpy.typing as npt

LOG_LOSS_EPSILON = 1e-15


@dataclass(frozen=True)
class BinaryScores:
    brier: float
    log_loss: float


@dataclass(frozen=True)
class ReliabilityBin:
    lower: float
    upper: float
    count: int
    mean_prediction: float | None
    observed_rate: float | None


def binary_scores(labels: npt.ArrayLike, probabilities: npt.ArrayLike) -> BinaryScores:
    """Return Brier and log loss; only log loss uses epsilon clipping."""

    y_true, y_probability = _validated_binary_inputs(labels, probabilities)
    brier = float(np.mean(np.square(y_probability - y_true)))
    clipped = np.clip(y_probability, LOG_LOSS_EPSILON, 1 - LOG_LOSS_EPSILON)
    log_loss = float(-np.mean(y_true * np.log(clipped) + (1 - y_true) * np.log1p(-clipped)))
    return BinaryScores(brier=brier, log_loss=log_loss)


def reliability_bins(
    labels: npt.ArrayLike,
    probabilities: npt.ArrayLike,
    *,
    bin_count: int = 10,
) -> tuple[ReliabilityBin, ...]:
    """Aggregate probabilities into fixed equal-width bins over [0, 1]."""

    if type(bin_count) is not int or bin_count <= 0:
        raise ValueError("bin_count must be a positive integer")
    y_true, y_probability = _validated_binary_inputs(labels, probabilities)
    assignments = np.minimum((y_probability * bin_count).astype(np.int64), bin_count - 1)
    bins: list[ReliabilityBin] = []
    for index in range(bin_count):
        selected = assignments == index
        count = int(np.count_nonzero(selected))
        bins.append(
            ReliabilityBin(
                lower=index / bin_count,
                upper=(index + 1) / bin_count,
                count=count,
                mean_prediction=float(np.mean(y_probability[selected])) if count else None,
                observed_rate=float(np.mean(y_true[selected])) if count else None,
            )
        )
    return tuple(bins)


def _validated_binary_inputs(
    labels: npt.ArrayLike, probabilities: npt.ArrayLike
) -> tuple[npt.NDArray[np.float64], npt.NDArray[np.float64]]:
    y_true = np.asarray(labels)
    y_probability = np.asarray(probabilities, dtype=np.float64)
    if y_true.ndim != 1 or y_probability.ndim != 1 or len(y_true) == 0:
        raise ValueError("labels and probabilities must be non-empty one-dimensional arrays")
    if len(y_true) != len(y_probability):
        raise ValueError("labels and probabilities must have equal length")
    if not np.isin(y_true, (0, 1)).all():
        raise ValueError("labels must be binary")
    if not np.isfinite(y_probability).all():
        raise ValueError("probabilities must be finite")
    if ((y_probability < 0) | (y_probability > 1)).any():
        raise ValueError("probabilities must be within [0, 1]")
    return y_true.astype(np.float64, copy=False), y_probability
