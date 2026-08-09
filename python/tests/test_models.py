import numpy as np
import pytest
from sklearn.exceptions import NotFittedError

from bellwether_harness.models import default_model_specs


def test_default_models_are_the_two_requested_deterministic_cpu_models() -> None:
    specs = default_model_specs(seed=19)

    assert [spec.name for spec in specs] == ["logistic_regression", "lightgbm"]
    first = [spec.build().fit([[0.0], [1.0], [2.0], [3.0]], [0, 0, 1, 1]) for spec in specs]
    second = [spec.build().fit([[0.0], [1.0], [2.0], [3.0]], [0, 0, 1, 1]) for spec in specs]
    for left, right in zip(first, second, strict=True):
        assert np.array_equal(left.predict_proba([[1.5]]), right.predict_proba([[1.5]]))


def test_model_factories_do_not_share_fitted_state() -> None:
    spec = default_model_specs(seed=7)[0]

    first = spec.build()
    second = spec.build()
    first.fit([[0.0], [1.0]], [0, 1])

    with pytest.raises(NotFittedError):
        second.predict_proba([[0.5]])
