/**
 * In-memory mock of the Host Panel API, independent of the test runner: the
 * Vitest tests serve it through MSW, the Playwright tests through
 * page.route(). Each test gets a fresh instance (createMockApi), may change
 * its state or override a route (api.on), and can inspect the requests it
 * received (api.requests). A request no route handles is answered 404 and
 * recorded in api.unhandled, so that a missing mock is visible.
 */
import type { ContainerInfo, ContainerInspectInfo } from "dockerode";
import type { ApiKeyModel } from "../../src/Services/ApiKeys/Models/apikeys.model";
import type { DomainEntry } from "../../src/Services/Domains/Models/domains.model";
import type {
  SftpConfigModel,
  SftpUserModel,
} from "../../src/Services/Sftp/Models/sftp.model";
import type {
  CredentialModel,
  ServiceInstanceModel,
  ServicesConfigModel,
} from "../../src/Services/Databases/Models/databases.model";
import { compareVersions } from "../../src/Services/Databases/Models/databases.model";
import type {
  HostingConfigModel,
  HostingStackDetail,
  HostingStackSummary,
} from "../../src/Services/Hosting/Models/hosting.model";
import type {
  DockerNetworkDetailModel,
  DockerNetworkModel,
  NetworkDetailModel,
  NetworksConfigModel,
  NetworkMemberModel,
  NetworkSummaryModel,
} from "../../src/Services/Networks/Models/networks.model";
import {
  formatIPv4,
  parseIPv4Prefix,
} from "../../src/Services/Networks/subnet";
import type { MyUserModel } from "../../src/Services/Users/Models/users.model";
import type {
  CreateFunctionPayload,
  DeploymentModel,
  FunctionCredentialsModel,
  FunctionDetailModel,
  FunctionsConfigModel,
  FunctionSummaryModel,
  InvokeResultModel,
  UpdateFunctionPayload,
} from "../../src/Services/Functions/Models/functions.model";
import type {
  CronJobCreatePayload,
  CronJobModel,
} from "../../src/Services/Cron/Models/cron.model";
import {
  adminUser,
  containerInspect,
  dockerNetworks,
  FUNCTION_ACCESS_KEY,
  FUNCTION_SECRET_KEY,
  FUNCTION_TOKEN,
  functions,
  functionsConfig,
  hostingConfig,
  networks,
  networksConfig,
  S3_ACCESS_KEY,
  S3_SECRET,
  serviceInstances,
  servicesConfig,
} from "./fixtures";

export interface MockRequest {
  method: string;
  /** path without the API base URL, e.g. "/services/instances" */
  path: string;
  query: URLSearchParams;
  body: unknown;
}

export interface MockResponse {
  status: number;
  body?: unknown;
  /** raw body (e.g. HTML); body is sent as JSON otherwise */
  text?: string;
  contentType?: string;
}

type Params = Record<string, string>;
type Handler = (req: MockRequest, params: Params) => MockResponse;

interface Route {
  method: string;
  pattern: RegExp;
  keys: string[];
  handler: Handler;
}

export interface MockState {
  /** null: not logged in (401) */
  me: MyUserModel | null;
  servicesConfig: ServicesConfigModel;
  instances: ServiceInstanceModel[];
  hostingConfig: HostingConfigModel;
  stacks: HostingStackSummary[];
  /** registered networks with their members */
  networks: NetworkDetailModel[];
  /** GET /networks/config (count is derived from `networks`) */
  networksConfig: Omit<NetworksConfigModel, "count">;
  /** Docker networks the panel did not create, with their containers */
  dockerNetworks: DockerNetworkDetailModel[];
  /** GET /functions/config (count and used resources are derived) */
  functionsConfig: FunctionsConfigModel;
  /** functions with their detail (the list is derived) */
  functions: FunctionDetailModel[];
  /** scheduled tasks */
  cronJobs: CronJobModel[];
  /** GET /docker: the containers the user can see */
  containers: ContainerInfo[];
  /** GET /docker/:id answers, by container id (default: containerInspect). */
  inspects: Record<string, ContainerInspectInfo>;
  /** GET /domains */
  domains: DomainEntry[];
  /** GET /sftp/config (default: module disabled) */
  sftpConfig: SftpConfigModel;
  /** GET /sftp/users */
  sftpUsers: SftpUserModel[];
  /** GET /users/me/api-keys: the user's own keys */
  apiKeys: ApiKeyModel[];
  /** answers POST /functions/:name/invoke (default: echoes the event) */
  invoke: (name: string, event: unknown) => InvokeResultModel;
}

const json = (status: number, body?: unknown): MockResponse => ({
  status,
  body,
});
const apiError = (status: number, code: string, error: string) =>
  json(status, { code, error });

/** "/services/instances/:instance" → regexp with named segments. */
function compile(method: string, path: string, handler: Handler): Route {
  const keys: string[] = [];
  const source = path
    .split("/")
    .map((segment) => {
      if (!segment.startsWith(":")) {
        return segment.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      }
      keys.push(segment.slice(1));
      return "([^/]+)";
    })
    .join("/");
  return { method, pattern: new RegExp(`^${source}$`), keys, handler };
}

export class MockApi {
  state: MockState;
  readonly requests: MockRequest[] = [];
  readonly unhandled: string[] = [];
  private readonly overrides: Route[] = [];
  private readonly routes: Route[];

  constructor(state?: Partial<MockState>) {
    this.state = {
      me: adminUser,
      servicesConfig: servicesConfig(),
      instances: serviceInstances(),
      hostingConfig: hostingConfig(),
      stacks: [],
      networks: networks(),
      networksConfig: networksConfig(),
      dockerNetworks: dockerNetworks(),
      functionsConfig: functionsConfig(),
      functions: functions(),
      cronJobs: [],
      containers: [],
      inspects: {},
      domains: [],
      sftpConfig: { enabled: false },
      sftpUsers: [],
      apiKeys: [],
      invoke: (_name, event) => ({
        statusCode: 200,
        requestId: "req-0001",
        durationMs: 12,
        payload: JSON.stringify({ ok: true, event }),
        logTail: "START RequestId: req-0001\nEND RequestId: req-0001\n",
      }),
      ...state,
    };
    this.routes = this.defaultRoutes();
  }

  /**
   * Registers the functions on their networks (their own fn-<name> one
   * created): off by default so that the networks fixtures stay as they are.
   */
  withFunctionNetworks(): this {
    for (const fn of this.state.functions) {
      for (const n of fn.networks) {
        let net = this.network(n);
        if (!net && n === `fn-${fn.name}`) net = this.addNetwork(n);
        if (net) net.members.push(this.memberOf(net, "function", fn.name));
      }
    }
    return this;
  }

  /** Overrides a route for the current test (takes precedence). */
  on(method: string, path: string, handler: Handler | MockResponse): this {
    const h = typeof handler === "function" ? handler : () => handler;
    this.overrides.unshift(compile(method.toUpperCase(), path, h));
    return this;
  }

  /** Requests received for a method and path (exact path). */
  calls(method: string, path: string): MockRequest[] {
    return this.requests.filter(
      (r) => r.method === method.toUpperCase() && r.path === path,
    );
  }

  handle(req: MockRequest): MockResponse {
    this.requests.push(req);
    for (const route of [...this.overrides, ...this.routes]) {
      if (route.method !== req.method) continue;
      const m = route.pattern.exec(req.path);
      if (!m) continue;
      const params: Params = {};
      route.keys.forEach((k, i) => (params[k] = decodeURIComponent(m[i + 1])));
      return route.handler(req, params);
    }
    this.unhandled.push(`${req.method} ${req.path}`);
    return apiError(404, "NOT_FOUND", "Not mocked");
  }

  private admin(): MockResponse | null {
    if (!this.state.me) return apiError(401, "UNAUTHORIZED", "Unauthorized");
    if (this.state.me.profile !== "admin") {
      return apiError(403, "FORBIDDEN", "Forbidden");
    }
    return null;
  }

  private defaultRoutes(): Route[] {
    const r = (method: string, path: string, handler: Handler) =>
      compile(method, path, handler);
    return [
      // The OIDC login page the 401 interceptor redirects to.
      r("GET", "/openid-connect/login", () => ({
        status: 200,
        text: "<html><body><h1>Mock identity provider</h1></body></html>",
        contentType: "text/html",
      })),
      r("GET", "/users/me", () =>
        this.state.me
          ? json(200, this.state.me)
          : apiError(401, "UNAUTHORIZED", "Unauthorized"),
      ),
      r("GET", "/users/me/api-keys", () =>
        this.state.me
          ? json(200, this.state.apiKeys)
          : apiError(401, "UNAUTHORIZED", "Unauthorized"),
      ),
      r("GET", "/docker", () => json(200, this.state.containers)),
      r("GET", "/docker/:id", (_req, p) => {
        const c = this.state.containers.find((c) => c.Id === p.id);
        return c
          ? json(200, this.state.inspects[c.Id] ?? containerInspect(c))
          : apiError(404, "CONTAINER_NOT_FOUND", "container not found");
      }),
      r("GET", "/domains", () => {
        const denied = this.admin();
        return denied ?? json(200, this.state.domains);
      }),
      r("GET", "/sftp/config", () => {
        const denied = this.admin();
        return denied ?? json(200, this.state.sftpConfig);
      }),
      r("GET", "/sftp/users", () => {
        const denied = this.admin();
        return denied ?? json(200, this.state.sftpUsers);
      }),

      // Hosting
      r("GET", "/hosting/config", () => json(200, this.state.hostingConfig)),
      r("GET", "/hosting/sites", () => json(200, [])),
      r("GET", "/hosting/stacks", () => json(200, this.state.stacks)),
      r("GET", "/hosting/php-modules", () => json(200, { modules: [] })),
      r("GET", "/hosting/stacks/check-name", (req) =>
        json(200, {
          available: !this.state.stacks.some(
            (s) => s.name === req.query.get("name"),
          ),
        }),
      ),
      r("GET", "/hosting/stacks/:name", (_req, p) => {
        const denied = this.admin();
        if (denied) return denied;
        const stack = this.state.stacks.find((s) => s.name === p.name);
        return stack
          ? json(200, this.stackDetail(stack))
          : apiError(404, "HOSTING_STACK_NOT_FOUND", "");
      }),
      r("POST", "/hosting/stacks", (req) => {
        const denied = this.admin();
        if (denied) return denied;
        const name = (req.body as { name: string }).name;
        return json(202, {
          name,
          operation: {
            id: "op-1",
            kind: "create",
            state: "running",
            startedAt: new Date().toISOString(),
          },
        });
      }),

      // Private networks (literal paths before "/networks/:name")
      r("GET", "/networks/config", () => {
        const denied = this.admin();
        return (
          denied ??
          json(200, {
            ...this.state.networksConfig,
            count: this.state.networks.filter((n) => !n.system).length,
          })
        );
      }),
      r("GET", "/networks/docker", () => {
        const denied = this.admin();
        return denied ?? json(200, this.state.dockerNetworks.map(externalOf));
      }),
      r("GET", "/networks/docker/:id", (_req, p) => {
        const denied = this.admin();
        if (denied) return denied;
        const net = this.state.dockerNetworks.find((n) => n.id === p.id);
        return net ? json(200, net) : apiError(404, "NETWORK_NOT_FOUND", "");
      }),
      r("GET", "/networks", () => {
        const denied = this.admin();
        return denied ?? json(200, this.state.networks.map(summaryOf));
      }),
      r("GET", "/networks/:name", (_req, p) => {
        const denied = this.admin();
        if (denied) return denied;
        const net = this.network(p.name);
        return net ? json(200, net) : apiError(404, "NETWORK_NOT_FOUND", "");
      }),
      r("POST", "/networks", (req) => this.createNetwork(req)),
      r("PATCH", "/networks/:name", (req, p) => {
        const net = this.network(p.name);
        if (!net) return apiError(404, "NETWORK_NOT_FOUND", "");
        net.description =
          (req.body as { description: string }).description || undefined;
        return json(200, networkOf(net));
      }),
      r("DELETE", "/networks/:name", (_req, p) => {
        const net = this.network(p.name);
        if (!net) return apiError(404, "NETWORK_NOT_FOUND", "");
        if (net.system) return apiError(409, "NETWORK_SYSTEM", "");
        if (net.members.length > 0) return apiError(409, "NETWORK_IN_USE", "");
        this.state.networks = this.state.networks.filter((n) => n !== net);
        return { status: 204 };
      }),
      r("POST", "/networks/:name/members", (req, p) =>
        this.attachMember(p.name, req.body as { kind: string; name: string }),
      ),
      r("DELETE", "/networks/:name/members/:kind/:member", (_req, p) =>
        this.detachMember(p.name, p.kind, p.member),
      ),
      r("POST", "/networks/:name/disconnect", (req, p) => {
        const net = this.network(p.name);
        if (!net) return apiError(404, "NETWORK_NOT_FOUND", "");
        const id = (req.body as { containerId: string }).containerId;
        const m = net.members.find((x) => x.containerId === id);
        if (!m) return apiError(404, "NETWORK_MEMBER_NOT_FOUND", "");
        if (m.kind !== "unmanaged") {
          return apiError(409, "NETWORK_MEMBER_MANAGED", "");
        }
        net.members = net.members.filter((x) => x !== m);
        return { status: 204 };
      }),

      // Functions (literal paths before "/functions/:name")
      ...this.functionRoutes(),

      // Scheduled tasks
      r("GET", "/cron", () => json(200, this.visibleJobs())),
      r("POST", "/cron", (req) =>
        this.saveJob(req.body as CronJobCreatePayload),
      ),
      r("PUT", "/cron/:id", (req, p) =>
        this.saveJob(req.body as CronJobCreatePayload, p.id),
      ),
      r("PATCH", "/cron/:id", (req, p) => {
        const job = this.state.cronJobs.find((j) => j.id === p.id);
        if (!job) return apiError(404, "CRON_JOB_NOT_FOUND", "");
        Object.assign(job, req.body as object);
        return json(200, job);
      }),
      r("DELETE", "/cron/:id", (_req, p) => {
        this.state.cronJobs = this.state.cronJobs.filter((j) => j.id !== p.id);
        return { status: 204 };
      }),
      r("POST", "/cron/:id/run", () => json(202, {})),

      // Managed services
      r("GET", "/services/config", () => {
        const denied = this.admin();
        return denied ?? json(200, this.state.servicesConfig);
      }),
      r("GET", "/services/instances", () => {
        const denied = this.admin();
        return denied ?? json(200, this.state.instances);
      }),
      r("GET", "/services/instances/:instance/resources", (_req, p) => {
        const inst = this.state.instances.find((i) => i.key === p.instance);
        if (!inst) return apiError(404, "SERVICE_INSTANCE_NOT_FOUND", "");
        return json(
          200,
          inst.engine === "postgres"
            ? [
                {
                  instance: inst.key,
                  engine: inst.engine,
                  version: inst.version,
                  name: "shop",
                  managed: true,
                  host: inst.project,
                  port: 5432,
                },
              ]
            : [],
        );
      }),
      r("POST", "/services/resources", (req) => this.createResource(req)),
      r("DELETE", "/services/instances/:instance", (req, p) => {
        const inst = this.state.instances.find((i) => i.key === p.instance);
        if (!inst) return apiError(404, "SERVICE_INSTANCE_NOT_FOUND", "");
        const purge = req.query.get("purge") === "true";
        if (purge && !inst.modified) {
          return apiError(409, "SERVICE_INSTANCE_NOT_MODIFIED", "");
        }
        if (!purge && (inst.modified || inst.resources !== 0)) {
          return apiError(409, "SERVICE_INSTANCE_NOT_EMPTY", "");
        }
        this.state.instances = this.state.instances.filter((i) => i !== inst);
        return { status: 204 };
      }),
      r("PATCH", "/services/instances/:instance", (req, p) => {
        const inst = this.state.instances.find((i) => i.key === p.instance);
        if (!inst?.options)
          return apiError(404, "SERVICE_INSTANCE_NOT_FOUND", "");
        const changes = (req.body as { options: Record<string, string> })
          .options;
        inst.options = { ...inst.options, ...changes };
        return json(200, { options: inst.options });
      }),
      r("PUT", "/services/instances/:instance/networks", (req, p) => {
        const inst = this.state.instances.find((i) => i.key === p.instance);
        if (!inst) return apiError(404, "SERVICE_INSTANCE_NOT_FOUND", "");
        const wanted = (req.body as { networks: string[] }).networks;
        if (wanted.length === 0) {
          return apiError(400, "SERVICE_LAST_NETWORK", "");
        }
        for (const n of this.state.networks) {
          const on = wanted.includes(n.name);
          const has = n.members.some(
            (m) => m.kind === "instance" && m.name === inst.key,
          );
          if (on && !has)
            n.members.push(this.memberOf(n, "instance", inst.key));
          if (!on && has) {
            n.members = n.members.filter(
              (m) => !(m.kind === "instance" && m.name === inst.key),
            );
          }
        }
        inst.networks = [...wanted].sort();
        return json(200, { networks: inst.networks });
      }),
      r("PUT", "/services/instances/:instance/exposure", (req, p) => {
        const inst = this.state.instances.find((i) => i.key === p.instance);
        if (!inst) return apiError(404, "SERVICE_INSTANCE_NOT_FOUND", "");
        const body = req.body as {
          address: string;
          port: number;
          tlsOptional?: boolean;
        };
        const taken = this.state.instances.some(
          (i) => i !== inst && i.exposure?.port === body.port,
        );
        if (taken) return apiError(409, "SERVICE_PORT_IN_USE", "");
        const loopback = body.address.startsWith("127.");
        inst.exposure = {
          address: body.address,
          port: body.port,
          tls: !loopback,
          tlsOptional: body.tlsOptional || undefined,
          fingerprint: loopback ? undefined : "AB:CD:EF:01",
        };
        return json(200, inst);
      }),
      r("DELETE", "/services/instances/:instance/exposure", (_req, p) => {
        const inst = this.state.instances.find((i) => i.key === p.instance);
        if (!inst) return apiError(404, "SERVICE_INSTANCE_NOT_FOUND", "");
        delete inst.exposure;
        return json(200, inst);
      }),
      r("POST", "/services/instances/:instance/update", (req, p) =>
        this.upgradeInstance(p.instance, req.body as { version?: string }),
      ),
      r("POST", "/services/updates/check", () => {
        const denied = this.admin();
        if (denied) return denied;
        const catalog = { checkedAt: new Date().toISOString() };
        this.state.servicesConfig.catalog = catalog;
        return json(200, catalog);
      }),
      r(
        "POST",
        "/services/instances/:instance/resources/:name/password",
        (_req, p) =>
          json(200, {
            credentials: this.credentials(p.instance, p.name),
          }),
      ),
      r(
        "DELETE",
        "/services/instances/:instance/resources/:name",
        (_req, p) => {
          const inst = this.state.instances.find((i) => i.key === p.instance);
          const dedicated = inst?.options !== undefined;
          if (dedicated) {
            this.state.instances = this.state.instances.filter(
              (i) => i.key !== p.instance,
            );
          }
          return json(200, { instanceRetired: dedicated });
        },
      ),
    ];
  }

  private functionRoutes(): Route[] {
    const r = (method: string, path: string, handler: Handler) =>
      compile(method, path, (req, params) => {
        const denied = this.admin();
        if (denied) return denied;
        if (
          path !== "/functions/config" &&
          !this.state.functionsConfig.enabled
        ) {
          return apiError(404, "FUNCTIONS_DISABLED", "");
        }
        return handler(req, params);
      });
    const withFn =
      (
        handler: (
          fn: FunctionDetailModel,
          req: MockRequest,
          p: Params,
        ) => MockResponse,
      ): Handler =>
      (req, p) => {
        const fn = this.fn(p.name);
        return fn
          ? handler(fn, req, p)
          : apiError(404, "FUNCTION_NOT_FOUND", "");
      };
    return [
      r("GET", "/functions/config", () => {
        const c = this.state.functionsConfig;
        if (!c.config) return json(200, c);
        const fns = this.state.functions;
        return json(200, {
          ...c,
          config: {
            ...c.config,
            count: fns.length,
            usedMemoryMB: fns.reduce((a, f) => a + f.memoryMB, 0),
            usedCpus: fns.reduce((a, f) => a + f.cpus, 0),
          },
        });
      }),
      r("GET", "/functions", () =>
        json(200, this.state.functions.map(functionSummaryOf)),
      ),
      r("POST", "/functions", (req) =>
        this.createFunction(req.body as CreateFunctionPayload),
      ),
      r(
        "GET",
        "/functions/:name",
        withFn((fn) => json(200, fn)),
      ),
      r(
        "PATCH",
        "/functions/:name",
        withFn((fn, req) => {
          if (fn.state === "modified")
            return apiError(409, "FUNCTION_MODIFIED", "");
          const body = req.body as UpdateFunctionPayload;
          Object.assign(fn, body);
          // As the API: "" resets a custom runtime's handler.
          if (fn.runtime === "provided" && body.handler === "") {
            fn.handler = "bootstrap";
          }
          return json(200, fn);
        }),
      ),
      r(
        "DELETE",
        "/functions/:name",
        withFn((fn, req) => {
          const purge = req.query.get("purge") === "true";
          const modified = fn.state === "modified" || fn.state === "invalid";
          if (purge && !modified)
            return apiError(409, "FUNCTION_NOT_MODIFIED", "");
          if (!purge && modified) return apiError(409, "FUNCTION_MODIFIED", "");
          this.state.functions = this.state.functions.filter((f) => f !== fn);
          for (const n of this.state.networks) {
            n.members = n.members.filter(
              (m) => !(m.kind === "function" && m.name === fn.name),
            );
          }
          this.state.networks = this.state.networks.filter(
            (n) => n.name !== `fn-${fn.name}`,
          );
          for (const j of this.state.cronJobs) {
            if (j.functionInvoke?.function === fn.name) {
              j.enabled = false;
              j.disabledReason = "FUNCTION_DELETED";
            }
          }
          return json(200, { containers: 1 });
        }),
      ),
      r(
        "POST",
        "/functions/:name/code",
        withFn((fn) => {
          const d: DeploymentModel = {
            id: `20261106T090000Z-${(fn.deployments.length + 1).toString(16).padStart(8, "0")}`,
            treeHash: "e5".repeat(32),
            zipSha256: "f6".repeat(32),
            zipSize: 2048,
            files: 3,
            bytes: 6144,
            active: true,
          };
          fn.deployments = [
            d,
            ...fn.deployments.map((x) => ({ ...x, active: false })),
          ];
          fn.deployment = d.id;
          if (fn.state === "not_deployed") fn.state = "running";
          return json(201, d);
        }),
      ),
      r(
        "GET",
        "/functions/:name/deployments",
        withFn((fn) => json(200, fn.deployments)),
      ),
      r(
        "POST",
        "/functions/:name/deployments/:id/activate",
        withFn((fn, _req, p) => {
          const d = fn.deployments.find((x) => x.id === p.id);
          if (!d) return apiError(404, "FUNCTION_DEPLOYMENT_NOT_FOUND", "");
          if (d.active) return apiError(409, "FUNCTION_DEPLOYMENT_ACTIVE", "");
          fn.deployments = fn.deployments.map((x) => ({
            ...x,
            active: x.id === d.id,
          }));
          fn.deployment = d.id;
          return { status: 204 };
        }),
      ),
      r(
        "PUT",
        "/functions/:name/secrets/:key",
        withFn((fn, req, p) => {
          if (typeof (req.body as { value?: unknown })?.value !== "string") {
            return apiError(400, "INVALID_REQUEST_BODY", "");
          }
          // The value is never kept by the mock either.
          if (!fn.secrets.includes(p.key))
            fn.secrets = [...fn.secrets, p.key].sort();
          return { status: 204 };
        }),
      ),
      r(
        "DELETE",
        "/functions/:name/secrets/:key",
        withFn((fn, _req, p) => {
          if (!fn.secrets.includes(p.key))
            return apiError(404, "FUNCTION_SECRET_NOT_FOUND", "");
          fn.secrets = fn.secrets.filter((s) => s !== p.key);
          return { status: 204 };
        }),
      ),
      r(
        "POST",
        "/functions/:name/credentials/rotate",
        withFn((fn, req) => {
          const sigv4 = (req.body as { sigv4?: boolean } | undefined)?.sigv4;
          if (sigv4 !== undefined) fn.sigv4 = sigv4;
          return json(200, this.functionCredentials(fn.name, fn.sigv4));
        }),
      ),
      r(
        "POST",
        "/functions/:name/invoke",
        withFn((fn, req) => {
          if (req.body === undefined || req.body === "") {
            return apiError(400, "INVALID_FUNCTION_EVENT", "");
          }
          if (fn.state !== "running")
            return apiError(409, "FUNCTION_NOT_RUNNING", "");
          return json(200, this.state.invoke(fn.name, req.body));
        }),
      ),
      r(
        "PUT",
        "/functions/:name/networks",
        withFn((fn, req) => {
          const wanted = (req.body as { networks: string[] }).networks;
          if (wanted.length === 0 || wanted.includes("services")) {
            return apiError(400, "INVALID_FUNCTION_NETWORK", "");
          }
          for (const n of this.state.networks) {
            const on = wanted.includes(n.name);
            const has = n.members.some(
              (m) => m.kind === "function" && m.name === fn.name,
            );
            if (on && !has)
              n.members.push(this.memberOf(n, "function", fn.name));
            if (!on && has) {
              n.members = n.members.filter(
                (m) => !(m.kind === "function" && m.name === fn.name),
              );
            }
          }
          fn.networks = [...wanted].sort();
          return json(200, { networks: fn.networks });
        }),
      ),
      r(
        "PUT",
        "/functions/:name/egress",
        withFn((fn, req) => {
          fn.egress = (req.body as { enabled: boolean }).enabled;
          return json(200, { egress: fn.egress });
        }),
      ),
      r(
        "POST",
        "/functions/:name/:action",
        withFn((fn, _req, p) => {
          if (!["start", "stop", "restart"].includes(p.action)) {
            return apiError(400, "INVALID_FUNCTION_ACTION", "");
          }
          if (!fn.deployment) return apiError(409, "FUNCTION_NOT_DEPLOYED", "");
          fn.state = p.action === "stop" ? "stopped" : "running";
          return { status: 204 };
        }),
      ),
    ];
  }

  private fn(name: string): FunctionDetailModel | undefined {
    return this.state.functions.find((f) => f.name === name);
  }

  private functionCredentials(
    name: string,
    sigv4: boolean,
  ): FunctionCredentialsModel {
    return {
      token: FUNCTION_TOKEN,
      ...(sigv4
        ? {
            accessKeyId: FUNCTION_ACCESS_KEY,
            secretAccessKey: FUNCTION_SECRET_KEY,
          }
        : {}),
      endpoint: `http://fn-${name}:8080/2015-03-31/functions/${name}/invocations`,
      region: "us-east-1",
    };
  }

  private createFunction(body: CreateFunctionPayload): MockResponse {
    if (!/^[a-z]([a-z0-9-]{0,19}[a-z0-9])?$/.test(body.name)) {
      return apiError(400, "INVALID_FUNCTION_NAME", "");
    }
    if (this.fn(body.name)) return apiError(409, "FUNCTION_EXISTS", "");
    if (body.networks?.includes("services")) {
      return apiError(400, "INVALID_FUNCTION_NETWORK", "");
    }
    const custom = body.runtime === "provided";
    if (!body.handler && !custom) {
      return apiError(400, "INVALID_FUNCTION_HANDLER", "");
    }
    const defaults = this.state.functionsConfig.config!.defaults;
    const name = body.name;
    const own = `fn-${name}`;
    const fn: FunctionDetailModel = {
      name,
      runtime: body.runtime,
      runtimeVersion: body.runtimeVersion,
      handler: body.handler || "bootstrap",
      memoryMB: body.memoryMB ?? defaults.memoryMB,
      cpus: body.cpus ?? defaults.cpus,
      timeoutSeconds: body.timeoutSeconds ?? defaults.timeoutSeconds,
      tmpSizeMB: body.tmpSizeMB ?? defaults.tmpSizeMB,
      state: "not_deployed",
      networks: [own, ...(body.networks ?? [])].sort(),
      egress: !!body.egress,
      mcpInvocable: false,
      createdAt: new Date().toISOString(),
      arch: "arm64",
      image: `mirror.gcr.io/amazon/aws-lambda-${body.runtime}:${body.runtimeVersion}-arm64@sha256:${"0".repeat(64)}`,
      env: body.env ?? [],
      secrets: [],
      deployments: [],
      endpoint: `http://${own}:8080/2015-03-31/functions/${name}/invocations`,
      sigv4: !!body.sigv4,
    };
    this.state.functions.push(fn);
    const net = this.addNetwork(own);
    for (const n of fn.networks) {
      const target = n === own ? net : this.network(n);
      target?.members.push(this.memberOf(target, "function", name));
    }
    return json(201, {
      function: fn,
      credentials: this.functionCredentials(name, fn.sigv4),
    });
  }

  /** A function's own network, as the API registers it. */
  private addNetwork(name: string): NetworkDetailModel {
    const index = this.state.networks.length;
    const subnet = `10.${90 + index}.0.0/24`;
    const net: NetworkDetailModel = {
      name,
      dockerName: `loch-net-${name}`,
      id: name.padEnd(64, "e"),
      subnet,
      gateway: `10.${90 + index}.0.1`,
      description: `Network of the function ${name.slice(3)}`,
      createdAt: new Date().toISOString(),
      state: "ok",
      internal: true,
      members: [],
    };
    this.state.networks.push(net);
    return net;
  }

  /** Restricted profiles do not see function invocations. */
  private visibleJobs(): CronJobModel[] {
    return this.state.me?.profile === "admin"
      ? this.state.cronJobs
      : this.state.cronJobs.filter((j) => j.type !== "function_invoke");
  }

  private saveJob(body: CronJobCreatePayload, id?: string): MockResponse {
    if (body.type === "function_invoke") {
      const target = body.functionInvoke?.function ?? "";
      if (!this.state.functionsConfig.enabled) {
        return apiError(404, "FUNCTIONS_DISABLED", "");
      }
      if (!this.fn(target)) {
        return json(404, {
          code: "FUNCTION_NOT_FOUND",
          error: "",
          field: "functionInvoke.function",
        });
      }
      const others = this.state.cronJobs.filter(
        (j) => j.id !== id && j.functionInvoke?.function === target,
      );
      if (others.length >= 5)
        return apiError(409, "FUNCTION_CRON_LIMIT_REACHED", "");
      body = {
        ...body,
        functionInvoke: {
          function: target,
          event: body.functionInvoke?.event?.trim()
            ? body.functionInvoke.event
            : "{}",
        },
      };
    }
    const now = new Date().toISOString();
    const existing = id
      ? this.state.cronJobs.find((j) => j.id === id)
      : undefined;
    if (id && !existing) return apiError(404, "CRON_JOB_NOT_FOUND", "");
    const job: CronJobModel = {
      ...(existing ?? {
        id: `job-${this.state.cronJobs.length + 1}`,
        lastStatus: "pending" as CronJobModel["lastStatus"],
        createdAt: now,
      }),
      ...body,
      updatedAt: now,
    } as CronJobModel;
    if (existing) Object.assign(existing, job);
    else this.state.cronJobs.push(job);
    return json(existing ? 200 : 201, job);
  }

  private network(name: string): NetworkDetailModel | undefined {
    return this.state.networks.find((n) => n.name === name);
  }

  /** A member as the API reports it once connected (next free address). */
  private memberOf(
    net: NetworkDetailModel,
    kind: "site" | "instance" | "function",
    name: string,
  ): NetworkMemberModel {
    const prefix = parseIPv4Prefix(net.subnet);
    const index = net.members.length + 2;
    const container =
      kind === "site"
        ? `hosted-${name}-web-1`
        : kind === "function"
          ? `fn-${name}`
          : (this.state.instances.find((i) => i.key === name)?.project ??
            `svc-${name}`);
    return {
      kind,
      name,
      container,
      containerId: `${index}`.padStart(12, "c"),
      state: "running",
      ipv4: prefix ? formatIPv4(prefix.address + index) : undefined,
      mac: `02:42:00:00:00:${index.toString(16).padStart(2, "0")}`,
      expected: true,
      present: true,
    };
  }

  private createNetwork(req: MockRequest): MockResponse {
    const denied = this.admin();
    if (denied) return denied;
    const body = req.body as {
      name: string;
      description?: string;
      subnet?: string;
    };
    if (!/^[a-z]([a-z0-9-]{0,22}[a-z0-9])?$/.test(body.name)) {
      return apiError(400, "INVALID_NETWORK_NAME", "");
    }
    if (this.network(body.name)) {
      return apiError(409, "NETWORK_NAME_TAKEN", "");
    }
    const subnet =
      body.subnet || `10.${50 + this.state.networks.length}.0.0/24`;
    const prefix = parseIPv4Prefix(subnet);
    const net: NetworkDetailModel = {
      name: body.name,
      dockerName: `loch-net-${body.name}`,
      id: `${body.name}`.padEnd(64, "f"),
      subnet,
      gateway: prefix ? formatIPv4(prefix.address + 1) : undefined,
      description: body.description,
      createdAt: new Date().toISOString(),
      createdBy: this.state.me?.email,
      state: "ok",
      internal: true,
      members: [],
    };
    this.state.networks.push(net);
    return json(201, networkOf(net));
  }

  private attachMember(
    network: string,
    body: { kind: string; name: string },
  ): MockResponse {
    const net = this.network(network);
    if (!net) return apiError(404, "NETWORK_NOT_FOUND", "");
    if (
      body.kind !== "site" &&
      body.kind !== "instance" &&
      body.kind !== "function"
    ) {
      return apiError(400, "INVALID_NETWORK_MEMBER_KIND", "");
    }
    if (net.members.some((m) => m.kind === body.kind && m.name === body.name)) {
      return apiError(409, "NETWORK_ALREADY_ATTACHED", "");
    }
    net.members.push(this.memberOf(net, body.kind, body.name));
    if (body.kind === "function") {
      const fn = this.fn(body.name);
      if (fn) fn.networks = [...fn.networks, network].sort();
      return json(200, {});
    }
    if (body.kind === "instance") {
      const inst = this.state.instances.find((i) => i.key === body.name);
      if (inst) inst.networks = [...inst.networks, network].sort();
      return json(200, {});
    }
    return json(200, { operation: this.redeploy(body.name, "attach") });
  }

  /**
   * A site is redeployed asynchronously: the operation is done at once here
   * (the pages must report an operation already finished when first seen).
   */
  private redeploy(name: string, action: string): string {
    const operation = `op-${action}-${name}`;
    const stack = this.state.stacks.find((s) => s.name === name);
    if (stack) {
      stack.operation = {
        id: operation,
        kind: "update",
        state: "succeeded",
        startedAt: new Date().toISOString(),
        finishedAt: new Date().toISOString(),
      };
    }
    return operation;
  }

  /** The detail of a stack: its networks are those declaring it. */
  private stackDetail(stack: HostingStackSummary): HostingStackDetail {
    return {
      ...stack,
      spec: {
        name: stack.name,
        template: stack.template,
        phpVersion: stack.phpVersion,
        site: stack.site,
        documentRoot: "web",
        domains: stack.domains,
        https: stack.https,
        redirectHttps: stack.redirectHttps,
        resources: { memoryMB: 512, cpus: 1 },
        env: [],
        networks: this.state.networks
          .filter((n) =>
            n.members.some(
              (m) => m.kind === "site" && m.name === stack.name && m.expected,
            ),
          )
          .map((n) => n.name)
          .sort(),
      },
      snippets: { phpIni: "", vhost: "" },
      containers: [],
    };
  }

  private detachMember(
    network: string,
    kind: string,
    name: string,
  ): MockResponse {
    const net = this.network(network);
    if (!net) return apiError(404, "NETWORK_NOT_FOUND", "");
    const member = net.members.find((m) => m.kind === kind && m.name === name);
    if (!member) return apiError(409, "NETWORK_NOT_ATTACHED", "");
    if (kind === "function") {
      const fn = this.fn(name);
      if (fn && fn.networks.length <= 1) {
        return apiError(400, "INVALID_FUNCTION_NETWORK", "");
      }
      if (fn) fn.networks = fn.networks.filter((n) => n !== network);
    }
    if (kind === "instance") {
      const inst = this.state.instances.find((i) => i.key === name);
      if (inst && inst.networks.length <= 1) {
        return apiError(400, "SERVICE_LAST_NETWORK", "");
      }
      if (inst) inst.networks = inst.networks.filter((n) => n !== network);
    }
    net.members = net.members.filter((m) => m !== member);
    return json(
      200,
      kind === "site" ? { operation: this.redeploy(name, "detach") } : {},
    );
  }

  private credentials(instanceKey: string, name: string): CredentialModel[] {
    const inst = this.state.instances.find((i) => i.key === instanceKey);
    const host = inst?.project ?? `svc-${instanceKey}`;
    if (inst?.engine === "elasticmq") {
      return [
        { key: "endpoint", value: `http://${host}:9324` },
        { key: "region", value: "us-east-1" },
        { key: "queueUrlPrefix", value: `http://${host}:9324/000000000000/` },
      ];
    }
    if (inst?.engine === "s3") {
      return [
        { key: "endpoint", value: `http://${host}:8080` },
        { key: "host", value: host },
        { key: "port", value: "8080" },
        { key: "region", value: "us-east-1" },
        { key: "accessKeyId", value: S3_ACCESS_KEY },
        { key: "secretAccessKey", value: S3_SECRET, secret: true },
        { key: "bucket", value: name },
        { key: "pathStyle", value: "true" },
      ];
    }
    return [
      { key: "host", value: host },
      { key: "port", value: "6379" },
      { key: "user", value: name },
      { key: "password", value: S3_SECRET, secret: true },
    ];
  }

  /** POST /services/instances/:instance/update (dedicated instances only). */
  private upgradeInstance(
    key: string,
    body: { version?: string } | undefined,
  ): MockResponse {
    const denied = this.admin();
    if (denied) return denied;
    const inst = this.state.instances.find((i) => i.key === key);
    if (!inst) return apiError(404, "SERVICE_INSTANCE_NOT_FOUND", "");
    if (!inst.options) return apiError(400, "SERVICE_UPDATE_NOT_SUPPORTED", "");
    if (inst.modified) return apiError(409, "SERVICE_INSTANCE_MODIFIED", "");
    const version = body?.version;
    if (!version || version === inst.version) {
      if (!inst.update?.image || !inst.update.latestImage) {
        return json(200, {
          updated: false,
          version: inst.version,
          image: inst.image,
        });
      }
      inst.image = inst.update.latestImage;
      inst.update = { ...inst.update, image: false, latestImage: undefined };
    } else {
      const target = inst.update?.versions?.find((v) => v.version === version);
      if (!target) {
        const older = compareVersions(version, inst.version) < 0;
        return older
          ? apiError(400, "SERVICE_VERSION_DOWNGRADE", "")
          : apiError(400, "INVALID_SERVICE_VERSION", "");
      }
      inst.version = target.version;
      inst.image = target.image;
      inst.supportUntil = target.supportUntil;
      inst.activeSupportUntil = target.activeSupportUntil;
      const versions = inst.update?.versions?.filter(
        (v) => compareVersions(v.version, target.version) > 0,
      );
      inst.update = { image: false, versions };
    }
    return json(200, {
      updated: true,
      version: inst.version,
      image: inst.image,
    });
  }

  private createResource(req: MockRequest): MockResponse {
    const denied = this.admin();
    if (denied) return denied;
    const body = req.body as {
      engine: string;
      version: string;
      name: string;
      options?: Record<string, string>;
      networks?: string[];
    };
    const engine = this.state.servicesConfig.engines?.find(
      (e) => e.id === body.engine,
    );
    if (!engine) return apiError(400, "INVALID_SERVICE_ENGINE", "");
    const key =
      engine.lifecycle === "dedicated"
        ? `${body.engine}-${body.name}`
        : `${body.engine}-${body.version.replace(/\./g, "-")}`;
    if (engine.lifecycle === "dedicated") {
      if (this.state.instances.some((i) => i.key === key)) {
        return apiError(409, "RESOURCE_EXISTS", "Resource already exists");
      }
      this.state.instances.push({
        key,
        engine: body.engine,
        version: body.version,
        image: engine.versions[0].image,
        project: `svc-${key}`,
        name: body.name,
        options: body.options,
        deprecated: false,
        state: "running",
        health: "healthy",
        ready: true,
        modified: false,
        resources: 1,
        networks: body.networks?.length
          ? [...body.networks].sort()
          : ["services"],
      });
    }
    return json(201, {
      resource: {
        instance: key,
        engine: body.engine,
        version: body.version,
        name: body.name,
        managed: true,
        host: `svc-${key}`,
        port: engine.defaultPort,
      },
      credentials: this.credentials(key, body.name),
      instanceCreated: engine.lifecycle === "dedicated",
    });
  }
}

/** The list entry of a function (the detail without its detail fields). */
function functionSummaryOf(f: FunctionDetailModel): FunctionSummaryModel {
  return {
    name: f.name,
    runtime: f.runtime,
    runtimeVersion: f.runtimeVersion,
    handler: f.handler,
    memoryMB: f.memoryMB,
    cpus: f.cpus,
    timeoutSeconds: f.timeoutSeconds,
    tmpSizeMB: f.tmpSizeMB,
    state: f.state,
    health: f.health,
    deployment: f.deployment,
    networks: f.networks,
    egress: f.egress,
    mcpInvocable: f.mcpInvocable,
    createdAt: f.createdAt,
  };
}

/** The list entry of a network: `members` is the number of containers. */
function summaryOf(n: NetworkDetailModel): NetworkSummaryModel {
  return {
    ...networkOf(n),
    state: n.state,
    problem: n.problem,
    reservedOverlap: n.reservedOverlap,
    members: n.members.filter((m) => m.present).length,
  };
}

/** The list entry of a Docker network: `containers` is a count. */
function externalOf(n: DockerNetworkDetailModel): DockerNetworkModel {
  const { members, ...rest } = n;
  return { ...rest, containers: members.length };
}

/** The registry entry of a network (POST / PATCH responses). */
function networkOf(n: NetworkDetailModel) {
  return {
    name: n.name,
    dockerName: n.dockerName,
    id: n.id,
    subnet: n.subnet,
    gateway: n.gateway,
    description: n.description,
    system: n.system,
    createdAt: n.createdAt,
    createdBy: n.createdBy,
  };
}

export function createMockApi(state?: Partial<MockState>): MockApi {
  return new MockApi(state);
}
