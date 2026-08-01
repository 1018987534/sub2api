const DEFAULT_GATEWAY_PERCENT = 10;
const DEFAULT_GATEWAY154_PERCENT = 0;
const DEFAULT_GATEWAY2_PERCENT = 0;
const DEFAULT_ROUTING_CONFIG_TTL_SECONDS = 15;
const ROUTING_CONFIG_TIMEOUT_MS = 2000;

let routingConfigCache = null;
let routingConfigPromise = null;

function integer(env, key, fallback, min = 0, max = 100) {
  const parsed = Number.parseInt(env[key] ?? "", 10);
  if (!Number.isFinite(parsed)) {
    return fallback;
  }
  return Math.min(max, Math.max(min, parsed));
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
      origin: env.GATEWAY_ORIGIN,
      effective_weight: integer(
        env,
        "GATEWAY_PERCENT",
        DEFAULT_GATEWAY_PERCENT,
      ),
    },
    {
      id: "yt-us-01",
      origin: env.GATEWAY154_ORIGIN,
      effective_weight: integer(
        env,
        "GATEWAY154_PERCENT",
        DEFAULT_GATEWAY154_PERCENT,
      ),
    },
    {
      id: "vmiss-us-02",
      origin: env.GATEWAY2_ORIGIN,
      effective_weight: integer(
        env,
        "GATEWAY2_PERCENT",
        DEFAULT_GATEWAY2_PERCENT,
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
      origin: env.CONTROL_ORIGIN,
      effective_weight: Math.max(0, 100 - gatewayWeight),
    },
    ...gateways,
  ].filter((node) => node.origin);
}

function normalizeRuntimeNodes(payload) {
  const nodes = payload?.data?.nodes ?? payload?.nodes;
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
    if (!Number.isFinite(weight) || weight < 0 || weight > 10000) {
      throw new Error("routing runtime returned an invalid weight");
    }
    return {
      id: String(node?.id ?? ""),
      origin,
      effective_weight: weight,
    };
  });
}

async function fetchRoutingNodes(env) {
  const configURL = String(env.ROUTING_CONFIG_URL ?? "").trim();
  if (!configURL) {
    return staticRoutingNodes(env);
  }

  const now = Date.now();
  if (
    routingConfigCache?.configURL === configURL &&
    now < routingConfigCache.expiresAt
  ) {
    return routingConfigCache.nodes;
  }
  if (routingConfigPromise) {
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
      return nodes;
    } catch (error) {
      console.error(
        "routing config refresh failed",
        error instanceof Error ? error.message : String(error),
      );
      if (routingConfigCache?.configURL === configURL) {
        return routingConfigCache.nodes;
      }
      return staticRoutingNodes(env);
    } finally {
      routingConfigPromise = null;
    }
  })();

  return routingConfigPromise;
}

function selectOrigins(env, randomSource = crypto, runtimeNodes = null) {
  const nodes = (runtimeNodes ?? staticRoutingNodes(env)).filter(
    (node) => node.origin && node.effective_weight > 0,
  );
  const totalWeight = nodes.reduce(
    (total, node) => total + node.effective_weight,
    0,
  );
  if (totalWeight <= 0) {
    return [];
  }

  const roll =
    randomSource.getRandomValues(new Uint32Array(1))[0] % totalWeight;
  let cumulativeWeight = 0;
  let selectedOrigin = null;
  for (const node of nodes) {
    cumulativeWeight += node.effective_weight;
    if (roll < cumulativeWeight) {
      selectedOrigin = node.origin;
      break;
    }
  }

  const origins = [];
  appendUnique(origins, selectedOrigin);
  for (const node of nodes) {
    appendUnique(origins, node.origin);
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

function shouldFailOver(response) {
  return response.status >= 500 && response.status <= 599;
}

function canRetry(request) {
  return request.method === "GET" || request.method === "HEAD";
}

export default {
  async fetch(request, env) {
    const routingNodes = await fetchRoutingNodes(env);
    const origins = selectOrigins(env, crypto, routingNodes);
    if (origins.length === 0) {
      return Response.json(
        { error: "No routing node currently has a positive effective weight" },
        { status: 503 },
      );
    }

    // Creating a Response is not idempotent. Once a POST is sent to an origin,
    // never replay it at the edge: the first origin may already have reached the
    // model upstream even if its connection fails before returning headers.
    if (!canRetry(request)) {
      return fetch(originRequest(request, origins[0]), {
        redirect: "manual",
      });
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
  selectOrigins,
  staticRoutingNodes,
};
