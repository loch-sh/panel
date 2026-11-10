import { AxiosInstance } from "../../Utils/axios";
import {
  CatalogStatusModel,
  CreatedResourceModel,
  CreateResourcePayload,
  CredentialModel,
  DeleteResultModel,
  ExposurePayload,
  InstanceUpgradePayload,
  InstanceUpgradeResultModel,
  ServiceInstanceModel,
  ServiceResourceModel,
  ServicesConfigModel,
} from "./Models/databases.model";

export const databasesServiceKeys = {
  config: "services-config",
  instances: "services-instances",
  resources: "services-resources",
};

/** Longer than the server bound of an update (pull + restart, ~9 min). */
const UPGRADE_TIMEOUT_MS = 10 * 60_000;

const path = (segment: string) => encodeURIComponent(segment);

export const databasesService = {
  config: async () => {
    const { data } =
      await AxiosInstance.get<ServicesConfigModel>("/services/config");
    return data;
  },
  instances: async () => {
    const { data } = await AxiosInstance.get<ServiceInstanceModel[]>(
      "/services/instances",
    );
    return data;
  },
  resources: async (instance: string) => {
    const { data } = await AxiosInstance.get<ServiceResourceModel[]>(
      `/services/instances/${path(instance)}/resources`,
    );
    return data;
  },
  create: async (payload: CreateResourcePayload) => {
    const { data } = await AxiosInstance.post<CreatedResourceModel>(
      "/services/resources",
      payload,
    );
    return data;
  },
  rotatePassword: async ({
    instance,
    name,
  }: {
    instance: string;
    name: string;
  }) => {
    const { data } = await AxiosInstance.post<{
      credentials: CredentialModel[];
    }>(
      `/services/instances/${path(instance)}/resources/${path(name)}/password`,
    );
    return data.credentials;
  },
  delete: async ({ instance, name }: { instance: string; name: string }) => {
    const { data } = await AxiosInstance.delete<DeleteResultModel>(
      `/services/instances/${path(instance)}/resources/${path(name)}`,
    );
    return data;
  },
  update: async ({
    instance,
    options,
  }: {
    instance: string;
    options: Record<string, string>;
  }) => {
    const { data } = await AxiosInstance.patch<{
      options: Record<string, string>;
    }>(`/services/instances/${path(instance)}`, { options });
    return data.options;
  },
  /** Deletes an instance edited by hand, with its data (never runs its files). */
  purge: async (instance: string) => {
    await AxiosInstance.delete(`/services/instances/${path(instance)}`, {
      params: { purge: true },
    });
  },
  retire: async (instance: string) => {
    await AxiosInstance.delete(`/services/instances/${path(instance)}`);
  },
  /** Replaces the networks of an instance (it is recreated on them). */
  updateNetworks: async ({
    instance,
    networks,
  }: {
    instance: string;
    networks: string[];
  }) => {
    const { data } = await AxiosInstance.put<{ networks: string[] }>(
      `/services/instances/${path(instance)}/networks`,
      { networks },
    );
    return data.networks;
  },
  /** Publishes the port of an instance (applied right away). */
  setExposure: async ({
    instance,
    exposure,
  }: {
    instance: string;
    exposure: ExposurePayload;
  }) => {
    const { data } = await AxiosInstance.put<ServiceInstanceModel>(
      `/services/instances/${path(instance)}/exposure`,
      exposure,
    );
    return data;
  },
  /** Closes the published port of an instance. */
  deleteExposure: async (instance: string) => {
    const { data } = await AxiosInstance.delete<ServiceInstanceModel>(
      `/services/instances/${path(instance)}/exposure`,
    );
    return data;
  },
  /**
   * Updates a dedicated instance to the latest image of its version, or to
   * a newer version. Synchronous: the server bounds the pull (4 min) and the
   * restart (5 min).
   */
  upgrade: async ({
    instance,
    version,
  }: {
    instance: string;
  } & InstanceUpgradePayload) => {
    const body: InstanceUpgradePayload = version ? { version } : {};
    const { data } = await AxiosInstance.post<InstanceUpgradeResultModel>(
      `/services/instances/${path(instance)}/update`,
      body,
      { timeout: UPGRADE_TIMEOUT_MS },
    );
    return data;
  },
  /** Discovers the available versions now (throttled by the server). */
  checkUpdates: async () => {
    const { data } = await AxiosInstance.post<CatalogStatusModel>(
      "/services/updates/check",
    );
    return data;
  },
};
