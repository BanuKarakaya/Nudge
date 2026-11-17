#!/usr/bin/env python3
"""One-time app icon setup. The configuration token never belongs in the bot's env."""
import argparse
import json
import os
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--app-id", required=True)
    parser.add_argument("--icon-url", required=True,
                        help="Public HTTPS URL, e.g. https://your-host/dashboard/nudgy-icon.png")
    args = parser.parse_args()
    if not args.icon_url.startswith("https://"):
        parser.error("--icon-url must use HTTPS")
    token = os.environ.get("SLACK_APP_CONFIG_TOKEN")
    if not token:
        parser.error("Set SLACK_APP_CONFIG_TOKEN to an app configuration token with app_configurations:write")
    request = urllib.request.Request(
        "https://slack.com/api/apps.icon.set",
        data=json.dumps({"app_id": args.app_id, "url": args.icon_url}).encode(),
        headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            result = json.load(response)
    except urllib.error.HTTPError as error:
        raise SystemExit("Slack returned HTTP " + str(error.code)) from None
    if not result.get("ok"):
        # Slack error codes contain no request/credential bodies.
        code = result.get("error", "unknown_error")
        raise SystemExit("Slack icon setup failed: " + str(code))
    print("Nudgy app icon updated. All installations of this app use its icon.")


if __name__ == "__main__":
    main()
