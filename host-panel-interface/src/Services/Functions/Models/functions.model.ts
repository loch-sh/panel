/**
 * Models of the functions module (serverless, AWS Lambda handlers), typed
 * from the API (`internal/functions/views.go`, `functions_handler.go`).
 */

/**
 * State of a function. `modified` (compose project edited outside the
 * panel) and `invalid` (unreadable reference) can only be purged;
 * `unverified`: a container runs but is not the reference's.
 */
export type FunctionState =
  | "not_deployed"
  | "running"
  | "stopped"
  | "missing"
  | "modified"
  | "unverified"
  | "invalid";

export const FUNCTION_STATES: readonly FunctionState[] = [
  "not_deployed",
  "running",
  "stopped",
  "missing",
  "modified",
  "unverified",
  "invalid",
];

export function isFunctionState(state: string): state is FunctionState {
  return (FUNCTION_STATES as readonly string[]).includes(state);
}

/** States that only a purge can remove (no file of the function is run). */
export function needsPurge(state: string): boolean {
  return state === "modified" || state === "invalid";
}

export interface FunctionLimits {
  memoryMB: number;
  cpus: number;
  timeoutSeconds: number;
  tmpSizeMB: number;
}

/** A function of the list (`GET /functions`). */
export interface FunctionSummaryModel extends FunctionLimits {
  name: string;
  /** runtime id: "nodejs", "python" or "provided" (custom runtime) */
  runtime: string;
  runtimeVersion: string;
  handler: string;
  state: FunctionState | string;
  /** Docker health of the running container (healthy, unhealthy, starting) */
  health?: string;
  /** id of the active deployment (absent: no code deployed yet) */
  deployment?: string;
  networks: string[];
  /** outbound Internet access */
  egress: boolean;
  /**
   * The MCP tool of the loch CLI may invoke it: a guard-rail, not an access
   * control (that is the functions:invoke scope). Changing it needs
   * functions:deploy.
   */
  mcpInvocable: boolean;
  createdAt: string;
}

export interface EnvVarModel {
  name: string;
  value: string;
}

/** A kept deployment (extracted zip). */
export interface DeploymentModel {
  /** `<UTC date>T<time>Z-<8 hex>`, e.g. 20261105T120000Z-1a2b3c4d */
  id: string;
  treeHash: string;
  zipSha256: string;
  zipSize: number;
  files: number;
  /** extracted size */
  bytes: number;
  active: boolean;
  /** deploy-time checks that did not refuse the code (DEPLOYMENT_WARNINGS) */
  warnings?: string[];
}

/** Warning codes of a deployment (custom runtime: dynamic bootstrap). */
export const DEPLOYMENT_WARNINGS = ["FUNCTION_BOOTSTRAP_DYNAMIC"] as const;
export type DeploymentWarning = (typeof DEPLOYMENT_WARNINGS)[number];
export const isDeploymentWarning = (w: string): w is DeploymentWarning =>
  (DEPLOYMENT_WARNINGS as readonly string[]).includes(w);

/** One function (`GET /functions/{name}`): secret names only, never values. */
export interface FunctionDetailModel extends FunctionSummaryModel {
  arch: string;
  /** pinned image reference: `<repository>:<tag>@sha256:<digest>` */
  image: string;
  env: EnvVarModel[];
  /** names only */
  secrets: string[];
  deployments: DeploymentModel[];
  /** Invoke URL for the members of the function's networks */
  endpoint: string;
  /** SigV4 callers are accepted (the key pair is in the container) */
  sigv4: boolean;
  /** why the function is `unverified` (server wording, technical) */
  problem?: string;
}

export interface FunctionRuntimeModel {
  id: string;
  /** newest first */
  versions: string[];
  /** custom runtime: the code is a "bootstrap" executable */
  custom?: boolean;
  /** used when the handler is omitted (custom runtimes) */
  defaultHandler?: string;
}

/** Configuration of the module (`config` of `GET /functions/config`). */
export interface FunctionsInfoModel {
  arch: string;
  runtimes: FunctionRuntimeModel[];
  /** a trusted runtime binary is installed: nothing can run otherwise */
  runtimeReady: boolean;
  max: number;
  count: number;
  minMemoryMB: number;
  maxMemoryMB: number;
  /** sum of the memory limits of every function */
  maxTotalMemoryMB: number;
  maxTotalCpus: number;
  usedMemoryMB: number;
  usedCpus: number;
  maxCodeBytes: number;
  deploymentsKept: number;
  defaults: FunctionLimits;
  maxTimeoutSeconds: number;
  maxEventBytes: number;
}

export interface FunctionsConfigModel {
  enabled: boolean;
  /** FUNCTIONS_MISCONFIGURED: enabled, but the installation check failed */
  error?: string;
  config?: FunctionsInfoModel;
}

/** Invoke credentials, returned once (creation and rotation). */
export interface FunctionCredentialsModel {
  /** `Authorization: Bearer <token>` */
  token: string;
  accessKeyId?: string;
  secretAccessKey?: string;
  endpoint: string;
  region: string;
}

export interface CreateFunctionPayload {
  name: string;
  runtime: string;
  runtimeVersion: string;
  /** optional on the custom runtime (default bootstrap) */
  handler?: string;
  memoryMB?: number;
  cpus?: number;
  timeoutSeconds?: number;
  tmpSizeMB?: number;
  env?: EnvVarModel[];
  /** joined besides the function's own network fn-<name> */
  networks?: string[];
  egress?: boolean;
  sigv4?: boolean;
}

export interface CreatedFunctionModel {
  function: FunctionDetailModel;
  credentials: FunctionCredentialsModel;
}

export interface UpdateFunctionPayload {
  memoryMB?: number;
  cpus?: number;
  timeoutSeconds?: number;
  tmpSizeMB?: number;
  /** needs functions:deploy; "" resets a custom runtime's to bootstrap */
  handler?: string;
  /** replaces every variable; needs functions:deploy */
  env?: EnvVarModel[];
  /** MCP clients may invoke it; needs functions:deploy */
  mcpInvocable?: boolean;
}

export interface DeleteFunctionResultModel {
  containers: number;
  /** the dedicated network still has other members: it is kept */
  networkKept?: boolean;
}

/**
 * Envelope of an invocation (always 200). Everything in it was written by
 * the function: untrusted data, rendered as text only.
 */
export interface InvokeResultModel {
  statusCode: number;
  functionError?: string;
  requestId?: string;
  durationMs: number;
  /** the response as text when it is UTF-8 */
  payload?: string;
  /** the response otherwise */
  payloadBase64?: string;
  /** end of the invocation's logs (4 KiB) */
  logTail: string;
}

export type FunctionAction = "start" | "stop" | "restart";

/** Same rule as the API (1 to 21 characters: fn-<name> fits 24). */
export const FUNCTION_NAME_PATTERN = /^[a-z]([a-z0-9-]{0,19}[a-z0-9])?$/;
export const FUNCTION_NAME_MAX_LENGTH = 21;
export const RESERVED_FUNCTION_NAMES = [
  "config",
  "new",
  "default",
  "all",
  "latest",
  "function",
  "functions",
  "deployments",
  "invoke",
  "panel",
  "loch",
  "system",
  "api",
];

/** `<file>.<function>` (Node.js) or `<module>.<function>` (Python). */
export const HANDLER_PATTERN =
  /^[A-Za-z0-9_-]+(?:\/[A-Za-z0-9_-]+)*(?:\.[A-Za-z_][A-Za-z0-9_]*)+$/;
/** Custom runtime: any name passed to the bootstrap as _HANDLER. */
export const CUSTOM_HANDLER_PATTERN =
  /^[A-Za-z0-9_][A-Za-z0-9_.-]*(?:\/[A-Za-z0-9_][A-Za-z0-9_.-]*)*$/;
export const HANDLER_MAX_LENGTH = 128;

/** The custom runtime (Go, Rust…: a "bootstrap" executable, provided.al2023). */
export const CUSTOM_RUNTIME = "provided";
export const BOOTSTRAP_FILE = "bootstrap";
export const isCustomRuntime = (runtime: string) => runtime === CUSTOM_RUNTIME;

/**
 * Whether a handler is accepted by the API for a runtime; the empty
 * handler of the custom runtime means its default (bootstrap).
 */
export function handlerValid(runtime: string, handler: string): boolean {
  if (isCustomRuntime(runtime)) {
    return (
      handler === "" ||
      (handler.length <= HANDLER_MAX_LENGTH &&
        CUSTOM_HANDLER_PATTERN.test(handler))
    );
  }
  return handler.length <= HANDLER_MAX_LENGTH && HANDLER_PATTERN.test(handler);
}

/** Default handler of a runtime (the AWS console's; empty: bootstrap). */
export function defaultHandler(runtime: string): string {
  if (isCustomRuntime(runtime)) return "";
  return runtime === "python"
    ? "lambda_function.lambda_handler"
    : "index.handler";
}

/** Build and zip commands of a Go bootstrap for an architecture (Go's names). */
export function goBuildCommand(arch: string): string {
  return `CGO_ENABLED=0 GOOS=linux GOARCH=${arch} go build -tags lambda.norpc -o bootstrap . && zip function.zip bootstrap`;
}

export const ENV_NAME_PATTERN = /^[A-Za-z_][A-Za-z0-9_]{0,127}$/;
export const ENV_VALUE_MAX_BYTES = 4096;
export const MAX_ENV_ENTRIES = 100;

const RESERVED_ENV_PREFIXES = [
  "AWS_LAMBDA_",
  "LAMBDA_",
  "LD_",
  "LOCH_RUNTIME_",
  "AWS_CONTAINER_",
  "AWS_XRAY_",
  "_",
];
const RESERVED_ENV_NAMES = new Set([
  "AWS_REGION",
  "AWS_DEFAULT_REGION",
  "AWS_EXECUTION_ENV",
  "AWS_EC2_METADATA_DISABLED",
  "PATH",
  "HOME",
  "HOSTNAME",
  "PYTHONDONTWRITEBYTECODE",
]);

/** A name set by the panel, the runtime or the AWS bootstrap (case-insensitive). */
export function isReservedEnvName(name: string): boolean {
  const upper = name.toUpperCase();
  return (
    RESERVED_ENV_NAMES.has(upper) ||
    RESERVED_ENV_PREFIXES.some((p) => upper.startsWith(p))
  );
}

/** Why a value is refused by the API (one line of UTF-8, 4096 bytes). */
export function envValueProblem(value: string): "lines" | "size" | null {
  if (/[\u0000\r\n]/.test(value)) return "lines";
  if (new TextEncoder().encode(value).length > ENV_VALUE_MAX_BYTES) {
    return "size";
  }
  return null;
}

/** Bounds of the resources (the configuration bounds the memory). */
export const MIN_CPUS = 0.1;
export const MAX_CPUS = 16;
export const MIN_TMP_MB = 16;
export const MAX_TMP_MB = 10240;

/**
 * Region of the invoke endpoint: the runtime's fixed default (the API does
 * not report it outside the credentials, which carry it as `region`).
 */
export const FUNCTION_REGION = "us-east-1";

/** The function's own private network, created with it. */
export const dedicatedNetwork = (name: string) => `fn-${name}`;

/** Its container and compose project (also its hostname on its networks). */
export const functionContainer = (name: string) => `fn-${name}`;

/** Splits a pinned image reference into the reference and its digest. */
export function splitImage(image: string): { ref: string; digest?: string } {
  const at = image.indexOf("@");
  return at < 0
    ? { ref: image }
    : { ref: image.slice(0, at), digest: image.slice(at + 1) };
}

/** Date of a deployment, read from its id (`20261105T120000Z-…`). */
export function deploymentDate(id: string | undefined): Date | null {
  const m = /^(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})Z-/.exec(id ?? "");
  if (!m) return null;
  const [, y, mo, d, h, mi, s] = m.map(Number);
  if (mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || s > 59) {
    return null;
  }
  const date = new Date(Date.UTC(y, mo - 1, d, h, mi, s));
  // Date.UTC rolls over (Feb 31 -> Mar 3): refuse what it changed.
  if (Number.isNaN(date.getTime()) || date.getUTCDate() !== d) return null;
  return date;
}
