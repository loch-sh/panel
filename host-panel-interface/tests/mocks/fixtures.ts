/**
 * Data of the mocked API, shared by the Vitest (MSW) and Playwright tests.
 * Shapes follow the models in src/Services: a type error here means the API
 * contract changed.
 */
import type { ContainerInfo } from "dockerode";
import type { ApiKeyModel } from "../../src/Services/ApiKeys/Models/apikeys.model";
import type { DomainEntry } from "../../src/Services/Domains/Models/domains.model";
import type { SftpUserModel } from "../../src/Services/Sftp/Models/sftp.model";
import type {
  HostingConfigModel,
  HostingStackSummary,
} from "../../src/Services/Hosting/Models/hosting.model";
import type {
  DockerNetworkDetailModel,
  NetworkDetailModel,
  NetworksConfigModel,
} from "../../src/Services/Networks/Models/networks.model";
import type {
  DeploymentModel,
  FunctionDetailModel,
  FunctionsConfigModel,
} from "../../src/Services/Functions/Models/functions.model";
import type {
  ServiceInstanceModel,
  ServicesConfigModel,
} from "../../src/Services/Databases/Models/databases.model";
import {
  UserProfile,
  type MyUserModel,
} from "../../src/Services/Users/Models/users.model";

/** Base URL of the mocked API (never a real server). */
export const API_URL = "http://api.test";
export const WS_URL = "ws://api.test";

export const adminUser: MyUserModel = {
  name: "Ada Admin",
  email: "ada@example.com",
  profile: UserProfile.ADMIN,
  permissions: [],
};

export const restrictedUser: MyUserModel = {
  name: "Rémi Restricted",
  email: "remi@example.com",
  profile: UserProfile.RESTRICTED,
  permissions: ["shop"],
};

const sizes = (values: string[], def: string) => ({
  values,
  default: def,
  mutable: true,
});

export function servicesConfig(): ServicesConfigModel {
  return {
    enabled: true,
    network: "loch-services",
    catalog: { checkedAt: new Date(Date.now() - 5 * 60_000).toISOString() },
    exposure: {
      allowedCidrs: ["127.0.0.1/32", "203.0.113.10/32", "10.8.0.0/24"],
      minPort: 1024,
      maxPort: 32767,
      reservedPorts: [2538, 8080],
      allowTlsOptional: true,
      tlsMode: "auto",
    },
    engines: [
      {
        id: "postgres",
        kind: "database",
        defaultPort: 5432,
        lifecycle: "shared",
        maxNameLength: 32,
        versions: [
          {
            version: "17",
            image: "mirror.gcr.io/library/postgres:17",
            platform: "",
            supportUntil: "2029-11-08",
            deprecated: false,
            legacyAuth: true,
            tls: true,
            tlsOptional: true,
          },
        ],
      },
      {
        id: "redis",
        kind: "cache",
        defaultPort: 6379,
        lifecycle: "dedicated",
        maxNameLength: 24,
        versions: [
          {
            version: "8.4",
            image: "mirror.gcr.io/library/redis:8.4-alpine",
            platform: "",
            supportUntil: "",
            deprecated: false,
            tls: true,
          },
        ],
        options: [
          {
            key: "memory",
            ...sizes(["128", "256", "512"], "256"),
            custom: { min: 128, max: 65536, step: 128 },
          },
          {
            key: "policy",
            ...sizes(["noeviction", "allkeys-lru"], "noeviction"),
          },
          {
            key: "persistence",
            values: ["aof", "rdb", "none"],
            default: "aof",
            mutable: false,
          },
        ],
      },
      {
        id: "s3",
        kind: "storage",
        defaultPort: 8080,
        lifecycle: "dedicated",
        maxNameLength: 24,
        versions: [
          {
            version: "1.4",
            image: "ghcr.io/loch-sh/s3:1.4.0",
            platform: "",
            supportUntil: "",
            deprecated: false,
          },
        ],
        options: [
          {
            key: "memory",
            ...sizes(["128", "256", "512", "1024"], "256"),
            custom: { min: 128, max: 8192, step: 128 },
          },
          {
            key: "quota",
            ...sizes(["1024", "10240", "51200"], "10240"),
            custom: { min: 1024, max: 10485760, step: 1024 },
          },
          {
            key: "encryption",
            values: ["off", "on"],
            default: "off",
            mutable: false,
          },
        ],
      },
      {
        id: "elasticmq",
        kind: "queue",
        defaultPort: 9324,
        lifecycle: "dedicated",
        maxNameLength: 24,
        unauthenticated: true,
        versions: [
          {
            version: "1.7",
            image: "mirror.gcr.io/softwaremill/elasticmq-native:1.7.1",
            platform: "",
            supportUntil: "",
            deprecated: false,
          },
        ],
        options: [
          { key: "memory", ...sizes(["128", "256", "512"], "256") },
          {
            key: "limits",
            values: ["strict", "relaxed"],
            default: "strict",
            mutable: true,
          },
        ],
      },
      {
        id: "mosquitto",
        kind: "queue",
        defaultPort: 1883,
        lifecycle: "dedicated",
        maxNameLength: 24,
        versions: [
          {
            version: "2.0",
            image: "mirror.gcr.io/library/eclipse-mosquitto:2.0.22",
            platform: "",
            supportUntil: "",
            deprecated: false,
          },
        ],
        options: [
          { key: "memory", ...sizes(["64", "128", "256"], "128") },
          { key: "queued", ...sizes(["1000", "10000"], "1000") },
        ],
      },
    ],
  };
}

export function serviceInstances(): ServiceInstanceModel[] {
  const base = {
    deprecated: false,
    state: "running",
    health: "healthy",
    ready: true,
    modified: false,
  };
  return [
    {
      ...base,
      key: "postgres-17",
      engine: "postgres",
      version: "17",
      image: "mirror.gcr.io/library/postgres:17",
      project: "svc-postgres-17",
      resources: 1,
      networks: ["services"],
    },
    {
      ...base,
      key: "redis-cache",
      engine: "redis",
      version: "8.4",
      image: "mirror.gcr.io/library/redis:8.4-alpine",
      project: "svc-redis-cache",
      name: "cache",
      options: { memory: "256", policy: "noeviction", persistence: "aof" },
      supportUntil: "2027-05-31",
      activeSupportUntil: "2026-11-30",
      update: {
        image: true,
        latestImage: "mirror.gcr.io/library/redis:8.4.1-alpine",
        versions: [
          {
            version: "8.6",
            image: "mirror.gcr.io/library/redis:8.6-alpine",
            platform: "",
            supportUntil: "2029-05-31",
            activeSupportUntil: "2028-05-31",
            deprecated: false,
          },
        ],
      },
      resources: 1,
      networks: ["services", "backend"],
    },
  ];
}

export function hostingConfig(servicesEnabled = true): HostingConfigModel {
  return {
    enabled: true,
    sitesDirectory: "/data/websites",
    templates: ["php-nginx", "php-apache", "nginx", "apache"],
    phpVersions: [
      {
        tag: "8.4",
        activeSupportUntil: "2026-12-31",
        securitySupportUntil: "2028-12-31",
        deprecated: false,
      },
    ],
    defaults: {
      phpVersion: "8.4",
      documentRoot: "web",
      php: {
        memoryLimit: "256M",
        uploadMaxFilesize: "64M",
        postMaxSize: "64M",
        maxExecutionTime: 60,
        displayErrors: false,
        timezone: "Europe/Paris",
      },
      resources: { memoryMB: 512, cpus: 1 },
    },
    limits: {
      maxMemoryMB: 8192,
      maxCpus: 4,
      maxDomains: 20,
      maxEnvVars: 100,
      maxSnippetBytes: 65536,
    },
    defaultNetworks: servicesEnabled ? ["services"] : [],
  };
}

/** A hosted stack of the list. */
export function hostingStack(name: string): HostingStackSummary {
  return {
    name,
    template: "php-nginx",
    phpVersion: "8.4",
    image: "mirror.gcr.io/webdevops/php-nginx:8.4",
    site: name,
    domains: [`${name}.example.com`],
    https: true,
    redirectHttps: true,
    project: `hosted-${name}`,
    status: "running",
    running: 1,
    total: 1,
    modified: false,
    operation: null,
  };
}

const CREATED_AT = "2026-10-01T09:30:00Z";

/** Registered networks with their members (the mock derives the list). */
export function networks(): NetworkDetailModel[] {
  return [
    {
      name: "services",
      dockerName: "loch-services",
      id: "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2",
      subnet: "172.30.0.0/16",
      gateway: "172.30.0.1",
      system: true,
      createdAt: CREATED_AT,
      state: "ok",
      internal: true,
      members: [
        {
          kind: "instance",
          name: "postgres-17",
          container: "svc-postgres-17",
          containerId: "0123456789ab",
          state: "running",
          ipv4: "172.30.0.2",
          mac: "02:42:ac:1e:00:02",
          expected: true,
          present: true,
        },
        {
          kind: "instance",
          name: "redis-cache",
          container: "svc-redis-cache",
          containerId: "1123456789ab",
          state: "running",
          ipv4: "172.30.0.3",
          mac: "02:42:ac:1e:00:03",
          expected: true,
          present: true,
        },
      ],
    },
    {
      name: "backend",
      dockerName: "loch-net-backend",
      id: "b1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2",
      subnet: "10.20.0.0/24",
      gateway: "10.20.0.1",
      description: "Shop and its cache",
      createdAt: CREATED_AT,
      createdBy: "ada@example.com",
      state: "ok",
      internal: true,
      members: [
        {
          kind: "instance",
          name: "redis-cache",
          container: "svc-redis-cache",
          containerId: "1123456789ab",
          state: "running",
          ipv4: "10.20.0.2",
          mac: "02:42:0a:14:00:02",
          expected: true,
          present: true,
        },
        {
          kind: "site",
          name: "shop",
          container: "hosted-shop-web-1",
          expected: true,
          present: false,
        },
        {
          kind: "unmanaged",
          name: "ci-runner",
          container: "ci-runner-1",
          containerId: "9923456789ab",
          state: "running",
          ipv4: "10.20.0.9",
          mac: "02:42:0a:14:00:09",
          expected: false,
          present: true,
        },
      ],
    },
  ];
}

/** Creation rules of the networks (the mock derives `count`). */
export function networksConfig(): Omit<NetworksConfigModel, "count"> {
  return {
    max: 20,
    reservedRanges: ["192.168.1.0/24", "10.8.0.0/16"],
    privateRanges: [
      "10.0.0.0/8",
      "172.16.0.0/12",
      "192.168.0.0/16",
      "100.64.0.0/10",
    ],
    minPrefix: 16,
    maxPrefix: 28,
  };
}

const hex64 = (c: string) => c.repeat(64);

/** Docker networks the panel did not create, with their containers. */
export function dockerNetworks(): DockerNetworkDetailModel[] {
  return [
    {
      id: hex64("1"),
      name: "traefik",
      driver: "bridge",
      scope: "local",
      internal: false,
      subnets: ["172.18.0.0/16"],
      gateways: ["172.18.0.1"],
      origin: "compose",
      composeProject: "proxy",
      createdAt: CREATED_AT,
      containers: 2,
      members: [
        {
          kind: "site",
          name: "shop",
          container: "hosted-shop-web-1",
          containerId: "aa23456789ab",
          state: "running",
          ipv4: "172.18.0.5",
          mac: "02:42:ac:12:00:05",
          expected: false,
          present: true,
        },
        {
          kind: "unmanaged",
          name: "traefik",
          container: "proxy-traefik-1",
          containerId: "bb23456789ab",
          state: "running",
          ipv4: "172.18.0.2",
          mac: "02:42:ac:12:00:02",
          expected: false,
          present: true,
        },
      ],
    },
    {
      id: hex64("2"),
      name: "ci_default",
      driver: "bridge",
      scope: "local",
      internal: false,
      subnets: ["192.168.1.0/24"],
      gateways: ["192.168.1.1"],
      origin: "compose",
      composeProject: "ci",
      createdAt: CREATED_AT,
      containers: 0,
      reservedOverlap: "192.168.1.0/24",
      members: [],
    },
    {
      id: hex64("3"),
      name: "bridge",
      driver: "bridge",
      scope: "local",
      internal: false,
      subnets: ["172.17.0.0/16"],
      gateways: ["172.17.0.1"],
      origin: "docker",
      createdAt: CREATED_AT,
      containers: 0,
      members: [],
    },
  ];
}

/** A generated secret of the panel's form (32 alphanumerics). */
export const S3_SECRET = "FakeSecretForTests0000000000000A";
export const S3_ACCESS_KEY = "LOCHABCDEFGHIJKLMNOP";

/** Configuration of the functions module (enabled, a runtime installed). */
export function functionsConfig(): FunctionsConfigModel {
  return {
    enabled: true,
    config: {
      arch: "arm64",
      runtimes: [
        { id: "nodejs", versions: ["26", "24", "22"] },
        { id: "python", versions: ["3.14", "3.13", "3.12"] },
        {
          id: "provided",
          versions: ["al2023"],
          custom: true,
          defaultHandler: "bootstrap",
        },
      ],
      runtimeReady: true,
      max: 50,
      count: 3,
      minMemoryMB: 128,
      maxMemoryMB: 3008,
      maxTotalMemoryMB: 8192,
      maxTotalCpus: 8,
      usedMemoryMB: 1280,
      usedCpus: 2.5,
      maxCodeBytes: 50 * 1024 * 1024,
      deploymentsKept: 3,
      defaults: { memoryMB: 512, cpus: 1, timeoutSeconds: 30, tmpSizeMB: 512 },
      maxTimeoutSeconds: 900,
      maxEventBytes: 6 * 1024 * 1024,
    },
  };
}

const NODE_IMAGE =
  "mirror.gcr.io/amazon/aws-lambda-nodejs:22-arm64@sha256:" + "a1".repeat(32);
const PYTHON_IMAGE =
  "mirror.gcr.io/amazon/aws-lambda-python:3.13-arm64@sha256:" + "b2".repeat(32);

const deployment = (
  id: string,
  active: boolean,
  files = 12,
): DeploymentModel => ({
  id,
  treeHash: "c3".repeat(32),
  zipSha256: "d4".repeat(32),
  zipSize: 48_213,
  files,
  bytes: 183_402,
  active,
});

/**
 * Functions with their detail: a running Node.js one with two deployments,
 * one edited outside the panel (purge only), and a Python one accepting
 * SigV4 callers, with Internet access and a secret.
 */
export function functions(): FunctionDetailModel[] {
  return [
    {
      name: "thumbs",
      runtime: "nodejs",
      runtimeVersion: "22",
      handler: "index.handler",
      memoryMB: 512,
      cpus: 1,
      timeoutSeconds: 30,
      tmpSizeMB: 512,
      state: "running",
      health: "healthy",
      deployment: "20261105T120000Z-1a2b3c4d",
      networks: ["fn-thumbs"],
      egress: false,
      mcpInvocable: false,
      createdAt: CREATED_AT,
      arch: "arm64",
      image: NODE_IMAGE,
      env: [{ name: "BUCKET", value: "media" }],
      secrets: [],
      deployments: [
        deployment("20261105T120000Z-1a2b3c4d", true),
        deployment("20261101T080000Z-0f0e0d0c", false, 10),
      ],
      endpoint: "http://fn-thumbs:8080/2015-03-31/functions/thumbs/invocations",
      sigv4: false,
    },
    {
      name: "legacy",
      runtime: "nodejs",
      runtimeVersion: "24",
      handler: "index.handler",
      memoryMB: 256,
      cpus: 0.5,
      timeoutSeconds: 10,
      tmpSizeMB: 512,
      state: "modified",
      deployment: "20261020T100000Z-99aa88bb",
      networks: ["fn-legacy"],
      egress: false,
      mcpInvocable: false,
      createdAt: CREATED_AT,
      arch: "arm64",
      image: NODE_IMAGE,
      env: [],
      secrets: [],
      deployments: [deployment("20261020T100000Z-99aa88bb", true)],
      endpoint: "http://fn-legacy:8080/2015-03-31/functions/legacy/invocations",
      sigv4: false,
    },
    {
      name: "report",
      runtime: "python",
      runtimeVersion: "3.13",
      handler: "lambda_function.lambda_handler",
      memoryMB: 512,
      cpus: 1,
      timeoutSeconds: 120,
      tmpSizeMB: 1024,
      state: "stopped",
      deployment: "20261103T070000Z-5e6f7a8b",
      networks: ["backend", "fn-report"],
      egress: true,
      mcpInvocable: true,
      createdAt: CREATED_AT,
      arch: "arm64",
      image: PYTHON_IMAGE,
      env: [{ name: "LOG_LEVEL", value: "info" }],
      secrets: ["DB_PASSWORD"],
      deployments: [deployment("20261103T070000Z-5e6f7a8b", true)],
      endpoint: "http://fn-report:8080/2015-03-31/functions/report/invocations",
      sigv4: true,
    },
  ];
}

/** Credentials returned once by a creation or a rotation. */
export const FUNCTION_TOKEN = "FakeBearerTokenForTests0000000000000000000A";
export const FUNCTION_ACCESS_KEY = "LOCHFNACCESSKEY00001";
export const FUNCTION_SECRET_KEY = "FakeSigV4SecretForTests00000000000000000B";

/** A running container, in a compose project when `project` is given. */
export function container(name: string, project?: string): ContainerInfo {
  return {
    Id: `${name.padEnd(12, "0")}${"0".repeat(52)}`.slice(0, 64),
    Names: [`/${name}`],
    Image: "mirror.gcr.io/library/nginx:1.30-alpine",
    ImageID: `sha256:${"a".repeat(64)}`,
    Command: "nginx -g 'daemon off;'",
    Created: 1_760_000_000,
    Ports: [],
    Labels: project ? { "com.docker.compose.project": project } : {},
    State: "running",
    Status: "Up 2 hours",
    HostConfig: { NetworkMode: "bridge" },
    NetworkSettings: { Networks: {} },
    Mounts: [],
  };
}

/** A domain entry routing the given names to a container. */
export function domainEntry(
  containerName: string,
  domains: string[],
): DomainEntry {
  return {
    containerId: containerName,
    containerName,
    containerState: "running",
    containerImage: "mirror.gcr.io/library/nginx:1.30-alpine",
    traefikEnabled: true,
    routers: [
      {
        name: containerName,
        rule: domains.map((d) => `Host(\`${d}\`)`).join(" || "),
        domains,
        entrypoints: ["websecure"],
        tls: true,
        serviceName: containerName,
      },
    ],
    services: [{ name: containerName, port: 80 }],
  };
}

export function sftpUser(username: string, site: string): SftpUserModel {
  return {
    username,
    site,
    subPath: "",
    homeDir: `/data/websites/hosted/${site}`,
    status: 1,
    quotaSize: 0,
    quotaFiles: 0,
    uploadBandwidth: 0,
    downloadBandwidth: 0,
    usedQuotaSize: 0,
    usedQuotaFiles: 0,
    requirePasswordChange: false,
    lastLogin: 0,
    createdAt: 1_760_000_000_000,
    publicKeys: [],
  };
}

export function apiKey(id: string): ApiKeyModel {
  return {
    id,
    name: `key-${id}`,
    keyPrefix: `loch_${id}`,
    scopes: ["containers:read"],
    expiresAt: "2027-11-06T00:00:00Z",
    createdAt: "2026-11-06T00:00:00Z",
  };
}
