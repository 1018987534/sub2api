import assert from "node:assert/strict";
import test from "node:test";
import worker from "./whole-site-dispatcher.mjs";
import { resetRoutingConfigCache } from "./responses-dispatcher.mjs";

const runtime = "https://control-origin.xiaohondou.com/api/v1/gateway-routing/runtime";
const ordinary = "https://gateway3-origin.xiaohondou.com";
const overseas = "https://gateway-ovh-origin.xiaohondou.com";
const env = {
  ROUTING_CONFIG_URL: runtime,
  ROUTING_CONFIG_TOKEN: "test-runtime-token",
  ORIGIN_AUTH_TOKEN: "test-origin-token-which-is-long-enough",
};
const nodes = [
  { id: "vmiss-us-01", origin: "https://gateway-origin.xiaohondou.com", effective_weight: 0, auto_disabled: true },
  { id: "vmiss-us-02", origin: "https://gateway2-origin.xiaohondou.com", effective_weight: 0, origin_ready: true },
  { id: "dmit-us-01", origin: ordinary, effective_weight: 40, origin_ready: true },
  { id: "ovh-us-01", origin: overseas, effective_weight: 0, ovh_geo_effective_percent: 100, origin_ready: true },
];

function request(path, country, init) {
  return Object.assign(new Request(`https://xiaohondou.com${path}`, init), { cf: { country } });
}

function setup(t, fetchOrigin, payload = nodes) {
  resetRoutingConfigCache();
  const old = globalThis.fetch;
  t.after(() => { globalThis.fetch = old; resetRoutingConfigCache(); });
  const calls = [];
  globalThis.fetch = async (req, init) => {
    if (req === runtime) {
      assert.equal(init.headers["X-Gateway-Routing-Token"], env.ROUTING_CONFIG_TOKEN);
      return Response.json({ data: { nodes: payload } });
    }
    assert.ok(req instanceof Request);
    calls.push(req);
    return fetchOrigin(req, calls.length);
  };
  return calls;
}

test("all paths, including new paths and callbacks, use the same country rule", async (t) => {
  const calls = setup(t, () => new Response("ok"));
  const paths = ["/", "/admin/settings", "/assets/index-e3d2.js", "/api/v1/auth/login",
    "/v1/responses", "/v1/messages", "/v1/models", "/v1/images/generations",
    "/api/v1/payment/webhook/easypay?signature=fixture%2Bsig", "/brand-new/path%2Ffile?x=1&x=2"];
  for (const [country, expected] of [["CN", ordinary], ["XX", ordinary], ["US", overseas]]) {
    for (const path of paths) {
      assert.equal((await worker.fetch(request(path, country), env)).status, 200);
      assert.equal(calls.at(-1).url, expected + path);
    }
  }
  assert.equal(calls.length, 3 * paths.length);
});

test("preserves the body, authentication, cookies and redirect, overwrites spoofed metadata", async (t) => {
  const headers = new Headers({ Location: "/admin", "Set-Cookie": "auth=fixture; Secure" });
  const calls = setup(t, () => new Response(null, { status: 303, headers }));
  const body = '{"value":19}';
  const resp = await worker.fetch(request("/api/v1/admin/settings", "CN", {
    method: "PUT", body,
    headers: {
      Authorization: "Bearer fixture", Cookie: "session=fixture",
      "CF-Connecting-IP": "203.0.113.20", "X-Forwarded-For": "203.0.113.99",
      "X-Real-IP": "203.0.113.99", "X-Sub2API-Origin-Auth": "fake",
      "X-Sub2API-Client-IP": "203.0.113.99", "X-Sub2API-Node-ID": "fake",
    },
  }), env);
  // Consume in the fetch stub in production; a clone held by the test may be inspected here.
  assert.equal(calls.length, 1);
  assert.equal(calls[0].method, "PUT");
  assert.equal(await calls[0].text(), body);
  assert.equal(calls[0].headers.get("authorization"), "Bearer fixture");
  assert.equal(calls[0].headers.get("cookie"), "session=fixture");
  assert.equal(calls[0].headers.get("x-sub2api-origin-auth"), env.ORIGIN_AUTH_TOKEN);
  assert.equal(calls[0].headers.get("x-sub2api-client-ip"), "203.0.113.20");
  assert.equal(calls[0].headers.get("x-real-ip"), "203.0.113.20");
  assert.equal(calls[0].headers.get("x-forwarded-for"), null);
  assert.equal(calls[0].headers.get("x-sub2api-node-id"), null);
  assert.equal(calls[0].redirect, "manual");
  assert.equal(resp.headers.get("location"), "/admin");
  assert.equal(resp.headers.get("set-cookie"), "auth=fixture; Secure");
});

test("limited admission may retry; ambiguous POST and side-effectful GET never retry", async (t) => {
  for (const kind of ["nonce-match", "nonce-wrong", "ambiguous", "disconnect"]) {
    await t.test(kind, async (sub) => {
      const calls = setup(sub, async (req, n) => {
        if (req.body) assert.equal(await req.text(), "fixture");
        if (n > 1) return new Response("processed");
        if (kind === "disconnect") throw new Error("disconnect");
        if (kind === "ambiguous") return new Response("bad gateway", { status: 502 });
        return new Response(null, { status: 503, headers: {
          "X-Sub2API-Ingress-Reject": "node_capacity",
          "X-Sub2API-Edge-Capacity-Nonce": kind === "nonce-match" ? req.headers.get("X-Sub2API-Edge-Capacity-Nonce") : "other",
        } });
      }, nodes.map((n) => n.id === "vmiss-us-02" ? { ...n, effective_weight: 60 } : n));
      const write = request("/v1/responses", "CN", { method: "POST", body: "fixture", headers: { "Content-Length": "7" } });
      const actual = await worker.fetch(kind === "ambiguous" ? request("/api/v1/payment/webhook/easypay", "CN") : write, env);
      assert.equal(calls.length, kind === "nonce-match" ? 2 : 1);
      assert.equal(actual.status, { "nonce-match": 200, "nonce-wrong": 503, ambiguous: 502, disconnect: 503 }[kind]);
      if (calls.length > 1) assert.notEqual(calls[0].url, calls[1].url);
    });
  }
});

test("unknown-length and large uploads are streamed without retention for failover", async (t) => {
  for (const length of [null, "2097152"]) {
    await t.test(String(length), async (sub) => {
      const calls = setup(sub, (req) => new Response(null, { status: 503, headers: {
        "X-Sub2API-Ingress-Reject": "node_capacity", "X-Sub2API-Edge-Capacity-Nonce": req.headers.get("X-Sub2API-Edge-Capacity-Nonce"),
      } }), nodes.map((n) => n.id === "vmiss-us-02" ? { ...n, effective_weight: 60 } : n));
      const headers = length === null ? {} : { "Content-Length": length };
      assert.equal((await worker.fetch(request("/upload", "CN", { method: "POST", body: "fixture", headers }), env)).status, 503);
      assert.equal(calls.length, 1);
    });
  }
});

test("does not use OVH as an implicit CN fallback or send requests to unexpected origins", async (t) => {
  for (const payload of [nodes.map((node) => ({ ...node, auto_disabled: node.id !== "ovh-us-01" })),
    nodes.map((node) => ({ ...node, origin_ready: node.id === "ovh-us-01" })),
    nodes.map((node) => node.id === "dmit-us-01" ? { ...node, origin: "https://other.example" } : node)]) {
    await t.test("invalid or unavailable pool", async (sub) => {
      const calls = setup(sub, () => new Response("no"), payload);
      const result = await worker.fetch(request("/", "CN"), env);
      assert.equal(result.status, 503);
      assert.equal(calls.length, 0);
    });
  }
});

test("a node removed from the authoritative runtime does not break the remaining routes", async (t) => {
  const calls = setup(t, () => new Response("ok"), nodes.filter((node) => node.id !== "vmiss-us-01"));
  assert.equal((await worker.fetch(request("/admin/settings", "CN"), env)).status, 200);
  assert.equal(calls[0].url, ordinary + "/admin/settings");
});
