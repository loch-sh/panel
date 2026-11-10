import { describe, expect, it } from "vitest";
import { webRoutes } from "./webRouting";

const urls = (labels: Record<string, string>) =>
  webRoutes(labels).map((r) => r.url);

describe("webRoutes", () => {
  it("uses https only when a router of the host has TLS", () => {
    // A hosted site with TLS: an http router (redirect) and an https one.
    expect(
      urls({
        "traefik.enable": "true",
        "traefik.http.routers.hosted-blog-http.rule":
          "Host(`blog.example.com`) || Host(`www.blog.example.com`)",
        "traefik.http.routers.hosted-blog-https.rule":
          "Host(`blog.example.com`) || Host(`www.blog.example.com`)",
        "traefik.http.routers.hosted-blog-https.tls": "true",
      }),
    ).toEqual(["https://blog.example.com", "https://www.blog.example.com"]);
    // Without TLS: plain http.
    expect(
      urls({
        "traefik.enable": "true",
        "traefik.http.routers.hosted-shop-http.rule": "Host(`shop.test`)",
      }),
    ).toEqual(["http://shop.test"]);
    // A certificate resolver alone enables TLS.
    expect(
      urls({
        "traefik.http.routers.app.rule": "Host(`app.test`)",
        "traefik.http.routers.app.tls.certresolver": "le",
      }),
    ).toEqual(["https://app.test"]);
  });

  it("ignores containers without routing", () => {
    expect(webRoutes(null)).toEqual([]);
    expect(webRoutes({ "traefik.enable": "false" })).toEqual([]);
    expect(
      urls({
        "traefik.enable": "false",
        "traefik.http.routers.x.rule": "Host(`x.test`)",
      }),
    ).toEqual([]);
    expect(urls({ "traefik.http.routers.x.tls": "false" })).toEqual([]);
  });

  it("links only valid host names (labels are set by any container)", () => {
    const rule = [
      "panel.example.com@evil.example",
      "evil.example/phish",
      "evil.example#x",
      "evil example",
      "evil.example\\x",
      "evil.example:8443",
      "javascript:alert(1)",
      `${"a".repeat(64)}.example`,
      "ok.example",
    ]
      .map((h) => `Host(\`${h}\`)`)
      .join(" || ");
    expect(urls({ "traefik.http.routers.x.rule": rule })).toEqual([
      "http://ok.example",
    ]);
  });

  it("caps the number of addresses", () => {
    const rule = Array.from(
      { length: 25 },
      (_, i) => `Host(\`h${i}.test\`)`,
    ).join(" || ");
    expect(webRoutes({ "traefik.http.routers.x.rule": rule })).toHaveLength(10);
  });
});
