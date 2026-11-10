/** Registry name of the network shared with the services module. */
export const SYSTEM_NETWORK = "services";

export type NetworkState = "ok" | "missing" | "compromised";

/** A private network, as the API stores it. */
export interface NetworkModel {
  /** registry name (e.g. "backend"), "services" for the system network */
  name: string;
  /** Docker name (loch-net-<name>, SERVICES_NETWORK for the system one) */
  dockerName: string;
  /** Docker ID */
  id: string;
  subnet: string;
  gateway?: string;
  description?: string;
  /** the network shared with the services module: cannot be deleted */
  system?: boolean;
  createdAt: string;
  createdBy?: string;
}

/** A network of the list. */
export interface NetworkSummaryModel extends NetworkModel {
  state: NetworkState;
  /** why the network is compromised (server wording, technical) */
  problem?: string;
  /** number of containers on the Docker network */
  members: number;
  /** the reserved range (NETWORKS_RESERVED_RANGES) its subnet overlaps */
  reservedOverlap?: string;
}

export type NetworkMemberKind =
  | "site"
  | "instance"
  | "function"
  | "panel"
  | "unmanaged";

/** Kinds of members the panel attaches and detaches. */
export type AttachableKind = "site" | "instance" | "function";

export interface NetworkMemberModel {
  kind: NetworkMemberKind;
  /** site: hosted stack name; instance: service instance key; function: its name */
  name: string;
  container?: string;
  /** 12 characters */
  containerId?: string;
  /** Docker state of the container */
  state?: string;
  ipv4?: string;
  mac?: string;
  /** declared by its compose file (or its reference) */
  expected: boolean;
  /** on the Docker network now */
  present: boolean;
}

/** A network with its members (`members` is the list, not the count). */
export interface NetworkDetailModel extends Omit<
  NetworkSummaryModel,
  "members"
> {
  internal: boolean;
  members: NetworkMemberModel[];
}

/** Creation rules of the private networks (GET /networks/config). */
export interface NetworksConfigModel {
  /** maximum number of private networks (the system one not counted) */
  max: number;
  /** private networks created so far */
  count: number;
  /** LAN / VPN ranges used outside Docker: a subnet must not overlap them */
  reservedRanges: string[];
  /** ranges a subnet must come from */
  privateRanges: string[];
  minPrefix: number;
  maxPrefix: number;
}

export type DockerNetworkOrigin = "docker" | "compose" | "other";

/** A Docker network the panel did not create (read-only). */
export interface DockerNetworkModel {
  /** full Docker ID (64 hex) */
  id: string;
  name: string;
  driver: string;
  scope: string;
  internal: boolean;
  subnets: string[];
  gateways: string[];
  /** docker: bridge, host, none; compose: created by a compose project */
  origin: DockerNetworkOrigin;
  composeProject?: string;
  createdAt: string;
  /** number of containers on the network */
  containers: number;
  reservedOverlap?: string;
}

/** A Docker network the panel did not create, with its containers. */
export interface DockerNetworkDetailModel extends DockerNetworkModel {
  members: NetworkMemberModel[];
}

export interface CreateNetworkPayload {
  name: string;
  description?: string;
  /** empty: Docker allocates one */
  subnet?: string;
}

export interface MemberOperationResponse {
  /** hosting operation id (sites are redeployed asynchronously) */
  operation?: string;
}

/** Same rule as the API (the server stays the source of truth). */
export const NETWORK_NAME_PATTERN = /^[a-z]([a-z0-9-]{0,22}[a-z0-9])?$/;
export const NETWORK_NAME_MAX_LENGTH = 24;
export const NETWORK_DESCRIPTION_MAX_LENGTH = 200;
export const RESERVED_NETWORK_NAMES = [
  SYSTEM_NETWORK,
  "pub",
  "default",
  "host",
  "none",
  "bridge",
  "traefik",
  "config",
  "docker",
];

export type NetworkProblemKey =
  | "replaced"
  | "notBridge"
  | "notInternal"
  | "unexpectedOptions"
  | "missingLabel"
  | "unexpectedLabel"
  | "composeProject"
  | "unexpectedSubnet"
  | "inspectFailed"
  | "other";

/** Problem codes of a network (`problem`, stable API codes), by translation key. */
const NETWORK_PROBLEMS: Record<string, NetworkProblemKey> = {
  replaced: "replaced",
  not_bridge: "notBridge",
  not_internal: "notInternal",
  unexpected_options: "unexpectedOptions",
  missing_label: "missingLabel",
  unexpected_label: "unexpectedLabel",
  compose_project: "composeProject",
  unexpected_subnet: "unexpectedSubnet",
  inspect_failed: "inspectFailed",
};

/** Translation key (under `problems`) of a problem, "other" when unknown. */
export function networkProblemKey(
  problem: string | undefined,
): NetworkProblemKey {
  return (problem && NETWORK_PROBLEMS[problem]) || "other";
}
