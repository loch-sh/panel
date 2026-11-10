import { AttachableKind } from "../../Services/Networks/Models/networks.model";

export const NETWORKS_PATH = "/networks";

export function networkPath(name: string): string {
  return `${NETWORKS_PATH}/${encodeURIComponent(name)}`;
}

/** The list, on its "Other Docker networks" tab. */
export const DOCKER_NETWORKS_PATH = `${NETWORKS_PATH}?tab=docker`;

export function dockerNetworkPath(id: string): string {
  return `${NETWORKS_PATH}/docker/${encodeURIComponent(id)}`;
}

/** Router state the creation page hands to the detail page. */
export interface CreatedNetworkState {
  /** sites being redeployed onto the new network: name -> operation id */
  pendingSites?: Record<string, string>;
  /** members that could not be attached, with the reason */
  failedMembers?: { kind: AttachableKind; name: string; message: string }[];
}
