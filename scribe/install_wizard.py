"""Interactive installation wizard for meeting-scribe.

Guides the user from a fresh clone to a running user-level service.
Called by ``python -m scribe install`` after the package is installed,
or bootstrapped by ``install-wizard.py`` in the project root.
"""

from __future__ import annotations

import os
import platform
import shutil
import subprocess
import sys
from pathlib import Path

import yaml

from .config import DEFAULT_CONFIG_PATH, PROJECT_DIR


WELCOME = """
Welcome to meeting-scribe installer.

This wizard will:
  1. Check your Python environment.
  2. Ask for your AssemblyAI API key (transcription).
  3. Ask where scribe should listen for recordings (inbox).
  4. Ask where to write the knowledge library.
  5. Ask which local AI you want to write the notes (Kimi or Claude).
  6. Install and start a user-level background service.
""".strip()


def _print(message: str = "") -> None:
    print(message)


def _prompt(text: str) -> str:
    return input(text).strip()


def _prompt_required(text: str) -> str:
    while True:
        value = _prompt(text)
        if value:
            return value
        _print("This field is required.")


def _prompt_yes_no(text: str, default: bool = False) -> bool:
    suffix = " [Y/n]" if default else " [y/N]"
    while True:
        answer = _prompt(text + suffix).lower()
        if not answer:
            return default
        if answer in ("y", "yes"):
            return True
        if answer in ("n", "no"):
            return False
        _print("Please answer yes or no.")


def _prompt_path(text: str, must_exist: bool = True) -> Path:
    """Ask for a path. Never create it unless the user confirms."""
    while True:
        raw = _prompt_required(text)
        path = Path(raw).expanduser().resolve()
        if not path.is_absolute():
            _print("Please provide an absolute path.")
            continue
        if not path.exists():
            if _prompt_yes_no(f"Folder does not exist: {path}. Create it?", default=False):
                path.mkdir(parents=True, exist_ok=True)
                return path
            _print("Please provide a different path.")
            continue
        return path


def _check_python() -> None:
    version = sys.version_info
    if version < (3, 11):
        _print(f"Python {version.major}.{version.minor} is too old. Python 3.11+ is required.")
        sys.exit(1)
    _print(f"Python {version.major}.{version.minor}.{version.micro} OK.")


def _venv_python() -> Path:
    return PROJECT_DIR / ".venv" / "bin" / "python"


def _venv_pip() -> Path:
    return PROJECT_DIR / ".venv" / "bin" / "pip"


def _ensure_venv() -> None:
    venv_python = _venv_python()
    if venv_python.exists():
        _print("Virtual environment found.")
        return
    _print("Creating virtual environment ...")
    subprocess.run([sys.executable, "-m", "venv", str(PROJECT_DIR / ".venv")], check=True)
    _print("Virtual environment created.")


def _ensure_package_installed() -> None:
    venv_pip = _venv_pip()
    result = subprocess.run(
        [str(venv_pip), "show", "meeting-scribe"],
        capture_output=True,
        text=True,
    )
    if result.returncode == 0:
        _print("Package already installed in editable mode.")
        return
    _print("Installing meeting-scribe in editable mode ...")
    subprocess.run([str(venv_pip), "install", "-e", str(PROJECT_DIR)], check=True)
    _print("Package installed.")


def _find_binary(name: str) -> str | None:
    path = shutil.which(name)
    return path


def _prompt_binary(name: str) -> str:
    found = _find_binary(name)
    if found:
        if _prompt_yes_no(f"Found '{name}' at {found}. Use it?", default=True):
            return found
    while True:
        raw = _prompt_required(f"Enter the absolute path to the '{name}' executable: ")
        path = Path(raw).expanduser().resolve()
        if path.is_file() and os.access(path, os.X_OK):
            return str(path)
        _print(f"'{path}' is not an executable file. Please try again.")


def _prompt_api_key() -> str:
    while True:
        key = _prompt_required("Enter your AssemblyAI API key: ")
        if key:
            return key


def _load_existing_config() -> dict:
    if not DEFAULT_CONFIG_PATH.exists():
        return {}
    try:
        with open(DEFAULT_CONFIG_PATH, "r", encoding="utf-8") as fh:
            return yaml.safe_load(fh) or {}
    except (OSError, yaml.YAMLError):
        return {}


def _write_config(config: dict) -> None:
    with open(DEFAULT_CONFIG_PATH, "w", encoding="utf-8") as fh:
        yaml.safe_dump(config, fh, sort_keys=False, default_flow_style=False, allow_unicode=True)
    _print(f"Updated {DEFAULT_CONFIG_PATH}")


def _install_service(api_key: str) -> None:
    system = platform.system()
    if system == "Linux":
        _install_linux_service(api_key)
    elif system == "Darwin":
        _install_macos_service(api_key)
    else:
        _print(f"Unsupported OS: {system}. Service installation skipped.")
        sys.exit(1)


def _install_linux_service(api_key: str) -> None:
    install_script = PROJECT_DIR / "install" / "install.sh"
    _print("Installing systemd user service ...")
    subprocess.run(["bash", str(install_script)], check=True)

    override_dir = Path.home() / ".config" / "systemd" / "user" / "scribe.service.d"
    override_dir.mkdir(parents=True, exist_ok=True)
    override_file = override_dir / "override.conf"
    with open(override_file, "w", encoding="utf-8") as fh:
        fh.write("[Service]\n")
        fh.write(f"Environment=ASSEMBLYAI_API_KEY={api_key}\n")
    _print(f"Wrote API key to {override_file}")

    subprocess.run(["systemctl", "--user", "daemon-reload"], check=True)
    subprocess.run(["systemctl", "--user", "restart", "scribe"], check=True)
    subprocess.run(["systemctl", "--user", "enable", "scribe"], check=True)
    _print("Service started and enabled.")


def _install_macos_service(api_key: str) -> None:
    install_script = PROJECT_DIR / "install" / "install.sh"
    _print("Installing LaunchAgent ...")
    subprocess.run(["bash", str(install_script)], check=True)

    plist = Path.home() / "Library" / "LaunchAgents" / "com.scribe.plist"
    # Inject the API key into the plist. A simple XML insertion is enough
    # because the installer produced a well-known plist structure.
    text = plist.read_text(encoding="utf-8")
    env_block = (
        "\n    <key>EnvironmentVariables</key>\n"
        "    <dict>\n"
        f"        <key>ASSEMBLYAI_API_KEY</key>\n"
        f"        <string>{api_key}</string>\n"
        "    </dict>"
    )
    # Insert before the closing </dict>
    text = text.replace("</dict>", env_block + "\n</dict>", 1)
    plist.write_text(text, encoding="utf-8")
    _print(f"Wrote API key to {plist}")

    subprocess.run(["launchctl", "bootout", f"gui/{os.getuid()}/com.scribe"], check=False)
    subprocess.run(["launchctl", "bootstrap", f"gui/{os.getuid()}", str(plist)], check=True)
    _print("LaunchAgent started.")


def run() -> int:
    _print(WELCOME)
    _print()

    _check_python()
    _ensure_venv()
    _ensure_package_installed()
    _print()

    api_key = _prompt_api_key()
    _print()

    inbox = _prompt_path(
        "Enter the absolute path to your recordings folder (inbox).\n"
        "  Example: /home/fernando/Videos/obs\n"
        "  Path: "
    )
    _print()

    library = _prompt_path(
        "Enter the absolute path to your knowledge library folder.\n"
        "  Example: /home/fernando/projects/meeting-scribe/knowledge\n"
        "  Path: "
    )
    _print()

    backend = ""
    while backend not in ("kimi", "claude"):
        backend = _prompt("Which local AI should write the knowledge library? [kimi/claude]: ").lower()
        if backend not in ("kimi", "claude"):
            _print("Please answer 'kimi' or 'claude'.")

    binary_path = _prompt_binary(backend)
    _print()

    config = _load_existing_config()
    config["inbox"] = str(inbox)
    config["library"] = str(library)
    config["analyzer_backend"] = backend
    if backend == "kimi":
        config["kimi_path"] = binary_path
        config.pop("claude_path", None)
    else:
        config["claude_path"] = binary_path
        config.pop("kimi_path", None)

    _write_config(config)
    _print()

    _install_service(api_key)
    _print()

    _print("Installation complete.")
    if platform.system() == "Linux":
        _print("View logs: journalctl --user -u scribe -f")
        _print("Check status: systemctl --user status scribe")
    elif platform.system() == "Darwin":
        _print("View logs: tail -f /tmp/scribe.out.log /tmp/scribe.err.log")
    return 0


if __name__ == "__main__":
    sys.exit(run())
