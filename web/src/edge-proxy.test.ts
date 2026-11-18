import { describe, expect, it, vi } from "vitest";
import { proxyAPI } from "../edge/proxy";

const frontend = "https://admin-location-keep.rockyworks.in";
const env = { API_ORIGIN: "https://api-admin-location-keep.rockyworks.in" };

describe("Pages API proxy", () => {
  it("forwards query filters, cookies and activity headers to the fixed API", async () => {
    const send = vi
      .fn<typeof fetch>()
      .mockResolvedValue(Response.json({ items: [] }));
    const request = new Request(
      frontend + "/api/events?cursor=abc%2B123&device_ids=one,two",
      {
        headers: {
          Cookie: "lk_session=synthetic",
          "X-App-View": "logs",
          "X-App-Source": "interaction",
          "X-Forwarded-Host": "untrusted.example",
        },
      },
    );
    const response = await proxyAPI({ request, env }, send);
    const forwarded = send.mock.calls[0][0] as Request;
    expect(forwarded.url).toBe(
      env.API_ORIGIN + "/api/events?cursor=abc%2B123&device_ids=one,two",
    );
    expect(forwarded.headers.get("Cookie")).toBe("lk_session=synthetic");
    expect(forwarded.headers.get("X-App-View")).toBe("logs");
    expect(forwarded.headers.get("X-Forwarded-Host")).toBeNull();
    expect(forwarded.cache).toBe("no-store");
    expect(await response.json()).toEqual({ items: [] });
    expect(response.headers.get("Cache-Control")).toBe("private, no-store");
  });

  it("preserves POST bodies and foreign Origin for the backend's CSRF check", async () => {
    const send = vi
      .fn<typeof fetch>()
      .mockResolvedValue(
        Response.json({ code: "origin_rejected" }, { status: 403 }),
      );
    const request = new Request(frontend + "/api/usage/heartbeat", {
      method: "POST",
      headers: {
        Origin: "https://foreign.example",
        "Content-Type": "application/json",
      },
      body: '{"active":true}',
    });
    const response = await proxyAPI({ request, env }, send);
    const forwarded = send.mock.calls[0][0] as Request;
    expect(forwarded.method).toBe("POST");
    expect(forwarded.headers.get("Origin")).toBe("https://foreign.example");
    expect(await forwarded.text()).toBe('{"active":true}');
    expect(response.status).toBe(403);
  });

  it("passes Google redirects and multiple cookies back without following them", async () => {
    const headers = new Headers({
      Location: "https://accounts.google.com/o/oauth2/v2/auth?state=synthetic",
    });
    headers.append(
      "Set-Cookie",
      "lk_flow=synthetic; Path=/; HttpOnly; Secure; SameSite=Lax",
    );
    headers.append("Set-Cookie", "lk_session=; Path=/; Max-Age=0");
    const send = vi
      .fn<typeof fetch>()
      .mockResolvedValue(new Response(null, { status: 302, headers }));
    const response = await proxyAPI(
      { request: new Request(frontend + "/auth/login"), env },
      send,
    );
    expect((send.mock.calls[0][0] as Request).redirect).toBe("manual");
    expect(response.status).toBe(302);
    expect(response.headers.get("Location")).toContain(
      "https://accounts.google.com/",
    );
    expect(
      (
        response.headers as Headers & { getSetCookie(): string[] }
      ).getSetCookie(),
    ).toHaveLength(2);
  });

  it.each([
    undefined,
    "http://localhost:8080",
    frontend,
    "https://name:secret@example.invalid",
    "https://example.invalid/path",
    "https://example.invalid?url=another",
  ])("rejects an invalid upstream configuration: %s", async (API_ORIGIN) => {
    const send = vi.fn<typeof fetch>();
    const response = await proxyAPI(
      { request: new Request(frontend + "/api/session"), env: { API_ORIGIN } },
      send,
    );
    expect(response.status).toBe(503);
    expect(send).not.toHaveBeenCalled();
  });

  it("does not proxy static files or arbitrary destinations", async () => {
    const send = vi.fn<typeof fetch>();
    const response = await proxyAPI(
      {
        request: new Request(
          frontend + "/assets/app.js?url=https://other.example",
        ),
        env,
      },
      send,
    );
    expect(response.status).toBe(404);
    expect(send).not.toHaveBeenCalled();
  });

  it("returns a safe error for tunnel connection failures", async () => {
    const send = vi
      .fn<typeof fetch>()
      .mockRejectedValue(new Error("private upstream details"));
    const response = await proxyAPI(
      { request: new Request(frontend + "/api/session"), env },
      send,
    );
    expect(response.status).toBe(502);
    expect(await response.text()).not.toContain("private upstream details");
  });
});
