import { AxiosInstance } from "../../Utils/axios";
import {
  AttachableKind,
  CreateNetworkPayload,
  DockerNetworkDetailModel,
  DockerNetworkModel,
  MemberOperationResponse,
  NetworkDetailModel,
  NetworkModel,
  NetworkSummaryModel,
  NetworksConfigModel,
} from "./Models/networks.model";

export const networksServiceKeys = {
  networks: "networks",
  network: "network",
  config: "networksConfig",
  dockerNetworks: "dockerNetworks",
  dockerNetwork: "dockerNetwork",
};

const networkPath = (name: string) => `/networks/${encodeURIComponent(name)}`;

export const networksService = {
  list: async () => {
    const { data } =
      await AxiosInstance.get<NetworkSummaryModel[]>("/networks");
    return data;
  },
  /** Creation rules: limit, reserved and private ranges. */
  config: async () => {
    const { data } =
      await AxiosInstance.get<NetworksConfigModel>("/networks/config");
    return data;
  },
  /** Docker networks the panel did not create (read-only). */
  listDocker: async () => {
    const { data } =
      await AxiosInstance.get<DockerNetworkModel[]>("/networks/docker");
    return data;
  },
  getDocker: async (id: string) => {
    const { data } = await AxiosInstance.get<DockerNetworkDetailModel>(
      `/networks/docker/${encodeURIComponent(id)}`,
    );
    return data;
  },
  get: async (name: string) => {
    const { data } = await AxiosInstance.get<NetworkDetailModel>(
      networkPath(name),
    );
    return data;
  },
  create: async (payload: CreateNetworkPayload) => {
    const { data } = await AxiosInstance.post<NetworkModel>(
      "/networks",
      payload,
    );
    return data;
  },
  /** Docker cannot change the subnet: only the description. */
  update: async ({
    name,
    description,
  }: {
    name: string;
    description: string;
  }) => {
    const { data } = await AxiosInstance.patch<NetworkModel>(
      networkPath(name),
      { description },
    );
    return data;
  },
  delete: async (name: string) => {
    await AxiosInstance.delete(networkPath(name));
  },
  /** The member's container is recreated (a site asynchronously). */
  attach: async ({
    network,
    kind,
    name,
  }: {
    network: string;
    kind: AttachableKind;
    name: string;
  }) => {
    const { data } = await AxiosInstance.post<MemberOperationResponse>(
      `${networkPath(network)}/members`,
      { kind, name },
    );
    return data ?? {};
  },
  detach: async ({
    network,
    kind,
    name,
  }: {
    network: string;
    kind: AttachableKind;
    name: string;
  }) => {
    const { data } = await AxiosInstance.delete<MemberOperationResponse>(
      `${networkPath(network)}/members/${encodeURIComponent(kind)}/${encodeURIComponent(name)}`,
    );
    return data ?? {};
  },
  /** Unmanaged containers only (e.g. a CI stack). */
  disconnect: async ({
    network,
    containerId,
  }: {
    network: string;
    containerId: string;
  }) => {
    await AxiosInstance.post(`${networkPath(network)}/disconnect`, {
      containerId,
    });
  },
};
