#!/usr/bin/env python3
"""Create a private local configuration without overwriting existing secrets."""
import base64
import os
from pathlib import Path
import secrets


def main():
    root = Path(__file__).resolve().parent.parent
    target = root / ".env"
    keys = {
        "NUDGE_LINK_SIGNING_KEY", "FEEDBIN_ENCRYPTION_KEY",
        "NUDGY_AI_ENCRYPTION_KEY", "NUDGY_CODING_ENCRYPTION_KEY",
        "NUDGY_TRACKER_ENCRYPTION_KEY",
    }
    lines = []
    for line in (root / ".env.example").read_text().splitlines():
        name = line.split("=", 1)[0]
        if name in keys:
            line = name + "=" + base64.b64encode(secrets.token_bytes(32)).decode()
        lines.append(line)
    try:
        fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        raise SystemExit(".env already exists; preserved without changes. See .env.example for new options.")
    with os.fdopen(fd, "w") as output:
        output.write("\n".join(lines) + "\n")
    print("Created .env with independent encryption keys and mode 0600.")
    print("Set your Slack app credentials and public URL before using Slack.")


if __name__ == "__main__":
    main()
