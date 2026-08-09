"""Dependency-free expanding-window split generation."""

from __future__ import annotations

from collections.abc import Iterator, Sequence
from dataclasses import dataclass
from datetime import datetime
from typing import NamedTuple


@dataclass(frozen=True)
class WalkForwardConfig:
    """Row-count configuration for expanding, embargoed walk-forward folds."""

    initial_train_rows: int
    embargo_rows: int
    test_rows: int
    step_rows: int

    def __post_init__(self) -> None:
        for field_name in (
            "initial_train_rows",
            "embargo_rows",
            "test_rows",
            "step_rows",
        ):
            value = getattr(self, field_name)
            if type(value) is not int or value <= 0:
                raise ValueError(f"{field_name} must be a positive integer")
        if self.step_rows < self.test_rows:
            raise ValueError(
                "step_rows must be at least test_rows to avoid overlapping test windows"
            )


class WalkForwardFold(NamedTuple):
    """Half-open row boundaries for one expanding walk-forward fold."""

    train_start: int
    train_end: int
    embargo_start: int
    embargo_end: int
    test_start: int
    test_end: int


def expanding_walk_forward_splits(
    timestamps: Sequence[datetime], config: WalkForwardConfig
) -> Iterator[WalkForwardFold]:
    """Yield full expanding folds for monotonically ordered timestamps.

    All row bounds are half-open. Training starts at row zero and expands by
    ``step_rows`` after each fold; the embargo begins immediately after training.
    """

    _validate_monotonic_timestamps(timestamps)

    train_end = config.initial_train_rows
    row_count = len(timestamps)
    while train_end + config.embargo_rows + config.test_rows <= row_count:
        embargo_start = train_end
        embargo_end = embargo_start + config.embargo_rows
        test_start = embargo_end
        test_end = test_start + config.test_rows
        yield WalkForwardFold(0, train_end, embargo_start, embargo_end, test_start, test_end)
        train_end += config.step_rows


def _validate_monotonic_timestamps(timestamps: Sequence[datetime]) -> None:
    for index in range(1, len(timestamps)):
        try:
            is_non_monotonic = timestamps[index] < timestamps[index - 1]
        except TypeError as error:
            raise ValueError("timestamps must be mutually comparable and monotonic") from error
        if is_non_monotonic:
            raise ValueError(
                f"timestamps must be monotonic: row {index - 1} is later than row {index}"
            )
