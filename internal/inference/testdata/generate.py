"""Regenerate the LightGBM/leaves compatibility fixture.

The model is deliberately synthetic. It checks model-format and prediction
compatibility only; it is not evidence of forecast quality.
"""

from __future__ import annotations

import json
from pathlib import Path

import lightgbm as lgb
import numpy as np

FEATURE_NAMES = [
    "yes_implied_probability",
    "recent_trade_flow_imbalance",
    "liquidity_depth",
    "seconds_to_resolution",
    "block_lag",
]
SEED = 20260809


def main() -> None:
    rng = np.random.default_rng(SEED)
    rows = 512
    probability = rng.uniform(0.01, 0.99, rows)
    flow = rng.uniform(-1.0, 1.0, rows)
    liquidity = np.exp(rng.uniform(np.log(1.0), np.log(50_000.0), rows))
    seconds = rng.uniform(0.0, 14 * 24 * 60 * 60, rows)
    lag = rng.integers(0, 65, rows).astype(np.float64)
    features = np.column_stack((probability, flow, liquidity, seconds, lag))

    logit = np.log(probability / (1.0 - probability))
    signal = logit + 0.9 * flow + 0.25 * np.log1p(liquidity) - 0.02 * lag
    labels = (signal + rng.normal(0.0, 1.1, rows) > 0.0).astype(np.int8)

    dataset = lgb.Dataset(features, label=labels, feature_name=FEATURE_NAMES)
    model = lgb.train(
        {
            "objective": "binary",
            "metric": "binary_logloss",
            "learning_rate": 0.025,
            "num_leaves": 7,
            "max_depth": 4,
            "min_data_in_leaf": 12,
            "feature_fraction": 1.0,
            "bagging_fraction": 1.0,
            "bagging_freq": 0,
            "seed": SEED,
            "feature_fraction_seed": SEED,
            "bagging_seed": SEED,
            "data_random_seed": SEED,
            "deterministic": True,
            "force_col_wise": True,
            "num_threads": 1,
            "verbosity": -1,
        },
        dataset,
        num_boost_round=200,
    )

    fixture_rows = np.array(
        [
            [0.50, 0.00, 1_000.0, 86_400.0, 0.0],
            [0.08, -0.75, 25.0, 604_800.0, 12.0],
            [0.92, 0.80, 20_000.0, 3_600.0, 2.0],
            [0.35, 0.30, 250.0, 0.0, 64.0],
            [0.70, -0.20, 5_000.0, 1_209_600.0, 7.0],
        ],
        dtype=np.float64,
    )
    probabilities = model.predict(fixture_rows, raw_score=False, num_threads=1)

    output_dir = Path(__file__).resolve().parent
    model_text = model.model_to_string(num_iteration=-1)
    if "\nversion=v4\n" not in model_text:
        raise RuntimeError("expected LightGBM 4.7 text model version v4")
    (output_dir / "compatibility_model.txt").write_text(model_text, encoding="utf-8", newline="\n")
    (output_dir / "compatibility_expected.json").write_text(
        json.dumps(
            {
                "purpose": "format compatibility only; not a quality claim",
                "lightgbm_version": lgb.__version__,
                "loader_compatibility": "Go loader normalizes only the v4 header for leaves",
                "seed": SEED,
                "tree_count": model.num_trees(),
                "feature_names": FEATURE_NAMES,
                "rows": fixture_rows.tolist(),
                "probabilities": probabilities.tolist(),
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
        newline="\n",
    )


if __name__ == "__main__":
    main()
