#!/usr/bin/env python3
"""Exercise real AF_PACKET capture on an isolated Docker network, never the host LAN."""
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time
import uuid


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True).strip()


def wait_for(fn, timeout=40):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            result = fn()
            if result:
                return result
        except (OSError, ValueError, subprocess.CalledProcessError):
            pass
        time.sleep(1)
    raise AssertionError("condition did not become true before timeout")


def main():
    prefix = "tunwatch-test-" + uuid.uuid4().hex[:8]
    network, sensor, client, volume, inspector = (prefix + s for s in ("-net", "-sensor", "-client", "-data", "-inspector"))
    created = []
    token = secrets.token_urlsafe(32)
    image = os.environ.get("TUNWATCH_TEST_IMAGE", "tunwatch:local")
    with tempfile.TemporaryDirectory(prefix="tunwatch-integration-") as directory:
        path = Path(directory)
        path.chmod(0o755)
        config = {"listen": "0.0.0.0:8080", "interfaces": ["eth0"], "networks": ["192.0.2.0/29"],
                  "data_dir": "/data", "stale_after": "10s", "offline_after": "25s",
                  "startup_grace": "1s", "scan_interval": "10s", "active_arp": False}
        (path / "config.json").write_text(json.dumps(config))
        env = path / "secrets.env"
        env.write_text("TUNWATCH_API_TOKEN=" + token + "\n")
        env.chmod(0o600)
        try:
            docker("network", "create", "--internal", "--subnet", "192.0.2.0/29", network)
            docker("volume", "create", volume)
            docker("run", "-d", "--name", sensor, "--network", network, "--ip", "192.0.2.2",
                   "--cap-drop", "ALL", "--cap-add", "NET_RAW", "--read-only",
                   "--security-opt", "no-new-privileges", "--env-file", str(env),
                   "-v", str(path) + ":/etc/tunwatch:ro",
                   "-v", volume + ":/data", image)
            created.append(sensor)
            docker("run", "-d", "--name", inspector, "--network", network, "--ip", "192.0.2.4",
                   "--env-file", str(env), "alpine:3.23", "sleep", "300")
            created.append(inspector)

            def get(endpoint):
                # The internal bridge has no host-published ports or external egress.
                return json.loads(docker("exec", inspector, "sh", "-c",
                    'wget -qO- --header "Authorization: Bearer $TUNWATCH_API_TOKEN" '
                    + 'http://192.0.2.2:8080' + endpoint))

            wait_for(lambda: get("/api/v1/status")["ready"])
            proc = docker("run", "--rm", "--network", "none", "--pid", "container:" + sensor,
                          "--cap-drop", "ALL", "alpine:3.23", "cat", "/proc/1/status")
            fields = dict(line.split(":", 1) for line in proc.splitlines() if ":" in line)
            assert int(fields["CapEff"].strip(), 16) == 0
            assert int(fields["CapPrm"].strip(), 16) == 0
            assert int(fields["CapInh"].strip(), 16) == 0
            assert fields["NoNewPrivs"].strip() == "1"
            docker("run", "-d", "--name", client, "--network", network, "--ip", "192.0.2.3",
                   "alpine:3.23", "sleep", "300")
            created.append(client)
            docker("exec", client, "ping", "-c", "2", "192.0.2.2")

            def device(status):
                return next((d for d in get("/api/v1/devices")["devices"]
                             if "192.0.2.3" in d["ips"] and d["status"] == status), None)

            found = wait_for(lambda: device("online"))
            assert "arp" in found["sources"]
            assert "192.0.2.2" not in [ip for d in get("/api/v1/devices")["devices"] for ip in d["ips"]]
            print("PASS: NET_RAW capture with capabilities dropped after startup, ARP sender attribution, own-address exclusion", flush=True)
            docker("stop", "-t", "1", client)
            wait_for(lambda: device("stale"))
            wait_for(lambda: device("offline"))
            print("PASS: observed -> unconfirmed -> inferred offline", flush=True)
            events = get("/api/v1/events?limit=100")["events"]
            assert any(e["type"] == "offline" and e["device"]["id"] == found["id"] for e in events)
            config["active_arp"] = True
            (path / "config.json").write_text(json.dumps(config))
            docker("restart", "-t", "8", sensor)
            wait_for(lambda: get("/api/v1/status")["ready"])
            assert any(e["type"] == "offline" for e in get("/api/v1/events")["events"])
            docker("start", client)
            wait_for(lambda: device("online"))
            assert get("/api/v1/status")["capture"]["interfaces"][0]["probe_targets"] == 5
            print("PASS: compressed history survives restart; active ARP rediscovers silent client", flush=True)
        except Exception:
            if sensor in created:
                print(docker("logs", sensor))
            raise
        finally:
            for name in reversed(created):
                subprocess.run(["docker", "rm", "-f", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            subprocess.run(["docker", "network", "rm", network], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            subprocess.run(["docker", "volume", "rm", volume], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
