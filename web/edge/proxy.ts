export interface ProxyContext {
  request: Request;
  env: { API_ORIGIN?: string };
}

const jsonError = (status: number, code: string, error: string) =>
  new Response(JSON.stringify({ code, error }), {
    status,
    headers: {
      "Content-Type": "application/json",
      "Cache-Control": "private, no-store",
    },
  });

// Only the Pages environment chooses the upstream. Browser-provided URLs,
// origins and forwarded-host headers must never select a proxy destination.
export async function proxyAPI(
  { request, env }: ProxyContext,
  send: typeof fetch = fetch,
): Promise<Response> {
  const incoming = new URL(request.url);
  if (
    !incoming.pathname.startsWith("/api/") &&
    !incoming.pathname.startsWith("/auth/") &&
    incoming.pathname !== "/healthz"
  ) {
    return jsonError(404, "not_found", "API route not found.");
  }
  let origin: URL;
  try {
    origin = new URL(env.API_ORIGIN ?? "");
    if (
      origin.protocol !== "https:" ||
      origin.username ||
      origin.password ||
      origin.port ||
      origin.pathname !== "/" ||
      origin.search ||
      origin.hash ||
      origin.origin === incoming.origin
    )
      throw new Error("Invalid API origin");
  } catch {
    return jsonError(
      503,
      "api_not_configured",
      "The API connection is not configured.",
    );
  }
  const target = new URL(origin.origin);
  target.pathname = incoming.pathname;
  target.search = incoming.search;
  const upstream = new Request(target, request);
  upstream.headers.delete("Host");
  upstream.headers.delete("X-Forwarded-Host");
  upstream.headers.delete("X-Forwarded-Proto");
  try {
    // Preserve browser Origin and cookies. In particular, never follow Google's
    // redirect here or turn a foreign Origin into an authorized same-site POST.
    const received = await send(
      new Request(upstream, { redirect: "manual", cache: "no-store" }),
    );
    const response = new Response(received.body, received);
    response.headers.set("Cache-Control", "private, no-store");
    response.headers.set("Cloudflare-CDN-Cache-Control", "no-store");
    response.headers.delete("CDN-Cache-Control");
    return response;
  } catch {
    return jsonError(
      502,
      "api_unreachable",
      "The API is unavailable. Please retry.",
    );
  }
}
