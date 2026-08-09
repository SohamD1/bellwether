"""Small cross-platform task runner for local development."""

from __future__ import annotations

import argparse
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PYTHON_ROOT = ROOT / "python"


def run(*command: str, cwd: Path = ROOT) -> None:
    print(f"> {' '.join(command)}", flush=True)
    subprocess.run(command, cwd=cwd, check=True)


def go_files(root: Path = ROOT) -> list[str]:
    files: list[str] = []
    for path in root.rglob("*.go"):
        relative = path.relative_to(root)
        if {".git", ".pytest-tmp", ".venv", ".worktrees"} & set(relative.parts):
            continue
        if relative.parts[:2] in {
            ("contracts", "broadcast"),
            ("contracts", "cache"),
            ("contracts", "lib"),
            ("contracts", "out"),
        }:
            continue
        files.append(str(relative))
    return sorted(files)


def format_go(*, check: bool) -> None:
    files = go_files()
    if not files:
        return

    if not check:
        run("gofmt", "-w", *files)
        return

    result = subprocess.run(
        ["gofmt", "-l", *files],
        cwd=ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    if result.stdout.strip():
        print(result.stdout, end="")
        raise SystemExit("Go files need formatting; run python scripts/dev.py fmt")


def format_all() -> None:
    format_go(check=False)
    run("uv", "run", "ruff", "format", ".", "../scripts", cwd=PYTHON_ROOT)


def lint() -> None:
    format_go(check=True)
    if go_files():
        run("go", "vet", "./...")
    run("uv", "run", "ruff", "format", "--check", ".", "../scripts", cwd=PYTHON_ROOT)
    run("uv", "run", "ruff", "check", ".", "../scripts", cwd=PYTHON_ROOT)


def test() -> None:
    if go_files():
        run("go", "test", "./...")
    run("uv", "run", "pytest", cwd=PYTHON_ROOT)


def test_race() -> None:
    if go_files():
        run("go", "test", "-race", "./...")


def check() -> None:
    lint()
    test()


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("fmt", "lint", "test", "test-race", "check"))
    args = parser.parse_args()

    commands = {
        "fmt": format_all,
        "lint": lint,
        "test": test,
        "test-race": test_race,
        "check": check,
    }
    commands[args.command]()


if __name__ == "__main__":
    main()
