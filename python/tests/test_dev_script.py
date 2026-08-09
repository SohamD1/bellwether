from __future__ import annotations

import importlib.util
import subprocess
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("bellwether_dev", REPO_ROOT / "scripts" / "dev.py")
assert SPEC is not None and SPEC.loader is not None
DEV = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DEV)


def test_go_files_skips_local_worktrees_and_generated_outputs(tmp_path: Path) -> None:
    included = [
        tmp_path / "internal" / "keep.go",
        tmp_path / "service" / "contracts" / "out" / "source.go",
    ]
    excluded = [
        tmp_path / ".worktrees" / "feature" / "bad.go",
        tmp_path / ".pytest-tmp" / "bad.go",
        tmp_path / "contracts" / "lib" / "forge-std" / "bad.go",
        tmp_path / "contracts" / "cache" / "bad.go",
        tmp_path / "contracts" / "out" / "bad.go",
        tmp_path / "contracts" / "broadcast" / "bad.go",
    ]

    for path in included:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("package included\n", encoding="utf-8")

    for path in excluded:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("package badly_formatted\n\nfunc f( ){ }\n", encoding="utf-8")

    assert DEV.go_files(tmp_path) == [
        str(Path("internal") / "keep.go"),
        str(Path("service") / "contracts" / "out" / "source.go"),
    ]


def test_documented_local_outputs_are_git_ignored() -> None:
    documented_outputs = (".demo/example.parquet", "config.toml", "training.parquet")

    for output in documented_outputs:
        result = subprocess.run(
            ["git", "check-ignore", "--quiet", "--", output],
            cwd=REPO_ROOT,
            check=False,
        )
        assert result.returncode == 0, f"documented local output is not ignored: {output}"
