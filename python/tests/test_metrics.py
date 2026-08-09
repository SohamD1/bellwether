import math

import numpy as np
import pytest

from bellwether_harness.metrics import LOG_LOSS_EPSILON, binary_scores, reliability_bins


def test_binary_scores_use_raw_probabilities_for_brier_and_clipping_only_for_log_loss() -> None:
    scores = binary_scores(np.array([0, 1]), np.array([0.0, 0.0]))

    assert scores.brier == 0.5
    assert scores.log_loss == pytest.approx(-math.log(LOG_LOSS_EPSILON) / 2)


def test_reliability_bins_have_fixed_boundaries_and_deterministic_empty_bins() -> None:
    bins = reliability_bins(
        np.array([0, 1, 1]),
        np.array([0.0, 0.19, 1.0]),
        bin_count=5,
    )

    assert [(item.lower, item.upper, item.count) for item in bins] == [
        (0.0, 0.2, 2),
        (0.2, 0.4, 0),
        (0.4, 0.6, 0),
        (0.6, 0.8, 0),
        (0.8, 1.0, 1),
    ]
    assert bins[0].mean_prediction == pytest.approx(0.095)
    assert bins[0].observed_rate == 0.5
    assert bins[1].mean_prediction is None
    assert bins[4].mean_prediction == 1.0
    assert bins[4].observed_rate == 1.0


@pytest.mark.parametrize(
    ("labels", "probabilities"),
    [
        ([0, 2], [0.2, 0.8]),
        ([0, 1], [-0.1, 0.8]),
        ([0, 1], [0.2, float("nan")]),
        ([0], [0.2, 0.8]),
    ],
)
def test_metrics_reject_invalid_or_misaligned_inputs(
    labels: list[int], probabilities: list[float]
) -> None:
    with pytest.raises(ValueError):
        binary_scores(np.asarray(labels), np.asarray(probabilities))
