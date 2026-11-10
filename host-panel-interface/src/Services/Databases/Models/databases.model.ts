/** Categories of services, in menu order (one menu group each). */
export const SERVICE_KINDS = [
  "database",
  "cache",
  "messaging",
  "queue",
  "storage",
] as const;
export type ServiceKind = (typeof SERVICE_KINDS)[number];

/** Engines the panel can provide with their category, in menu order. */
export const SERVICE_ENGINES = {
  mysql: "database",
  mariadb: "database",
  postgres: "database",
  mongodb: "database",
  redis: "cache",
  valkey: "cache",
  rabbitmq: "messaging",
  nats: "messaging",
  s3: "storage",
  elasticmq: "queue",
  mosquitto: "queue",
} as const satisfies Record<string, ServiceKind>;
export type ServiceEngineId = keyof typeof SERVICE_ENGINES;

export const SERVICE_ENGINE_IDS = Object.keys(
  SERVICE_ENGINES,
) as ServiceEngineId[];

export function isServiceEngine(value: unknown): value is ServiceEngineId {
  return (
    typeof value === "string" &&
    Object.prototype.hasOwnProperty.call(SERVICE_ENGINES, value)
  );
}

/**
 * Wording of the resources of an engine: a database (shared database
 * engines), a vhost (RabbitMQ) or a dedicated instance (Redis, Valkey, NATS).
 */
export type ResourceWording = "database" | "vhost" | "dedicated";

export function resourceWording(
  engine: ServiceEngineModel | undefined,
  id: string,
): ResourceWording {
  if (engine?.lifecycle === "dedicated") return "dedicated";
  return id === "rabbitmq" ? "vhost" : "database";
}

/** Option of a dedicated engine, chosen at creation (fixed values only). */
export interface ServiceOptionModel {
  key: string;
  values: string[];
  default: string;
  /** can be changed after creation (the instance is recreated) */
  mutable: boolean;
  /** sizes: any multiple of step MB between min and max is accepted too */
  custom?: { min: number; max: number; step: number };
}

/** Options whose values are sizes in MB (shown as 256 MB / 1 GB). */
export const SIZE_OPTIONS = [
  "memory",
  "storage",
  "quota",
  "message-size",
  "payload",
];

export interface ServiceVersionModel {
  version: string;
  image: string;
  platform: string;
  /** end of the security fixes (YYYY-MM-DD), when known */
  supportUntil?: string;
  /** end of the active support (bug fixes, YYYY-MM-DD), when known */
  activeSupportUntil?: string;
  /** the security support is over */
  deprecated: boolean;
  /** offered because the server configuration pins it */
  pinned?: boolean;
  /** stricter name limit of this version (e.g. 16 on MySQL 5.6) */
  maxNameLength?: number;
  /** resources may use the legacy password mechanism (old clients) */
  legacyAuth?: boolean;
  /** the version can require TLS from its clients */
  tls?: boolean;
  /** the version can offer TLS without requiring it */
  tlsOptional?: boolean;
}

export interface ServiceEngineModel {
  id: string;
  kind: string;
  defaultPort: number;
  versions: ServiceVersionModel[];
  /** shared: one instance per version; dedicated: one per resource */
  lifecycle: "shared" | "dedicated";
  options?: ServiceOptionModel[];
  maxNameLength: number;
  /** no authentication at all (ElasticMQ): every connected site reaches it */
  unauthenticated?: boolean;
}

/** Exposure policy of the API configuration (mirrored as hints). */
export interface ExposureLimitsModel {
  /** an address must be inside one of them */
  allowedCidrs: string[];
  minPort: number;
  maxPort: number;
  reservedPorts: number[];
  /** TLS may be offered without being required (SERVICES_ALLOW_TLS_OPTIONAL) */
  allowTlsOptional: boolean;
  /** auto: a non-loopback address requires TLS from every client */
  tlsMode: "auto" | "off";
}

export interface ServicesConfigModel {
  enabled: boolean;
  error?: string;
  /** Docker name of the shared network */
  network?: string;
  engines?: ServiceEngineModel[];
  exposure?: ExposureLimitsModel;
  /** state of the version discovery (registries) */
  catalog?: CatalogStatusModel;
}

/** Last discovery of the versions (also the answer of a manual check). */
export interface CatalogStatusModel {
  /** RFC 3339; missing: never checked (built-in catalogs in use) */
  checkedAt?: string;
  /** engines whose last discovery failed, entirely or partly */
  failed?: string[];
  /** a check ran moments ago: none started (the values are the last ones) */
  throttled?: boolean;
}

/** What a dedicated instance can be updated to. */
export interface ServiceUpdateInfoModel {
  /** a newer image of the same version exists */
  image: boolean;
  latestImage?: string;
  /** newer versions it can move to, newest first */
  versions?: ServiceVersionModel[];
}

/** Body of POST /services/instances/{instance}/update. */
export interface InstanceUpgradePayload {
  /** missing: the latest image of the current version */
  version?: string;
}

export interface InstanceUpgradeResultModel {
  /** false: already up to date */
  updated: boolean;
  version: string;
  image: string;
}

/** An update of any kind is available. */
export function updateAvailable(i: ServiceInstanceModel): boolean {
  return !!i.update && (i.update.image || (i.update.versions?.length ?? 0) > 0);
}

/**
 * Compares two dotted versions (17, 8.4, 10.11) segment by segment:
 * negative when a is older than b.
 */
export function compareVersions(a: string, b: string): number {
  const pa = a.split(".").map(Number);
  const pb = b.split(".").map(Number);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const d = (pa[i] || 0) - (pb[i] || 0);
    if (d !== 0) return d;
  }
  return 0;
}

export interface ServiceInstanceModel {
  key: string;
  engine: string;
  version: string;
  image: string;
  project: string;
  /** dedicated instances: their name and options */
  name?: string;
  options?: Record<string, string>;
  deprecated: boolean;
  /** support dates of the instance's version, when known */
  supportUntil?: string;
  activeSupportUntil?: string;
  /** dedicated instances not edited by hand: available updates */
  update?: ServiceUpdateInfoModel;
  state: string;
  health: string;
  ready: boolean;
  modified: boolean;
  /** null when the instance is not ready (its catalog cannot be read). */
  resources: number | null;
  /** published port in effect */
  exposure?: ServiceExposureModel;
  /** the last change of the published port failed */
  exposureError?: boolean;
  /** private networks of the instance (registry names) */
  networks: string[];
  /** engine without authentication (ElasticMQ) */
  unauthenticated?: boolean;
}

/** Body of PUT /services/instances/{instance}/exposure. */
export interface ExposurePayload {
  address: string;
  port: number;
  tlsOptional?: boolean;
}

export interface ServiceExposureModel {
  address: string;
  port: number;
  tls: boolean;
  /** TLS offered but not required */
  tlsOptional?: boolean;
  /** SHA-256 of the certificate */
  fingerprint?: string;
}

export interface ServiceResourceModel {
  instance: string;
  engine: string;
  version: string;
  name: string;
  managed: boolean;
  host: string;
  port: number;
}

export type CredentialKey =
  | "host"
  | "port"
  | "database"
  | "vhost"
  | "user"
  | "password"
  | "uri"
  | "external"
  | "tlsFingerprint"
  // S3
  | "endpoint"
  | "region"
  | "bucket"
  | "accessKeyId"
  | "secretAccessKey"
  | "pathStyle"
  // ElasticMQ
  | "queueUrlPrefix";

export interface CredentialModel {
  key: CredentialKey | string;
  value: string;
  secret?: boolean;
}

export interface CreatedResourceModel {
  resource: ServiceResourceModel;
  credentials: CredentialModel[];
  instanceCreated: boolean;
}

export interface CreateResourcePayload {
  engine: string;
  version: string;
  name: string;
  options?: Record<string, string>;
  /** MySQL 8.4 mysql_native_password, PostgreSQL md5 */
  legacyAuth?: boolean;
  /**
   * Networks of the instance when this request creates it (empty: the
   * shared network; required for an engine without authentication).
   */
  networks?: string[];
}

export interface DeleteResultModel {
  instanceRetired: boolean;
  retireBlocked?: "modified" | "error";
}

/** Same rule as the API (the server stays the source of truth). */
export const RESOURCE_NAME_PATTERN = /^[a-z][a-z0-9_]*$/;
export const MAX_RESOURCE_NAME_LENGTH = 32;

/** Dedicated instance names become host names: no underscore. */
export const DEDICATED_NAME_PATTERN = /^[a-z][a-z0-9]*$/;

/** The catalog entry of a version (TLS capabilities come from the API). */
export function findVersion(
  engine: ServiceEngineModel | undefined,
  version: string,
): ServiceVersionModel | undefined {
  return engine?.versions.find((v) => v.version === version);
}
