import test from "node:test";
import assert from "node:assert/strict";

import responsesDispatcher, {
  fetchRoutingNodes,
  normalizeRuntimeNodes,
  resetRoutingConfigCache,
  resolveRoutingNodes,
  selectOrigins,
  staticRoutingNodes,
} from "./responses-dispatcher.mjs";

const env = {
  BWG_US_01_ORIGIN: "https://control.example",
  BWG_US_01_PERCENT: "50",
  VMISS_US_01_ORIGIN: "https://old.example",
  VMISS_US_01_PERCENT: "10",
  YT_US_01_ORIGIN: "https://yt.example",
  YT_US_01_PERCENT: "30",
  VMISS_US_02_ORIGIN: "https://new.example",
  VMISS_US_02_PERCENT: "10",
};

function originsFor(randomValue, nodes = null) {
  return selectOrigins(
    env,
    {
      getRandomValues(target) {
        target[0] = randomValue;
        return target;
      },
    },
    nodes,
  );
}

test("selects the four static nodes with the configured percentages", () => {
  assert.deepEqual(originsFor(0), [
    "https://control.example",
    "https://old.example",
    "https://yt.example",
    "https://new.example",
  ]);
  assert.deepEqual(originsFor(50), [
    "https://old.example",
    "https://control.example",
    "https://yt.example",
    "https://new.example",
  ]);
  assert.deepEqual(originsFor(60), [
    "https://yt.example",
    "https://control.example",
    "https://old.example",
    "https://new.example",
  ]);
  assert.deepEqual(originsFor(90), [
    "https://new.example",
    "https://control.example",
    "https://old.example",
    "https://yt.example",
  ]);
});

test("uses arbitrary runtime ratios and omits zero-weight nodes", () => {
  const nodes = [
    { origin: "https://control.example", effective_weight: 5 },
    { origin: "https://old.example", effective_weight: 0 },
    { origin: "https://yt.example", effective_weight: 3 },
    { origin: "https://new.example", effective_weight: 1 },
  ];
  assert.deepEqual(originsFor(5, nodes), [
    "https://yt.example",
    "https://control.example",
    "https://new.example",
  ]);
  assert.deepEqual(originsFor(8, nodes), [
    "https://new.example",
    "https://control.example",
    "https://yt.example",
  ]);
});

test("rejects malformed runtime nodes", () => {
  assert.throws(
    () => normalizeRuntimeNodes({ data: { nodes: [] } }),
    /no nodes/,
  );
  assert.throws(
    () =>
      normalizeRuntimeNodes({
        data: {
          nodes: [{ id: "bad", origin: "http://bad.example", effective_weight: 1 }],
        },
      }),
    /invalid origin/,
  );
  assert.throws(
    () =>
      normalizeRuntimeNodes({
        data: {
          nodes: [
            {
              id: "bad",
              origin: "https://user:pass@bad.example",
              effective_weight: 1,
            },
          ],
        },
      }),
    /invalid origin/,
  );
});

test("fetches runtime weights and keeps the last good value on refresh failure", async () => {
  resetRoutingConfigCache();
  const originalFetch = globalThis.fetch;
  const originalDateNow = Date.now;
  let calls = 0;
  let now = 1_000_000;
  Date.now = () => now;
  globalThis.fetch = async (_url, options) => {
    calls += 1;
    if (calls > 1) {
      throw new Error("temporary failure");
    }
    assert.equal(
      options.headers["X-Gateway-Routing-Token"],
      "runtime-secret",
    );
    assert.equal(options.redirect, "manual");
    return Response.json({
      data: {
        nodes: [
          { id: "bwg-us-01", origin: "https://control.example", effective_weight: 5 },
          { id: "vmiss-us-02", origin: "https://new.example", effective_weight: 1 },
        ],
      },
    });
  };

  try {
    const runtimeEnv = {
      ...env,
      ROUTING_CONFIG_URL: "https://control.example/api/v1/gateway-routing/runtime",
      ROUTING_CONFIG_TOKEN: "runtime-secret",
      ROUTING_CONFIG_TTL_SECONDS: "5",
    };
    const first = await fetchRoutingNodes(runtimeEnv);
    assert.equal(first.length, 2);
    // Fresh cache avoids a network call.
    assert.deepEqual(await fetchRoutingNodes(runtimeEnv), first);
    assert.equal(calls, 1);
    // Once expired, a temporary fetch failure preserves the last good nodes.
    now += 6000;
    assert.deepEqual(await fetchRoutingNodes(runtimeEnv), first);
    assert.equal(calls, 2);
  } finally {
    globalThis.fetch = originalFetch;
    Date.now = originalDateNow;
    resetRoutingConfigCache();
  }
});

test("falls back to static nodes when no runtime endpoint is configured", async () => {
  resetRoutingConfigCache();
  assert.deepEqual(await fetchRoutingNodes(env), staticRoutingNodes(env));
});

test("cold routing cache uses static nodes while runtime config refreshes", async () => {
  resetRoutingConfigCache();
  const originalFetch = globalThis.fetch;
  let finishRefresh;
  const refreshResponse = new Promise((resolve) => {
    finishRefresh = resolve;
  });
  globalThis.fetch = async () => refreshResponse;
  const background = [];
  const metadata = {};
  const runtimeEnv = {
    ...env,
    ROUTING_CONFIG_URL: "https://control.example/api/v1/gateway-routing/runtime",
    ROUTING_CONFIG_TOKEN: "runtime-secret",
  };

  try {
    assert.deepEqual(
      resolveRoutingNodes(
        runtimeEnv,
        { waitUntil: (promise) => background.push(promise) },
        metadata,
      ),
      staticRoutingNodes(runtimeEnv),
    );
    assert.equal(metadata.source, "static_refresh");
    assert.equal(background.length, 1);

    finishRefresh(
      Response.json({
        data: {
          nodes: [
            { id: "bwg-us-01", origin: "https://control.example", effective_weight: 5 },
            { id: "vmiss-us-02", origin: "https://new.example", effective_weight: 1 },
          ],
        },
      }),
    );
    await background[0];

    const cachedMetadata = {};
    assert.equal(resolveRoutingNodes(runtimeEnv, null, cachedMetadata).length, 2);
    assert.equal(cachedMetadata.source, "cache");
  } finally {
    globalThis.fetch = originalFetch;
    resetRoutingConfigCache();
  }
});

test("expired routing cache is served immediately and refreshed once", async () => {
  resetRoutingConfigCache();
  const originalFetch = globalThis.fetch;
  const originalDateNow = Date.now;
  let now = 1_000_000;
  let calls = 0;
  let finishRefresh;
  const runtimeEnv = {
    ...env,
    ROUTING_CONFIG_URL: "https://control.example/api/v1/gateway-routing/runtime",
    ROUTING_CONFIG_TOKEN: "runtime-secret",
    ROUTING_CONFIG_TTL_SECONDS: "5",
  };
  Date.now = () => now;
  globalThis.fetch = async () => {
    calls += 1;
    if (calls === 1) {
      return Response.json({
        data: {
          nodes: [
            { id: "bwg-us-01", origin: "https://control.example", effective_weight: 5 },
            { id: "vmiss-us-02", origin: "https://new.example", effective_weight: 1 },
          ],
        },
      });
    }
    return new Promise((resolve) => {
      finishRefresh = resolve;
    });
  };

  try {
    const initial = await fetchRoutingNodes(runtimeEnv);
    now += 6000;
    const background = [];
    const metadata = {};
    assert.deepEqual(
      resolveRoutingNodes(
        runtimeEnv,
        { waitUntil: (promise) => background.push(promise) },
        metadata,
      ),
      initial,
    );
    assert.deepEqual(
      resolveRoutingNodes(
        runtimeEnv,
        { waitUntil: (promise) => background.push(promise) },
      ),
      initial,
    );
    assert.equal(metadata.source, "stale_refresh");
    assert.equal(calls, 2);
    assert.equal(background.length, 2);
    assert.equal(background[0], background[1]);

    finishRefresh(
      Response.json({
        data: {
          nodes: [
            { id: "bwg-us-01", origin: "https://control.example", effective_weight: 4 },
          ],
        },
      }),
    );
    await background[0];
  } finally {
    globalThis.fetch = originalFetch;
    Date.now = originalDateNow;
    resetRoutingConfigCache();
  }
});

test("POST forwarding overwrites and sends edge routing trace headers", async () => {
  resetRoutingConfigCache();
  const originalFetch = globalThis.fetch;
  const originalDateNow = Date.now;
  let nowCall = 0;
  let forwardedRequest;
  Date.now = () => (nowCall++ === 0 ? 1_000 : 1_023);
  globalThis.fetch = async (request) => {
    forwardedRequest = request;
    return new Response("ok");
  };

  try {
    const response = await responsesDispatcher.fetch(
      new Request("https://public.example/v1/responses", {
        method: "POST",
        headers: {
          "X-Sub2API-Edge-Routing-Ms": "99999",
          "X-Sub2API-Edge-Routing-Source": "spoofed",
        },
        body: "{}",
      }),
      {
        BWG_US_01_ORIGIN: "https://control.example",
        BWG_US_01_PERCENT: "100",
      },
      { waitUntil() {} },
    );

    assert.equal(await response.text(), "ok");
    assert.equal(forwardedRequest.url, "https://control.example/v1/responses");
    assert.equal(forwardedRequest.headers.get("X-Sub2API-Edge-Routing-Ms"), "23");
    assert.equal(
      forwardedRequest.headers.get("X-Sub2API-Edge-Routing-Source"),
      "static",
    );
  } finally {
    globalThis.fetch = originalFetch;
    Date.now = originalDateNow;
    resetRoutingConfigCache();
  }
});

test("compresses large uncompressed JSON POST bodies at the edge", async () => {
  resetRoutingConfigCache();
  const originalFetch = globalThis.fetch;
  let forwardedRequest;
  const body = JSON.stringify({ input: "x".repeat(100_000) });
  globalThis.fetch = async (request) => {
    forwardedRequest = request;
    return new Response("ok");
  };

  try {
    const response = await responsesDispatcher.fetch(
      new Request("https://public.example/v1/responses", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Content-Length": String(new TextEncoder().encode(body).byteLength),
        },
        body,
      }),
      {
        BWG_US_01_ORIGIN: "https://control.example",
        BWG_US_01_PERCENT: "100",
        EDGE_REQUEST_GZIP_MIN_BYTES: "1024",
      },
    );

    assert.equal(await response.text(), "ok");
    assert.equal(forwardedRequest.headers.get("content-encoding"), "gzip");
    assert.equal(forwardedRequest.headers.get("content-length"), null);
    const decoded = await new Response(
      forwardedRequest.body.pipeThrough(new DecompressionStream("gzip")),
    ).text();
    assert.equal(decoded, body);
  } finally {
    globalThis.fetch = originalFetch;
    resetRoutingConfigCache();
  }
});

test("does not recompress an already encoded or signed JSON body", async () => {
  resetRoutingConfigCache();
  const originalFetch = globalThis.fetch;
  const forwarded = [];
  globalThis.fetch = async (request) => {
    forwarded.push(request);
    return new Response("ok");
  };
  const body = "x".repeat(100_000);

  try {
    await responsesDispatcher.fetch(
      new Request("https://public.example/v1/responses", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Content-Encoding": "gzip",
          "Content-Length": String(body.length),
        },
        body,
      }),
      {
        BWG_US_01_ORIGIN: "https://control.example",
        BWG_US_01_PERCENT: "100",
        EDGE_REQUEST_GZIP_MIN_BYTES: "1024",
      },
    );
    await responsesDispatcher.fetch(
      new Request("https://public.example/v1/responses", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Content-Length": String(body.length),
          Digest: "sha-256=signature",
        },
        body,
      }),
      {
        BWG_US_01_ORIGIN: "https://control.example",
        BWG_US_01_PERCENT: "100",
        EDGE_REQUEST_GZIP_MIN_BYTES: "1024",
      },
    );
    assert.equal(forwarded[0].headers.get("content-encoding"), "gzip");
    assert.equal(forwarded[1].headers.get("content-encoding"), null);
    assert.equal(forwarded[1].headers.get("digest"), "sha-256=signature");
  } finally {
    globalThis.fetch = originalFetch;
    resetRoutingConfigCache();
  }
});

test("reads the legacy static variable names during migration", () => {
  const legacyEnv = {
    CONTROL_ORIGIN: "https://control.example",
    GATEWAY_ORIGIN: "https://old.example",
    GATEWAY_PERCENT: "10",
    GATEWAY154_ORIGIN: "https://yt.example",
    GATEWAY154_PERCENT: "30",
    GATEWAY2_ORIGIN: "https://new.example",
    GATEWAY2_PERCENT: "10",
  };
  assert.deepEqual(staticRoutingNodes(legacyEnv), staticRoutingNodes(env));
});
