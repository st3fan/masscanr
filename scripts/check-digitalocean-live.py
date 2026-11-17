#!/usr/bin/env python3
"""Run one explicitly selected DigitalOcean live integration check.

Default Go tests never include these checks. This helper reads only the two
DigitalOcean secrets from ignored .env and never prints their values.
"""

import argparse
import os
from pathlib import Path
import subprocess


ROOT = Path(__file__).resolve().parent.parent
GATES = {
    "catalog": "MASSCANR_DO_LIVE_CATALOG",
    "keys": "MASSCANR_DO_LIVE_KEYS",
    "droplet": "MASSCANR_DO_LIVE_DROPLET",
}


def load_secrets(env: dict[str, str]) -> None:
    path = ROOT / ".env"
    if not path.exists():
        raise SystemExit("ignored .env is required for DigitalOcean live checks")
    for line in path.read_text().splitlines():
        key, separator, value = line.partition("=")
        if not separator or key not in {"DIGITALOCEAN_API_TOKEN", "DIGITALOCEAN_SSH_SEED"}:
            continue
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        env.setdefault(key, value)
    for key in ("DIGITALOCEAN_API_TOKEN", "DIGITALOCEAN_SSH_SEED"):
        if not env.get(key):
            raise SystemExit(f"{key} is required in .env or the environment")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("check", choices=GATES)
    parser.add_argument("--region", default="tor1")
    parser.add_argument("--size", default="s-1vcpu-1gb")
    args = parser.parse_args()

    env = os.environ.copy()
    load_secrets(env)
    env["MASSCANR_DO_LIVE"] = "1"
    env[GATES[args.check]] = "1"
    env["MASSCANR_DO_LIVE_REGION"] = args.region
    env["MASSCANR_DO_LIVE_SIZE"] = args.size
    test_name = {
        "catalog": "TestLiveDigitalOceanCatalog",
        "keys": "TestLiveDigitalOceanKeyLifecycle",
        "droplet": "TestLiveDigitalOceanDropletLifecycle",
    }[args.check]
    print(f"Running DigitalOcean {args.check} check in {args.region} ({args.size})", flush=True)
    return subprocess.run(
        ["go", "test", "-tags=digitalocean_live", "./internal/cloud/digitalocean", "-run", f"^{test_name}$", "-count=1", "-v"],
        cwd=ROOT,
        env=env,
        check=False,
    ).returncode


if __name__ == "__main__":
    raise SystemExit(main())
