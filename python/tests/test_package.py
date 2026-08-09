from importlib.metadata import requires

import bellwether_harness


def test_package_version() -> None:
    assert bellwether_harness.__version__ == "0.1.0"


def test_lightgbm_dependency_excludes_incompatible_future_major() -> None:
    dependencies = requires("bellwether-harness") or ()
    lightgbm = next(item for item in dependencies if item.startswith("lightgbm"))

    assert lightgbm == "lightgbm<5,>=4.6"
