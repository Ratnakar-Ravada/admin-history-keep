import { proxyAPI } from "./edge/proxy";

interface Env {
  ASSETS: { fetch(request: Request): Promise<Response> };
  API_ORIGIN?: string;
}

export default {
  fetch(request: Request, env: Env): Promise<Response> {
    const { pathname } = new URL(request.url);
    if (
      pathname.startsWith("/api/") ||
      pathname.startsWith("/auth/") ||
      pathname === "/healthz"
    ) {
      return proxyAPI({ request, env });
    }
    return env.ASSETS.fetch(request);
  },
};
