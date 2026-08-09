import pytest

from bellwether_harness.runs import RunIdentity, canonical_config_hash


def test_config_hash_is_stable_for_semantically_identical_mappings() -> None:
    first = {"model": {"penalty": "l2", "regularization": 0.5}, "seed": 7}
    second = {"seed": 7, "model": {"regularization": 0.5, "penalty": "l2"}}

    assert canonical_config_hash(first) == canonical_config_hash(second)


def test_config_hash_changes_when_configuration_changes() -> None:
    baseline = {"model": "logistic_regression", "seed": 7}
    changed = {"model": "logistic_regression", "seed": 8}

    assert canonical_config_hash(baseline) != canonical_config_hash(changed)


def test_run_identity_captures_git_config_and_snapshot_provenance() -> None:
    config = {"seed": 7, "model": "logistic_regression"}

    identity = RunIdentity.from_config(
        git_sha="abc123",
        config=config,
        data_snapshot_id="snapshot-2026-01-01",
    )

    assert identity.git_sha == "abc123"
    assert identity.config_hash == canonical_config_hash(config)
    assert identity.data_snapshot_id == "snapshot-2026-01-01"


@pytest.mark.parametrize(
    ("git_sha", "config_hash", "data_snapshot_id"),
    [
        ("", "a" * 64, "snapshot-1"),
        ("abc123", "", "snapshot-1"),
        ("abc123", "a" * 64, "   "),
    ],
)
def test_run_identity_rejects_empty_provenance_fields(
    git_sha: str, config_hash: str, data_snapshot_id: str
) -> None:
    with pytest.raises(ValueError, match="must not be empty"):
        RunIdentity(
            git_sha=git_sha,
            config_hash=config_hash,
            data_snapshot_id=data_snapshot_id,
        )
