"""Run a reproducible Bellwether walk-forward experiment from labeled Parquet."""

from __future__ import annotations

import argparse
import html
import json
import os
import tempfile
import tomllib
from collections.abc import Mapping, Sequence
from dataclasses import asdict
from pathlib import Path
from typing import Any

from bellwether_harness.dataset import LabeledDataset, load_labeled_parquet
from bellwether_harness.evaluation import (
    EvaluationResult,
    PredictorEvaluation,
    evaluate_walk_forward,
)
from bellwether_harness.models import default_model_specs
from bellwether_harness.runs import RunIdentity
from bellwether_harness.splits import WalkForwardConfig

_TOP_LEVEL_CONFIG_KEYS = {"seed", "reliability_bin_count", "split"}
_SPLIT_CONFIG_KEYS = {
    "initial_train_rows",
    "embargo_rows",
    "test_rows",
    "step_rows",
}


def main(argv: Sequence[str] | None = None) -> int:
    """Run an experiment and atomically publish its three artifacts."""

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data", required=True, type=Path)
    parser.add_argument("--config", required=True, type=Path)
    parser.add_argument("--output-dir", required=True, type=Path)
    parser.add_argument("--git-sha", required=True)
    parser.add_argument("--data-snapshot-id", required=True)
    args = parser.parse_args(argv)

    config = _load_config(args.config)
    split_values = config["split"]
    split_config = WalkForwardConfig(
        initial_train_rows=split_values["initial_train_rows"],
        embargo_rows=split_values["embargo_rows"],
        test_rows=split_values["test_rows"],
        step_rows=split_values["step_rows"],
    )
    identity = RunIdentity.from_config(
        git_sha=args.git_sha,
        config=config,
        data_snapshot_id=args.data_snapshot_id,
    )
    dataset = load_labeled_parquet(args.data)
    specs = default_model_specs(seed=config["seed"])

    evaluation = evaluate_walk_forward(
        dataset,
        split_config,
        run_identity=identity,
        seed=config["seed"],
        reliability_bin_count=config["reliability_bin_count"],
        model_specs=specs,
    )

    model_text = _fit_full_lightgbm(dataset, specs)
    metrics_bytes = _serialize_metrics(evaluation, config)
    svg_bytes = _reliability_svg(evaluation.aggregate).encode("utf-8")
    model_bytes = model_text.encode("utf-8")

    args.output_dir.mkdir(parents=True, exist_ok=True)
    _atomic_write(args.output_dir / "metrics.json", metrics_bytes)
    _atomic_write(args.output_dir / "reliability.svg", svg_bytes)
    _atomic_write(args.output_dir / "model.txt", model_bytes)
    return 0


def _load_config(path: Path) -> dict[str, Any]:
    with path.open("rb") as config_file:
        config = tomllib.load(config_file)
    _require_exact_keys(config, _TOP_LEVEL_CONFIG_KEYS, "experiment config")
    split = config["split"]
    if not isinstance(split, Mapping):
        raise ValueError("experiment config split must be a table")
    _require_exact_keys(split, _SPLIT_CONFIG_KEYS, "experiment config split")
    if type(config["seed"]) is not int or config["seed"] < 0:
        raise ValueError("seed must be a non-negative integer")
    _positive_integer(config["reliability_bin_count"], "reliability_bin_count")
    for name in _SPLIT_CONFIG_KEYS:
        _positive_integer(split[name], name)
    return config


def _require_exact_keys(values: Mapping[str, Any], expected: set[str], description: str) -> None:
    actual = set(values)
    if actual != expected:
        missing = sorted(expected - actual)
        unexpected = sorted(str(key) for key in actual - expected)
        raise ValueError(
            f"{description} keys must match the schema; missing={missing}, unexpected={unexpected}"
        )


def _positive_integer(value: object, name: str) -> None:
    if type(value) is not int or value <= 0:
        raise ValueError(f"{name} must be a positive integer")


def _fit_full_lightgbm(dataset: LabeledDataset, specs: tuple[Any, ...]) -> str:
    lightgbm_spec = next(spec for spec in specs if spec.name == "lightgbm")
    model = lightgbm_spec.build()
    model.fit(
        dataset.features,
        dataset.outcomes,
        feature_name=list(dataset.feature_names),
    )
    return str(model.booster_.model_to_string())


def _serialize_metrics(evaluation: EvaluationResult, config: Mapping[str, Any]) -> bytes:
    payload = {
        "aggregate": [_predictor_payload(predictor) for predictor in evaluation.aggregate],
        "config": config,
        "folds": [
            {
                "boundaries": dict(fold.boundaries._asdict()),
                "eligible_train_market_ids": list(fold.eligible_train_market_ids),
                "eligible_train_rows": list(fold.eligible_train_rows),
                "fold_index": fold.fold_index,
                "predictors": [_predictor_payload(predictor) for predictor in fold.predictors],
                "test_market_ids": list(fold.test_market_ids),
                "test_rows": list(fold.test_rows),
                "training_cutoff_ns": fold.training_cutoff_ns,
            }
            for fold in evaluation.folds
        ],
        "log_loss_epsilon": evaluation.log_loss_epsilon,
        "run_identity": asdict(evaluation.run_identity),
    }
    return (
        json.dumps(
            payload,
            allow_nan=False,
            ensure_ascii=False,
            indent=2,
            sort_keys=True,
        )
        + "\n"
    ).encode("utf-8")


def _predictor_payload(predictor: PredictorEvaluation) -> dict[str, Any]:
    return {
        "name": predictor.name,
        "reliability": [asdict(item) for item in predictor.reliability],
        "scores": asdict(predictor.scores),
    }


def _reliability_svg(predictors: tuple[PredictorEvaluation, ...]) -> str:
    width = 800
    height = 520
    left = 80
    top = 50
    plot_width = 440
    plot_height = 400
    colors = ("#2563eb", "#dc2626", "#059669", "#7c3aed", "#d97706")
    lines = [
        '<?xml version="1.0" encoding="UTF-8"?>',
        (
            f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" '
            f'height="{height}" viewBox="0 0 {width} {height}">'
        ),
        '<rect width="100%" height="100%" fill="#ffffff"/>',
        '<text x="400" y="28" text-anchor="middle" '
        'font-family="sans-serif" font-size="20">Aggregate reliability</text>',
        (
            f'<rect x="{left}" y="{top}" width="{plot_width}" height="{plot_height}" '
            'fill="none" stroke="#111827"/>'
        ),
        (
            f'<line x1="{left}" y1="{top + plot_height}" '
            f'x2="{left + plot_width}" y2="{top}" '
            'stroke="#9ca3af" stroke-dasharray="6 4"/>'
        ),
        (
            f'<text x="{left + plot_width / 2:.1f}" y="{height - 20}" '
            'text-anchor="middle" font-family="sans-serif" font-size="14">'
            "Mean predicted probability</text>"
        ),
        (
            f'<text x="20" y="{top + plot_height / 2:.1f}" '
            'text-anchor="middle" font-family="sans-serif" font-size="14" '
            f'transform="rotate(-90 20 {top + plot_height / 2:.1f})">'
            "Observed outcome rate</text>"
        ),
    ]
    for tick in range(6):
        value = tick / 5
        x = left + value * plot_width
        y = top + (1 - value) * plot_height
        lines.extend(
            [
                (
                    f'<line x1="{x:.1f}" y1="{top + plot_height}" '
                    f'x2="{x:.1f}" y2="{top + plot_height + 6}" stroke="#111827"/>'
                ),
                (
                    f'<text x="{x:.1f}" y="{top + plot_height + 22}" '
                    'text-anchor="middle" font-family="sans-serif" font-size="12">'
                    f"{value:.1f}</text>"
                ),
                (f'<line x1="{left - 6}" y1="{y:.1f}" x2="{left}" y2="{y:.1f}" stroke="#111827"/>'),
                (
                    f'<text x="{left - 10}" y="{y + 4:.1f}" '
                    'text-anchor="end" font-family="sans-serif" font-size="12">'
                    f"{value:.1f}</text>"
                ),
            ]
        )
    for index, predictor in enumerate(predictors):
        color = colors[index % len(colors)]
        points = [
            (
                left + item.mean_prediction * plot_width,
                top + (1 - item.observed_rate) * plot_height,
            )
            for item in predictor.reliability
            if item.count and item.mean_prediction is not None and item.observed_rate is not None
        ]
        if points:
            point_text = " ".join(f"{x:.3f},{y:.3f}" for x, y in points)
            lines.append(
                f'<polyline points="{point_text}" fill="none" stroke="{color}" stroke-width="2"/>'
            )
            lines.extend(
                f'<circle cx="{x:.3f}" cy="{y:.3f}" r="4" fill="{color}"/>' for x, y in points
            )
        legend_y = top + 25 + index * 28
        lines.extend(
            [
                (
                    f'<line x1="560" y1="{legend_y}" x2="590" y2="{legend_y}" '
                    f'stroke="{color}" stroke-width="3"/>'
                ),
                (
                    f'<text x="600" y="{legend_y + 5}" '
                    'font-family="sans-serif" font-size="14">'
                    f"{html.escape(predictor.name)}</text>"
                ),
            ]
        )
    lines.append("</svg>")
    return "\n".join(lines) + "\n"


def _atomic_write(path: Path, data: bytes) -> None:
    temporary_path: Path | None = None
    try:
        with tempfile.NamedTemporaryFile(
            dir=path.parent,
            prefix=f".{path.name}.",
            suffix=".tmp",
            delete=False,
        ) as temporary:
            temporary_path = Path(temporary.name)
            temporary.write(data)
            temporary.flush()
            os.fsync(temporary.fileno())
        os.replace(temporary_path, path)
        temporary_path = None
    finally:
        if temporary_path is not None:
            temporary_path.unlink(missing_ok=True)


if __name__ == "__main__":
    raise SystemExit(main())
