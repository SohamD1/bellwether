"""Deterministic run provenance identifiers."""

from __future__ import annotations

import hashlib
import json
from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any


def canonical_config_hash(config: Mapping[str, Any]) -> str:
    """Return a SHA-256 hash of a mapping serialized as canonical JSON."""

    if not isinstance(config, Mapping):
        raise ValueError("config must be a mapping")
    try:
        canonical_json = json.dumps(
            config,
            ensure_ascii=False,
            separators=(",", ":"),
            sort_keys=True,
            allow_nan=False,
        )
    except (TypeError, ValueError) as error:
        raise ValueError("config must be JSON-serializable with finite values") from error
    return hashlib.sha256(canonical_json.encode("utf-8")).hexdigest()


@dataclass(frozen=True)
class RunIdentity:
    """Immutable provenance required to reproduce an evaluation run."""

    git_sha: str
    config_hash: str
    data_snapshot_id: str

    def __post_init__(self) -> None:
        for field_name in ("git_sha", "config_hash", "data_snapshot_id"):
            value = getattr(self, field_name)
            if not isinstance(value, str) or not value.strip():
                raise ValueError(f"{field_name} must not be empty")

    @classmethod
    def from_config(
        cls,
        *,
        git_sha: str,
        config: Mapping[str, Any],
        data_snapshot_id: str,
    ) -> RunIdentity:
        """Build a run identity with the canonical hash of ``config``."""

        return cls(
            git_sha=git_sha,
            config_hash=canonical_config_hash(config),
            data_snapshot_id=data_snapshot_id,
        )
