from datetime import datetime, timedelta

import pytest

from bellwether_harness.splits import (
    WalkForwardConfig,
    expanding_walk_forward_splits,
)


def timestamps(count: int) -> list[datetime]:
    start = datetime(2026, 1, 1)
    return [start + timedelta(days=index) for index in range(count)]


def test_expanding_splits_have_exact_half_open_boundaries_and_embargo() -> None:
    config = WalkForwardConfig(
        initial_train_rows=4,
        embargo_rows=2,
        test_rows=3,
        step_rows=3,
    )

    folds = list(expanding_walk_forward_splits(timestamps(13), config))

    assert folds == [
        (0, 4, 4, 6, 6, 9),
        (0, 7, 7, 9, 9, 12),
    ]
    for fold in folds:
        assert fold.train_end == fold.embargo_start
        assert fold.embargo_end == fold.test_start
        assert fold.train_end < fold.test_start


def test_splitter_stops_before_an_incomplete_test_tail() -> None:
    config = WalkForwardConfig(
        initial_train_rows=4,
        embargo_rows=2,
        test_rows=3,
        step_rows=3,
    )

    folds = list(expanding_walk_forward_splits(timestamps(11), config))

    assert folds == [(0, 4, 4, 6, 6, 9)]


@pytest.mark.parametrize(
    ("field", "value"),
    [
        ("initial_train_rows", 0),
        ("embargo_rows", 0),
        ("test_rows", 0),
        ("step_rows", 0),
        ("step_rows", 2),
    ],
)
def test_config_rejects_non_positive_or_overlapping_test_windows(field: str, value: int) -> None:
    values = {
        "initial_train_rows": 4,
        "embargo_rows": 1,
        "test_rows": 3,
        "step_rows": 3,
    }
    values[field] = value

    with pytest.raises(ValueError):
        WalkForwardConfig(**values)


def test_splitter_rejects_non_monotonic_timestamps() -> None:
    ordered = timestamps(9)
    ordered[5] = ordered[4] - timedelta(seconds=1)
    config = WalkForwardConfig(
        initial_train_rows=4,
        embargo_rows=1,
        test_rows=2,
        step_rows=2,
    )

    with pytest.raises(ValueError, match="timestamps must be monotonic"):
        list(expanding_walk_forward_splits(ordered, config))
