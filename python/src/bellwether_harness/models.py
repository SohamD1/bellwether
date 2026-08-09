"""Deterministic model factories for fold-local fitting."""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

from sklearn.linear_model import LogisticRegression
from sklearn.pipeline import make_pipeline
from sklearn.preprocessing import StandardScaler


@dataclass(frozen=True)
class ModelSpec:
    name: str
    _builder: Callable[[], Any]

    def build(self) -> Any:
        """Create a fresh, unfitted estimator for one fold."""

        return self._builder()


def default_model_specs(*, seed: int) -> tuple[ModelSpec, ...]:
    """Return logistic regression followed by deterministic CPU LightGBM."""

    if type(seed) is not int:
        raise ValueError("seed must be an integer")
    return (
        ModelSpec(
            "logistic_regression",
            lambda: make_pipeline(
                StandardScaler(),
                LogisticRegression(random_state=seed, solver="lbfgs", max_iter=1000),
            ),
        ),
        ModelSpec("lightgbm", lambda: _build_lightgbm(seed)),
    )


def _build_lightgbm(seed: int) -> Any:
    try:
        from lightgbm import LGBMClassifier
    except ImportError as error:  # pragma: no cover - dependency is required by this project
        raise RuntimeError("LightGBM is unavailable; install the project dependencies") from error
    return LGBMClassifier(
        objective="binary",
        n_estimators=200,
        learning_rate=0.05,
        num_leaves=31,
        random_state=seed,
        n_jobs=1,
        deterministic=True,
        force_col_wise=True,
        verbosity=-1,
        bagging_seed=seed,
        feature_fraction_seed=seed,
        data_random_seed=seed,
    )
