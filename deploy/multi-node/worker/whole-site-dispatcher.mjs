import {
  classifyCountry,
  resolveRoutingNodes,
  selectOrigins,
} from "./responses-dispatcher.mjs";

const SITE = "xiaohondou.com";
const RUNTIME = "https://control-origin.xiaohondou.com/api/v1/gateway-routing/runtime";
const ORIGINS = Object.freeze({
  "vmiss-us-01": "https://gateway-origin.xiaohondou.com",
  "vmiss-us-02": "https://gateway2-origin.xiaohondou.com",
  "dmit-us-01": "https://gateway3-origin.xiaohondou.com",
  "ovh-us-01": "https://gateway-ovh-origin.xiaohondou.com",
});
const AUTH = "X-Sub2API-Origin-Auth";
const NONCE = "X-Sub2API-Edge-Capacity-Nonce";
const CLIENT_IP = "X-Sub2API-Client-IP";
const MAX_REPLAY_BYTES = 1024 * 1024;

function unavailable(message) {
  return Response.json({ error: message }, {
    status: 503,
    headers: { "Cache-Control": "no-store", "Retry-After": "1" },
  });
}

function originRequest(request, url, env, nonce, timing, source) {
  const headers = new Headers(request.headers);
  const clientIP = headers.get("CF-Connecting-IP");
  for (const name of [...headers.keys()]) {
    if (name.startsWith("x-sub2api-") || name.startsWith("x-forwarded-") ||
      ["host", "forwarded", "x-real-ip", "true-client-ip", "cf-connecting-ip"].includes(name)) {
      headers.delete(name);
    }
  }
  headers.set(AUTH, env.ORIGIN_AUTH_TOKEN);
  headers.set(NONCE, nonce);
  headers.set("X-Sub2API-Edge-Routing-Ms", String(timing));
  headers.set("X-Sub2API-Edge-Routing-Source", source);
  if (clientIP) {
    headers.set(CLIENT_IP, clientIP);
    // Same-zone Cloudflare subrequests use this value for CF-Connecting-IP.
    headers.set("X-Real-IP", clientIP);
  }
  const upstream = new URL(url);
  const destination = new URL(request.url);
  destination.protocol = upstream.protocol;
  destination.host = upstream.host;
  const forwarded = new Request(destination, request);
  return new Request(forwarded, { headers, redirect: "manual" });
}

function capacityRejected(response, nonce) {
  return response.status === 503 && response.headers.get(NONCE) === nonce &&
    ["node_capacity", "node_capacity_unavailable"].includes(response.headers.get("X-Sub2API-Ingress-Reject"));
}

function canRetainBody(request) {
  if (!request.body) return true;
  const length = request.headers.get("Content-Length") ?? "";
  // Cloning an arbitrarily large or unknown-length upload can buffer its entire body.
  return /^\d{1,7}$/.test(length) && Number(length) <= MAX_REPLAY_BYTES;
}

function validNodes(nodes) {
  return nodes.length > 0 && nodes.length <= Object.keys(ORIGINS).length &&
    new Set(nodes.map((node) => node.id)).size === nodes.length &&
    nodes.every((node) => Object.hasOwn(ORIGINS, node.id) && ORIGINS[node.id] === node.origin);
}

export default {
  async fetch(request, env) {
    if (new URL(request.url).hostname !== SITE) {
      return new Response(null, { status: 421 });
    }
    if (env.ROUTING_CONFIG_URL !== RUNTIME || !env.ROUTING_CONFIG_TOKEN ||
      !/^[A-Za-z0-9_-]{32,128}$/.test(env.ORIGIN_AUTH_TOKEN ?? "")) {
      return unavailable("Routing configuration is unavailable");
    }

    const start = Date.now();
    const metadata = {};
    let nodes;
    try {
      nodes = await resolveRoutingNodes(env, metadata);
    } catch {
      return unavailable("Routing configuration is temporarily unavailable");
    }
    if (!validNodes(nodes)) return unavailable("Unexpected routing inventory");
    const timing = Math.max(0, Date.now() - start);
    const origins = selectOrigins(env, crypto, nodes.filter((node) => node.origin_ready === true), classifyCountry(request.cf));
    if (!origins.length) return unavailable("No routing origin is available");

    let pending = request;
    for (const [i, origin] of origins.entries()) {
      const next = i + 1 < origins.length && canRetainBody(pending) ? pending.clone() : null;
      const nonce = crypto.randomUUID();
      let response;
      try {
        response = await fetch(originRequest(pending, origin, env, nonce, timing, metadata.source ?? "unknown"));
      } catch {
        // A disconnected origin may already have processed even a GET (e.g. EasyPay).
        if (next?.body) void next.body.cancel().catch(() => {});
        return unavailable("Origin request failed");
      }
      if (!capacityRejected(response, nonce)) {
        if (next?.body) void next.body.cancel().catch(() => {});
        return response;
      }
      if (response.body) void response.body.cancel().catch(() => {});
      if (!next) return unavailable("No routing origin has capacity");
      pending = next;
    }
    return unavailable("No routing origin is available");
  },
};
