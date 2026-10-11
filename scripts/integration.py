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
        subprocess.run(["go", "build", "-o", str(path / "mdns-responder"), "scripts/testdata/mdns-responder.go"],
                       env={**os.environ, "CGO_ENABLED": "0"}, check=True)
        subprocess.run(["go", "build", "-o", str(path / "vrrp-sender"), "scripts/testdata/vrrp-sender.go"],
                       env={**os.environ, "CGO_ENABLED": "0"}, check=True)
        config = {"listen": "0.0.0.0:8080", "interfaces": ["must-not-use"],
                  "node_profiles": {"sensor-a": {"interfaces": ["eth0"]},
                                    "sensor-b": {"interfaces": ["other-nic.30"]}},
                  "networks": ["192.0.2.0/29"],
                  "data_dir": "/data", "stale_after": "10s", "offline_after": "25s",
                  "startup_grace": "1s", "scan_interval": "10s", "active_arp": False}
        (path / "config.json").write_text(json.dumps(config))
        env = path / "secrets.env"
        env.write_text("TUNWATCH_API_TOKEN=" + token + "\nTUNWATCH_NODE_NAME=sensor-a\n")
        env.chmod(0o600)
        try:
            # Docker --internal drops forwarded multicast as outside the IPv4 subnet.
            # Omit the bridge gateway address and NAT instead: local multicast works,
            # but these containers have no reachable default gateway or external egress.
            docker("network", "create", "--opt", "com.docker.network.bridge.inhibit_ipv4=true",
                   "--opt", "com.docker.network.bridge.enable_ip_masquerade=false",
                   "--subnet", "192.0.2.0/29", network)
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
                # The test bridge has no gateway address, NAT, or host-published ports.
                return json.loads(docker("exec", inspector, "sh", "-c",
                    'wget -qO- --header "Authorization: Bearer $TUNWATCH_API_TOKEN" '
                    + 'http://192.0.2.2:8080' + endpoint))

            wait_for(lambda: get("/api/v1/status")["ready"])
            status = get("/api/v1/status")
            assert status["node_name"] == "sensor-a"
            assert status["config"]["interfaces"] == ["eth0"]
            assert "node_profiles" not in status["config"]
            rejected = subprocess.run(["docker", "run", "--rm", "--network", "none", "--cap-drop", "ALL",
                "--env-file", str(env), "-e", "TUNWATCH_NODE_NAME=unregistered",
                "-v", str(path) + ":/etc/tunwatch:ro", image], capture_output=True, text=True)
            assert rejected.returncode != 0 and "no node_profiles entry" in rejected.stderr
            print("PASS: node-specific scope is selected; unregistered node fails closed", flush=True)
            proc = docker("run", "--rm", "--network", "none", "--pid", "container:" + sensor,
                          "--cap-drop", "ALL", "alpine:3.23", "cat", "/proc/1/status")
            fields = dict(line.split(":", 1) for line in proc.splitlines() if ":" in line)
            assert int(fields["CapEff"].strip(), 16) == 0
            assert int(fields["CapPrm"].strip(), 16) == 0
            assert int(fields["CapInh"].strip(), 16) == 0
            assert fields["NoNewPrivs"].strip() == "1"
            docker("run", "-d", "--name", client, "--network", network, "--ip", "192.0.2.3",
                   "-v", str(path) + ":/fixture:ro", "alpine:3.23", "/fixture/mdns-responder")
            created.append(client)
            docker("exec", client, "ping", "-c", "2", "192.0.2.2")

            def device(status):
                return next((d for d in get("/api/v1/devices")["devices"]
                             if "192.0.2.3" in d["ips"] and d["status"] == status), None)

            found = wait_for(lambda: device("online"))
            assert "arp" in found["sources"]
            assert "192.0.2.2" not in [ip for d in get("/api/v1/devices")["devices"] for ip in d["ips"]]
            wait_for(lambda: (device("online") or {}).get("hostname", {}).get("value") == "integration-client.local")
            assert device("online")["hostname"]["source"] == "mdns"
            assert get("/api/v1/status")["capture"]["interfaces"][0]["mdns_sent"] > 0
            print("PASS: active mDNS PTR query and response enrich the matching client", flush=True)
            docker("exec", client, "/fixture/vrrp-sender")
            vip = wait_for(lambda: get("/api/v1/vips?ip=192.0.2.1")["vips"])
            assert vip == [{"interface": "eth0", "ip": "192.0.2.1", "source": "vrrp_advertisement", "vrid": 42}]
            assert not device("online")["vip_only"]
            assert any(v["ip"] == "192.0.2.1" for v in device("online")["vips"])
            print("PASS: real VRRP advertisement crosses the BPF filter and marks the VIP without hiding the physical client", flush=True)
            print("PASS: NET_RAW capture with capabilities dropped after startup, ARP sender attribution, own-address exclusion", flush=True)
            docker("stop", "-t", "1", client)
            wait_for(lambda: device("stale"))
            wait_for(lambda: device("offline"))
            print("PASS: observed -> unconfirmed -> inferred offline", flush=True)
            events = get("/api/v1/events?limit=100")["events"]
            assert any(e["type"] == "offline" and e["device"]["id"] == found["id"] for e in events)
            config["active_arp"] = True
            config["networks"] = []  # infer the connected IPv4 scope
            (path / "config.json").write_text(json.dumps(config))
            arp_start = time.monotonic()
            docker("restart", "-t", "8", sensor)
            wait_for(lambda: get("/api/v1/status")["ready"])
            assert any(e["type"] == "offline" for e in get("/api/v1/events")["events"])
            docker("start", client)
            wait_for(lambda: device("online"))
            assert get("/api/v1/status")["capture"]["interfaces"][0]["probe_targets"] == 5
            sent = get("/api/v1/status")["capture"]["interfaces"][0]["arp_sent"]
            assert 0 < sent <= (time.monotonic() - arp_start) * 2 + 1
            print("PASS: compressed history survives restart; bounded ARP in inferred scope rediscovers silent client", flush=True)
        except Exception:
            if sensor in created:
                print(docker("logs", sensor))
            if client in created:
                print(docker("logs", client))
            raise
        finally:
            for name in reversed(created):
                subprocess.run(["docker", "rm", "-f", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            subprocess.run(["docker", "network", "rm", network], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            subprocess.run(["docker", "volume", "rm", volume], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
