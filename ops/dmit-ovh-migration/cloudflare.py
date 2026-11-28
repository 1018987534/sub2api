"""Scoped Cloudflare changes for the canonical zone and dispatcher only."""

import json
import os
import re
from fnmatch import fnmatchcase
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener


HOST = "xiaohondou.com"
CONTROL = "control-origin.xiaohondou.com"
ORIGIN_HOSTS = (
    "gateway-origin.xiaohondou.com", "gateway2-origin.xiaohondou.com",
    "gateway3-origin.xiaohondou.com", "gateway-ovh-origin.xiaohondou.com", CONTROL,
)
RUNTIME = f"https://{CONTROL}/api/v1/gateway-routing/runtime"
WORKER = "sub2api-responses-dispatcher"
ACCOUNT = "3e1de9bb31a7b222215fcec006ccd468"
ZONE = "82fcd03bdccb2cb99a70d57abf424091"
DMIT = "179.255.105.184"
OVH = "147.135.37.51"
ROUTE = HOST + "/*"
LEGACY = frozenset(HOST + suffix for suffix in (
    "/v1/responses*", "/responses*", "/backend-api/codex/responses*",
    "/v1/chat/completions*", "/chat/completions*",
))
KNOWN = LEGACY | {ROUTE}
UUID = re.compile(r"[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}\Z")


class Stop(RuntimeError):
    pass


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Edge:
    def __init__(self):
        self.token = os.environ.get("CLOUDFLARE_API_TOKEN")
        if not self.token:
            raise Stop("no authorized CLOUDFLARE_API_TOKEN; obtain the matching Cloudflare credential before edge operations")
        self.opener = build_opener(NoRedirect())
        self.zone = f"/zones/{ZONE}"
        self.worker = f"/accounts/{ACCOUNT}/workers/scripts/{WORKER}"

    def api(self, method, path, payload=None):
        request = Request("https://api.cloudflare.com/client/v4" + path, method=method,
                          data=None if payload is None else json.dumps(payload).encode(),
                          headers={"Authorization": "Bearer " + self.token, "Content-Type": "application/json"})
        try:
            with self.opener.open(request, timeout=15) as response:
                envelope = json.load(response)
        except HTTPError as exc:
            raise Stop(f"Cloudflare {method} {path.split('?')[0]} returned {exc.code}; read back before retrying") from None
        except (OSError, URLError, ValueError):
            raise Stop(f"Cloudflare {method} {path.split('?')[0]} result is unknown; read back before retrying") from None
        if envelope.get("success") is not True or envelope.get("result_info", {}).get("total_pages", 1) > 1:
            raise Stop("Cloudflare API did not return a complete successful result")
        return envelope["result"]

    def zone_check(self):
        zone = self.api("GET", self.zone)
        if zone["name"] != HOST or zone.get("account", {}).get("id") != ACCOUNT or zone.get("status") != "active":
            raise Stop("Cloudflare account/zone identity check failed")

    def routes(self):
        result = {}
        for row in self.api("GET", self.zone + "/workers/routes"):
            pattern = row["pattern"]
            host = urlsplit(pattern if "://" in pattern else "//" + pattern).hostname or ""
            overlaps = any(fnmatchcase(target, host) for target in (HOST, *ORIGIN_HOSTS))
            if pattern in KNOWN and row.get("script") == WORKER and pattern not in result:
                result[pattern] = row["id"]
            elif overlaps or row.get("script") == WORKER:
                raise Stop("unexpected Cloudflare route overlaps this site, an origin, or this Worker")
        return result

    def active_version(self):
        response = self.api("GET", self.worker + "/deployments")
        deployments = response["deployments"] if isinstance(response, dict) else response
        if not deployments:
            raise Stop("no Worker deployment)")
        versions = max(deployments, key=lambda d: d["created_on"])["versions"]
        if len(versions) != 1 or versions[0]["percentage"] != 100 or not UUID.fullmatch(versions[0]["version_id"]):
            raise Stop("Worker is not deployed to one verified version at 100%")
        return versions[0]["version_id"]

    def check_candidate(self, version):
        if not UUID.fullmatch(version):
            raise Stop("candidate Worker version must be an uploaded UUID")
        candidate = self.api("GET", self.worker + "/versions/" + version)
        if candidate.get("id") != version:
            raise Stop("unexpected candidate Worker identity")
        bindings = {b["name"]: b for b in candidate["resources"]["bindings"]}
        if bindings.get("ROUTING_CONFIG_URL", {}).get("text") != RUNTIME:
            raise Stop("candidate runtime URL does not match the protected origin")
        if not all(bindings.get(key, {}).get("type") == "secret_text" for key in ("ROUTING_CONFIG_TOKEN", "ORIGIN_AUTH_TOKEN")):
            raise Stop("candidate Worker is missing the required secret bindings")

    def dns(self):
        result = {}
        for name in (HOST, CONTROL):
            rows = self.api("GET", self.zone + "/dns_records?" + urlencode({"name": name, "per_page": 100}))
            records = [r for r in rows if r["type"] in ("A", "AAAA", "CNAME") and r["name"] == name]
            if len(records) != 1 or records[0]["type"] != "A" or records[0]["proxied"] is not True:
                raise Stop(f"{name}: expected exactly one proxied A and no AAAA/CNAME; review live DNS")
            record = records[0]
            if record["content"] not in (DMIT, OVH):
                raise Stop(f"{name}: DNS target does not match DMIT or OVH")
            result[name] = {k: record[k] for k in ("id", "content")}
        return result

    def snapshot(self):
        self.zone_check()
        return {"version": self.active_version(), "routes": self.routes(), "dns": self.dns()}

    def move_dns(self, baseline):
        for name, expected in baseline.items():
            current = self.dns()[name]
            if current["id"] != expected["id"] or current["content"] not in (expected["content"], OVH):
                raise Stop(f"{name}: DNS changed after preflight")
            if current["content"] != OVH:
                self.api("PATCH", self.zone + "/dns_records/" + current["id"], {"content": OVH})
            if self.dns()[name] != {"id": expected["id"], "content": OVH}:
                raise Stop(f"{name}: new DNS target not yet observed")

    def check_dns_ownership(self, baseline):
        current = self.dns()
        if current.keys() != baseline.keys() or any(current[name]["id"] != record["id"] or
                current[name]["content"] not in (record["content"], OVH) for name, record in baseline.items()):
            raise Stop("DNS changed after preflight")

    def deploy(self, before, after):
        current = self.active_version()
        if current == after:
            return
        if current != before:
            raise Stop("Worker deployment drifted; refusing to replace an unrelated version")
        self.api("POST", self.worker + "/deployments", {"strategy": "percentage", "versions": [{"version_id": after, "percentage": 100}]})
        if self.active_version() != after:
            raise Stop("Worker deployment readback did not match")

    def set_routes(self, desired):
        current = self.routes()
        if not desired or not desired <= KNOWN:
            raise Stop("refusing to update unrelated Worker routes")
        # Put the new match in place before removing the previous matches.
        for pattern in sorted(desired - current.keys()):
            self.api("POST", self.zone + "/workers/routes", {"pattern": pattern, "script": WORKER})
        current = self.routes()
        if not desired <= current.keys():
            raise Stop("new Worker route bindings not observed")
        for pattern in sorted(current.keys() - desired):
            self.api("DELETE", self.zone + "/workers/routes/" + current[pattern])
        if self.routes().keys() != desired:
            raise Stop("Worker route readback did not match")
