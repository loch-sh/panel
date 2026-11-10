/** A web address of a container, from its Traefik labels. */
export interface WebRoute {
  host: string;
  /** a router of this host terminates TLS */
  tls: boolean;
  url: string;
}

const ROUTER_LABEL = /^traefik\.http\.routers\.([^.]+)\.(.+)$/;
const HOST_RULE = /Host\(`([^`]+)`\)/g;
/** A DNS host name (labels are set by any container: nothing else is linked). */
const HOSTNAME =
  /^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/i;
/** At most this many addresses per container. */
export const MAX_WEB_ROUTES = 10;

/** The URL of a host, only when it is exactly that host (no userinfo trick). */
function safeUrl(host: string, tls: boolean): string | null {
  if (!HOSTNAME.test(host)) return null;
  const url = `${tls ? "https" : "http"}://${host}`;
  try {
    const u = new URL(url);
    if (u.hostname !== host.toLowerCase() || u.username || u.password) {
      return null;
    }
  } catch {
    return null;
  }
  return url;
}

/**
 * Web addresses routed to a container by Traefik: every `Host(...)` of its
 * routers' rules. A host is served over HTTPS when one of its routers has
 * TLS (`tls=true` or a `tls.*` option such as a certificate resolver),
 * otherwise over plain HTTP. Disabled Traefik (`traefik.enable=false`): none.
 * Labels can be set by any container: only valid host names are linked, at
 * most MAX_WEB_ROUTES.
 */
export function webRoutes(
  labels: Record<string, string> | undefined | null,
): WebRoute[] {
  if (!labels || labels["traefik.enable"] === "false") return [];
  const rules = new Map<string, string>();
  const tlsRouters = new Set<string>();
  for (const [key, value] of Object.entries(labels)) {
    const m = ROUTER_LABEL.exec(key);
    if (!m) continue;
    const [, router, setting] = m;
    if (setting === "rule") rules.set(router, value);
    else if (
      (setting === "tls" && value.toLowerCase() === "true") ||
      setting.startsWith("tls.")
    ) {
      tlsRouters.add(router);
    }
  }
  const hosts = new Map<string, boolean>();
  for (const [router, rule] of rules) {
    for (const [, host] of rule.matchAll(HOST_RULE)) {
      hosts.set(host, (hosts.get(host) ?? false) || tlsRouters.has(router));
    }
  }
  const out: WebRoute[] = [];
  for (const [host, tls] of hosts) {
    const url = safeUrl(host, tls);
    if (url) out.push({ host, tls, url });
    if (out.length === MAX_WEB_ROUTES) break;
  }
  return out;
}
