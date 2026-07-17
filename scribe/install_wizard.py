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
from rich.console import Console
from rich.panel import Panel
from rich.prompt import Confirm, Prompt
from rich.status import Status

from .config import DEFAULT_CONFIG_PATH, PROJECT_DIR


console = Console()


def _prompt_required(text: str, **kwargs) -> str:
    while True:
        value = Prompt.ask(text, console=console, **kwargs).strip()
        if value:
            return value
        console.print("[red]This field is required.[/red]")


def _prompt_path(text: str, example: str) -> Path:
    """Ask for an absolute path. Never create it unless the user confirms."""
    while True:
        raw = _prompt_required(text, default=example)
        path = Path(raw).expanduser().resolve()
        if not path.is_absolute():
            console.print("[red]Please provide an absolute path (e.g. /home/fernando/Videos/obs).[/red]")
            continue
        if not path.exists():
            if Confirm.ask(
                f"Folder does not exist: [cyan]{path}[/cyan]. Create it?",
                console=console,
                default=False,
            ):
                path.mkdir(parents=True, exist_ok=True)
                return path
            console.print("[yellow]Please provide a different path.[/yellow]")
            continue
        return path


def _check_python() -> None:
    version = sys.version_info
    if version < (3, 11):
        console.print(f"[red]Python {version.major}.{version.minor} is too old. Python 3.11+ is required.[/red]")
        sys.exit(1)
    console.print(f"[green]Python {version.major}.{version.minor}.{version.micro} OK[/green]")


def _venv_python() -> Path:
    return PROJECT_DIR / ".venv" / "bin" / "python"


def _venv_pip() -> Path:
    return PROJECT_DIR / ".venv" / "bin" / "pip"


def _ensure_venv() -> None:
    venv_python = _venv_python()
    if venv_python.exists():
        console.print("[green]Virtual environment found[/green]")
        return
    with Status("[bold green]Creating virtual environment...[/bold green]", console=console):
        subprocess.run([sys.executable, "-m", "venv", str(PROJECT_DIR / ".venv")], check=True)
    console.print("[green]Virtual environment created[/green]")


def _ensure_package_installed() -> None:
    venv_pip = _venv_pip()
    result = subprocess.run(
        [str(venv_pip), "show", "meeting-scribe"],
        capture_output=True,
        text=True,
    )
    if result.returncode == 0:
        console.print("[green]Package already installed in editable mode[/green]")
        return
    with Status("[bold green]Installing meeting-scribe...[/bold green]", console=console):
        subprocess.run([str(venv_pip), "install", "-e", str(PROJECT_DIR)], check=True)
    console.print("[green]Package installed[/green]")


def _find_binary(name: str) -> str | None:
    return shutil.which(name)


def _prompt_binary(name: str) -> str:
    found = _find_binary(name)
    if found:
        use = Confirm.ask(
            f"Found '[bold]{name}[/bold]' at [cyan]{found}[/cyan]. Use it?",
            console=console,
            default=True,
        )
        if use:
            return found
    while True:
        raw = _prompt_required(f"Enter the absolute path to the '[bold]{name}[/bold]' executable")
        path = Path(raw).expanduser().resolve()
        if path.is_file() and os.access(path, os.X_OK):
            return str(path)
        console.print(f"[red]'{path}' is not an executable file. Please try again.[/red]")


def _prompt_api_key() -> str:
    return _prompt_required("Enter your AssemblyAI API key")


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
    console.print(f"[green]Updated {DEFAULT_CONFIG_PATH}[/green]")


def _install_service(api_key: str) -> None:
    system = platform.system()
    if system == "Linux":
        _install_linux_service(api_key)
    elif system == "Darwin":
        _install_macos_service(api_key)
    else:
        console.print(f"[red]Unsupported OS: {system}. Service installation skipped.[/red]")
        sys.exit(1)


def _install_linux_service(api_key: str) -> None:
    install_script = PROJECT_DIR / "install" / "install.sh"
    with Status("[bold green]Installing systemd user service...[/bold green]", console=console):
        subprocess.run(["bash", str(install_script)], check=True)

    override_dir = Path.home() / ".config" / "systemd" / "user" / "scribe.service.d"
    override_dir.mkdir(parents=True, exist_ok=True)
    override_file = override_dir / "override.conf"
    with open(override_file, "w", encoding="utf-8") as fh:
        fh.write("[Service]\n")
        fh.write(f"Environment=ASSEMBLYAI_API_KEY={api_key}\n")
    console.print(f"[green]Wrote API key to {override_file}[/green]")

    with Status("[bold green]Starting service...[/bold green]", console=console):
        subprocess.run(["systemctl", "--user", "daemon-reload"], check=True)
        subprocess.run(["systemctl", "--user", "restart", "scribe"], check=True)
        subprocess.run(["systemctl", "--user", "enable", "scribe"], check=True)
    console.print("[green]Service started and enabled[/green]")


def _install_macos_service(api_key: str) -> None:
    install_script = PROJECT_DIR / "install" / "install.sh"
    with Status("[bold green]Installing LaunchAgent...[/bold green]", console=console):
        subprocess.run(["bash", str(install_script)], check=True)

    plist = Path.home() / "Library" / "LaunchAgents" / "com.scribe.plist"
    text = plist.read_text(encoding="utf-8")
    env_block = (
        "\n    <key>EnvironmentVariables</key>\n"
        "    <dict>\n"
        f"        <key>ASSEMBLYAI_API_KEY</key>\n"
        f"        <string>{api_key}</string>\n"
        "    </dict>"
    )
    text = text.replace("</dict>", env_block + "\n</dict>", 1)
    plist.write_text(text, encoding="utf-8")
    console.print(f"[green]Wrote API key to {plist}[/green]")

    with Status("[bold green]Starting LaunchAgent...[/bold green]", console=console):
        subprocess.run(["launchctl", "bootout", f"gui/{os.getuid()}/com.scribe"], check=False)
        subprocess.run(["launchctl", "bootstrap", f"gui/{os.getuid()}", str(plist)], check=True)
    console.print("[green]LaunchAgent started[/green]")


def run() -> int:
    console.print(
        Panel.fit(
            "[bold blue]meeting-scribe installer[/bold blue]\n\n"
            "This wizard will:\n"
            "  1. Check your Python environment.\n"
            "  2. Ask for your AssemblyAI API key (transcription).\n"
            "  3. Ask where scribe should listen for recordings.\n"
            "  4. Ask where to write the knowledge library.\n"
            "  5. Ask which local AI you want to write the notes.\n"
            "  6. Install and start a user-level background service.",
            title="Welcome",
            border_style="blue",
        )
    )

    _check_python()
    _ensure_venv()
    _ensure_package_installed()
    console.print()

    api_key = _prompt_api_key()
    console.print()

    inbox = _prompt_path(
        "Enter the absolute path to your recordings folder",
        example="/home/fernando/Videos/obs",
    )
    console.print()

    library = _prompt_path(
        "Enter the absolute path to your knowledge library folder",
        example="/home/fernando/projects/meeting-scribe/knowledge",
    )
    console.print()

    backend = ""
    while backend not in ("kimi", "claude"):
        backend = Prompt.ask(
            "Which local AI should write the knowledge library?",
            console=console,
            choices=["kimi", "claude"],
            default="kimi",
        ).lower()

    binary_path = _prompt_binary(backend)
    console.print()

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
    console.print()

    _install_service(api_key)
    console.print()

    console.print(Panel.fit("[bold green]Installation complete[/bold green]", border_style="green"))
    if platform.system() == "Linux":
        console.print("View logs: [cyan]journalctl --user -u scribe -f[/cyan]")
        console.print("Check status: [cyan]systemctl --user status scribe[/cyan]")
    elif platform.system() == "Darwin":
        console.print("View logs: [cyan]tail -f /tmp/scribe.out.log /tmp/scribe.err.log[/cyan]")
    return 0


if __name__ == "__main__":
    sys.exit(run())
