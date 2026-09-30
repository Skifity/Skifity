"""Where the catalogue scripts keep what they fetch and convert between runs.

They used to share /tmp/tpl. A fixed name in a directory every user on the
machine can write is one somebody else can create first, as a symlink to a file
of theirs, and the scripts would then read what they wrote or overwrite what
they own. This is a directory of the user's own, created private;
SKIFITY_TEMPLATE_WORK moves it.
"""
import json
import os
from pathlib import Path


def path(name: str) -> str:
    base = Path(os.environ.get("SKIFITY_TEMPLATE_WORK") or Path.home() / ".cache" / "skifity-templates")
    base.mkdir(mode=0o700, parents=True, exist_ok=True)
    return str(base / name)


def read_json(name: str):
    with open(path(name), encoding="utf-8") as handle:
        return json.load(handle)


def write_json(name: str, value) -> None:
    with open(path(name), "w", encoding="utf-8") as handle:
        json.dump(value, handle, indent=1)
