const DEFAULT_VMISS_US_01_PERCENT = 20;
const DEFAULT_YT_US_01_PERCENT = 40;
const DEFAULT_VMISS_US_02_PERCENT = 10;
const DEFAULT_DMIT_US_01_PERCENT = 5;
const DEFAULT_ROUTING_CONFIG_TTL_SECONDS = 15;
const ROUTING_CONFIG_TIMEOUT_MS = 2000;
const MAX_INGRESS_ERROR_BODY_BYTES = 8 * 1024;
const SAFE_EDGE_CONNECTION_FAILURE_STATUSES = new Set([521, 522, 523, 525, 526]);
const SAFE_CAPACITY_REJECTION_TYPES = new Set([
  "node_capacity",
  "node_capacity_unavailable",
]);
const EDGE_CAPACITY_NONCE_HEADER = "X-Sub2API-Edge-Capacity-Nonce";
const NGINX_BAD_REQUEST_HTML = new RegExp(
  [
    "^\\s*<html>\\s*<head><title>400 Bad Request</title></head>",
    "\\s*<body>\\s*<center><h1>400 Bad Request</h1></center>",
    "\\s*<hr><center>nginx(?:/[0-9.]+)?</center>",
    "\\s*</body>\\s*</html>\\s*$",
  ].join(""),
  "i",
);

let routingConfigCache = null;
let routingConfigPromise = null;

function integer(env, key, fallback, min = 0, max = 100) {
  const parsed = Number.parseInt(env[key] ?? "", 10);
  if (!Number.isFinite(parsed)) {
    return fallback;
  }
  return Math.min(max, Math.max(min, parsed));
}

function environmentValue(env, key, legacyKey = "") {
  const value = String(env[key] ?? "").trim();
  if (value) return value;
  return legacyKey ? String(env[legacyKey] ?? "").trim() : "";
}

function environmentInteger(env, key, fallback, legacyKey = "") {
  const raw = environmentValue(env, key, legacyKey);
  const parsed = Number.parseInt(raw, 10);
  if (!Number.isFinite(parsed)) {
    return fallback;
  }
  return Math.min(100, Math.max(0, parsed));
}

function appendUnique(origins, origin) {
  if (origin && !origins.includes(origin)) {
    origins.push(origin);
  }
}

function staticRoutingNodes(env) {
  const gateways = [
    {
      id: "vmiss-us-01",
      origin: environmentValue(env, "VMISS_US_01_ORIGIN", "GATEWAY_ORIGIN"),
      effective_weight: environmentInteger(
        env,
        "VMISS_US_01_PERCENT",
        DEFAULT_VMISS_US_01_PERCENT,
        "GATEWAY_PERCENT",
      ),
    },
    {
      id: "yt-us-01",
      origin: environmentValue(env, "YT_US_01_ORIGIN", "GATEWAY154_ORIGIN"),
      effective_weight: environmentInteger(
        env,
        "YT_US_01_PERCENT",
        DEFAULT_YT_US_01_PERCENT,
        "GATEWAY154_PERCENT",
      ),
    },
    {
      id: "vmiss-us-02",
      origin: environmentValue(env, "VMISS_US_02_ORIGIN", "GATEWAY2_ORIGIN"),
      effective_weight: environmentInteger(
        env,
        "VMISS_US_02_PERCENT",
        DEFAULT_VMISS_US_02_PERCENT,
        "GATEWAY2_PERCENT",
      ),
    },
    {
      id: "dmit-us-01",
      origin: environmentValue(env, "DMIT_US_01_ORIGIN"),
      effective_weight: environmentInteger(
        env,
        "DMIT_US_01_PERCENT",
        DEFAULT_DMIT_US_01_PERCENT,
      ),
    },
  ].filter((node) => node.origin);
  const gatewayWeight = gateways.reduce(
    (total, node) => total + node.effective_weight,
    0,
  );
  return [
    {
      id: "bwg-us-01",
      origin: environmentValue(env, "BWG_US_01_ORIGIN", "CONTROL_ORIGIN"),
      effective_weight:
        environmentValue(env, "BWG_US_01_PERCENT") === ""
          ? Math.max(0, 100 - gatewayWeight)
          : environmentInteger(env, "BWG_US_01_PERCENT", 0),
    },
    ...gateways,
  ].filter((node) => node.origin);
}

function normalizeRuntimeNodes(payload) {
  const nodes = payload?.data?.nodes ?? payload?.nodes;
  const overflowNodeID = String(
    payload?.data?.overflow_node_id ?? payload?.overflow_node_id ?? "",
  );
  if (!Array.isArray(nodes) || nodes.length === 0) {
    throw new Error("routing runtime returned no nodes");
  }
  const seenOrigins = new Set();
  return nodes.map((node) => {
    const origin = String(node?.origin ?? "").replace(/\/$/, "");
    const parsedOrigin = new URL(origin);
    if (
      parsedOrigin.protocol !== "https:" ||
      parsedOrigin.pathname !== "/" ||
      parsedOrigin.username !== "" ||
      parsedOrigin.password !== "" ||
      parsedOrigin.search !== "" ||
      parsedOrigin.hash !== ""
    ) {
      throw new Error("routing runtime returned an invalid origin");
    }
    if (seenOrigins.has(origin)) {
      throw new Error("routing runtime returned a duplicate origin");
    }
    seenOrigins.add(origin);
    const weight = Number.parseInt(node?.effective_weight ?? "", 10);
    if (!Number.isFinite(weight) || weight < 0 || weight > 100) {
      throw new Error("routing runtime returned an invalid weight");
    }
    return {
      id: String(node?.id ?? ""),
      origin,
      effective_weight: weight,
      target_weight: Number.parseInt(node?.target_weight ?? weight, 10),
      auto_disabled: node?.auto_disabled === true,
      overflow_fallback:
        node?.overflow_fallback === true || String(node?.id ?? "") === overflowNodeID,
    };
  });
}

function fetchRoutingNodes(env, metadata = null) {
  const configURL = String(env.ROUTING_CONFIG_URL ?? "").trim();
  if (!configURL) {
    if (metadata) metadata.source = "static";
    return staticRoutingNodes(env);
  }

  const now = Date.now();
  if (
    routingConfigCache?.configURL === configURL &&
    now < routingConfigCache.expiresAt
  ) {
    if (metadata) metadata.source = "cache";
    return routingConfigCache.nodes;
  }
  if (routingConfigPromise) {
    if (metadata) metadata.source = "shared_refresh";
    return routingConfigPromise;
  }

  routingConfigPromise = (async () => {
    try {
      const response = await fetch(configURL, {
        headers: {
          Accept: "application/json",
          "X-Gateway-Routing-Token": String(
            env.ROUTING_CONFIG_TOKEN ?? "",
          ),
        },
        redirect: "manual",
        signal: AbortSignal.timeout(ROUTING_CONFIG_TIMEOUT_MS),
      });
      if (!response.ok) {
        throw new Error(`routing runtime returned ${response.status}`);
      }
      const nodes = normalizeRuntimeNodes(await response.json());
      const ttlSeconds = integer(
        env,
        "ROUTING_CONFIG_TTL_SECONDS",
        DEFAULT_ROUTING_CONFIG_TTL_SECONDS,
        5,
        300,
      );
      routingConfigCache = {
        configURL,
        nodes,
        expiresAt: Date.now() + ttlSeconds * 1000,
      };
      if (metadata) metadata.source = "refresh";
      return nodes;
    } catch (error) {
      console.error(
        "routing config refresh failed",
        error instanceof Error ? error.message : String(error),
      );
      if (routingConfigCache?.configURL === configURL) {
        if (metadata) metadata.source = "stale";
        return routingConfigCache.nodes;
      }
      if (metadata) metadata.source = "fallback";
      return staticRoutingNodes(env);
    } finally {
      routingConfigPromise = null;
    }
  })();

  return routingConfigPromise;
}

function scheduleRoutingConfigRefresh(env, ctx) {
  const refresh = fetchRoutingNodes(env);
  // waitUntil keeps the isolate alive without delaying the request that noticed
  // an expired (or cold) routing cache. The promise is already single-flight.
  if (ctx && typeof ctx.waitUntil === "function") {
    ctx.waitUntil(refresh);
  } else {
    void refresh;
  }
}

function resolveRoutingNodes(env, ctx, metadata = null) {
  const configURL = String(env.ROUTING_CONFIG_URL ?? "").trim();
  if (!configURL) {
    if (metadata) metadata.source = "static";
    return staticRoutingNodes(env);
  }

  const matchingCache = routingConfigCache?.configURL === configURL;
  if (matchingCache && Date.now() < routingConfigCache.expiresAt) {
    if (metadata) metadata.source = "cache";
    return routingConfigCache.nodes;
  }

  scheduleRoutingConfigRefresh(env, ctx);
  if (matchingCache) {
    if (metadata) metadata.source = "stale_refresh";
    return routingConfigCache.nodes;
  }

  if (metadata) metadata.source = "static_refresh";
  return staticRoutingNodes(env);
}

function randomIndex(randomSource, length) {
  return randomSource.getRandomValues(new Uint32Array(1))[0] % length;
}

function weightedNode(nodes, randomSource) {
  const totalWeight = nodes.reduce(
    (total, node) => total + node.effective_weight,
    0,
  );
  if (totalWeight <= 0) {
    return null;
  }

  const roll = randomIndex(randomSource, totalWeight);
  let cumulativeWeight = 0;
  for (const node of nodes) {
    cumulativeWeight += node.effective_weight;
    if (roll < cumulativeWeight) {
      return node;
    }
  }
  return nodes.at(-1) ?? null;
}

function selectOrigins(env, randomSource = crypto, runtimeNodes = null) {
  const allNodes = (runtimeNodes ?? staticRoutingNodes(env)).filter((node) => node.origin);
  const weightedNodes = allNodes.filter((node) => node.effective_weight > 0);
  const fallbackNode = allNodes.find(
    (node) => node.overflow_fallback && !node.auto_disabled,
  );
  const selectedNode = weightedNode(weightedNodes, randomSource) ?? fallbackNode;
  if (!selectedNode) {
    return [];
  }

  const origins = [];
  appendUnique(origins, selectedNode.origin);
  if (fallbackNode) {
    appendUnique(origins, fallbackNode.origin);
  }
  const remaining = weightedNodes.filter(
    (node) => node.origin !== selectedNode.origin && node.origin !== fallbackNode?.origin,
  );
  while (remaining.length > 0) {
    const next = weightedNode(remaining, randomSource);
    appendUnique(origins, next.origin);
    remaining.splice(remaining.indexOf(next), 1);
  }
  return origins;
}

async function fetchWithFailover(request, origins) {
  let retryRequest = request;

  for (let i = 0; i < origins.length; i += 1) {
    if (i < origins.length - 1) {
      retryRequest = request.clone();
    }

    try {
      const response = await fetch(originRequest(request, origins[i]), {
        redirect: "manual",
      });
      if (!shouldFailOver(response) || i === origins.length - 1) {
        return response;
      }
    } catch (error) {
      if (i === origins.length - 1) {
        throw error;
      }
    }

    request = retryRequest;
  }

  return fetch(originRequest(request, origins[origins.length - 1]), {
    redirect: "manual",
  });
}

function originRequest(request, originBase) {
  const incomingURL = new URL(request.url);
  const originURL = new URL(originBase);
  incomingURL.protocol = originURL.protocol;
  incomingURL.hostname = originURL.hostname;
  incomingURL.port = originURL.port;
  return new Request(incomingURL, request);
}

async function safeIngressFailureType(request, response, capacityNonce = "") {
  if (request.method !== "POST") {
    return "";
  }
  const capacityRejection = response.headers
    .get("x-sub2api-ingress-reject")
    ?.trim()
    .toLowerCase();
  if (
    response.status === 503 &&
    capacityRejection &&
    SAFE_CAPACITY_REJECTION_TYPES.has(capacityRejection) &&
    capacityNonce &&
    response.headers.get(EDGE_CAPACITY_NONCE_HEADER) === capacityNonce
  ) {
    return capacityRejection;
  }
  if (SAFE_EDGE_CONNECTION_FAILURE_STATUSES.has(response.status)) {
    return `edge_connection_${response.status}`;
  }
  const contentEncoding = request.headers.get("content-encoding")?.trim().toLowerCase() ?? "";
  if (contentEncoding && contentEncoding !== "identity") {
    return "";
  }
  if (response.status !== 400) {
    return "";
  }
  const contentLength = Number.parseInt(response.headers.get("content-length") ?? "", 10);
  if (Number.isFinite(contentLength) && contentLength > MAX_INGRESS_ERROR_BODY_BYTES) {
    return "";
  }

  let body;
  try {
    body = await response.clone().text();
  } catch {
    return "";
  }
  if (new TextEncoder().encode(body).byteLength > MAX_INGRESS_ERROR_BODY_BYTES) {
    return "";
  }

  const contentType = response.headers.get("content-type")?.toLowerCase() ?? "";
  if (contentType.startsWith("text/html") && NGINX_BAD_REQUEST_HTML.test(body)) {
    return "nginx_bad_request";
  }
  if (!contentType.startsWith("application/json") || !response.headers.has("x-request-id")) {
    return "";
  }
  try {
    const payload = JSON.parse(body);
    if (
      payload?.error?.type === "invalid_request_error" &&
      payload?.error?.message === "Failed to read request body"
    ) {
      return "sub2api_request_body_read";
    }
  } catch {
    return "";
  }
  return "";
}

function retryableIngressFailureResponse() {
  return Response.json(
    {
      error: {
        code: "server_error",
        message: "Temporary ingress failure. Please retry the request.",
        type: "server_error",
      },
    },
    {
      status: 503,
      headers: {
        "Cache-Control": "no-store",
        "Retry-After": "1",
      },
    },
  );
}

async function fetchWithSafeIngressRecovery(
  request,
  origins,
  routingWaitMs,
  routingSource,
) {
  let attemptRequest = request;

  for (let i = 0; i < origins.length; i += 1) {
    const replayRequest = i < origins.length - 1 ? attemptRequest.clone() : null;
    const forwarded = originRequest(attemptRequest, origins[i]);
    const capacityNonce = crypto.randomUUID();
    forwarded.headers.set("X-Sub2API-Edge-Routing-Ms", String(routingWaitMs));
    forwarded.headers.set("X-Sub2API-Edge-Routing-Source", routingSource);
    forwarded.headers.set(EDGE_CAPACITY_NONCE_HEADER, capacityNonce);
    const response = await fetch(forwarded, { redirect: "manual" });
    const failureType = await safeIngressFailureType(
      attemptRequest,
      response,
      capacityNonce,
    );
    if (!failureType) {
      return response;
    }

    console.warn("retrying safe Responses ingress rejection", {
      attempt: i + 1,
      failureType,
      origin: origins[i],
    });
    if (!replayRequest) {
      return retryableIngressFailureResponse();
    }
    attemptRequest = replayRequest;
  }

  return retryableIngressFailureResponse();
}

function shouldFailOver(response) {
  return response.status >= 500 && response.status <= 599;
}

function canRetry(request) {
  return request.method === "GET" || request.method === "HEAD";
}

export default {
  async fetch(request, env, ctx) {
    const routingMetadata = {};
    const routingStart = Date.now();
    const routingNodes = resolveRoutingNodes(env, ctx, routingMetadata);
    const routingWaitMs = Math.max(0, Date.now() - routingStart);
    const origins = selectOrigins(env, crypto, routingNodes);
    if (origins.length === 0) {
      return Response.json(
        { error: "No routing node currently has a positive effective weight" },
        { status: 503 },
      );
    }

    // Creating a Response is not idempotent. Once a POST is sent to an origin,
    // only replay a narrowly classified ingress rejection that proves the body
    // was not accepted. Network failures, 5xx responses, and ordinary 4xx
    // responses may have reached the model and remain terminal at this layer.
    if (!canRetry(request)) {
      return fetchWithSafeIngressRecovery(
        request,
        origins,
        routingWaitMs,
        String(routingMetadata.source ?? "unknown"),
      );
    }

    return fetchWithFailover(request, origins);
  },
};

function resetRoutingConfigCache() {
  routingConfigCache = null;
  routingConfigPromise = null;
}

export {
  fetchRoutingNodes,
  normalizeRuntimeNodes,
  resetRoutingConfigCache,
  resolveRoutingNodes,
  selectOrigins,
  staticRoutingNodes,
};
