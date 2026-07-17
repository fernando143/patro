#!/usr/bin/env python3
"""Bootstrap the meeting-scribe installation wizard.

This script does not require the package to be installed. It creates the
virtual environment, installs meeting-scribe in editable mode, and then runs
the interactive wizard.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

PROJECT_DIR = Path(__file__).resolve().parent
VENV_PYTHON = PROJECT_DIR / ".venv" / "bin" / "python"


def main() -> int:
    if sys.version_info < (3, 11):
        print(f"Python {sys.version_info.major}.{sys.version_info.minor} is too old. Python 3.11+ is required.")
        return 1

    if not VENV_PYTHON.exists():
        print("Creating virtual environment ...")
        subprocess.run([sys.executable, "-m", "venv", str(PROJECT_DIR / ".venv")], check=True)

    print("Installing meeting-scribe ...")
    subprocess.run([str(VENV_PYTHON), "-m", "pip", "install", "-e", str(PROJECT_DIR)], check=True)

    print("Starting installer ...")
    return subprocess.run([str(VENV_PYTHON), "-m", "scribe", "install"]).returncode


if __name__ == "__main__":
    sys.exit(main())
