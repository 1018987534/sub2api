const DEFAULT_GATEWAY_PERCENT = 10;

function gatewayPercent(env) {
  const parsed = Number.parseInt(env.GATEWAY_PERCENT ?? "", 10);
  if (!Number.isFinite(parsed)) {
    return DEFAULT_GATEWAY_PERCENT;
  }
  return Math.min(100, Math.max(0, parsed));
}

function selectOrigins(env) {
  const roll = crypto.getRandomValues(new Uint32Array(1))[0] % 100;
  const gatewayFirst = roll < gatewayPercent(env);
  return gatewayFirst
    ? [env.GATEWAY_ORIGIN, env.CONTROL_ORIGIN]
    : [env.CONTROL_ORIGIN, env.GATEWAY_ORIGIN];
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

export default {
  async fetch(request, env) {
    const [primaryOrigin, secondaryOrigin] = selectOrigins(env);
    const retryRequest = request.clone();

    try {
      const response = await fetch(originRequest(request, primaryOrigin), {
        redirect: "manual",
      });
      if (!shouldFailOver(response)) {
        return response;
      }
    } catch {
      // Retry below with the other origin.
    }

    return fetch(originRequest(retryRequest, secondaryOrigin), {
      redirect: "manual",
    });
  },
};
