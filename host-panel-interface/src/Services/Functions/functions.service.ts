import { AxiosInstance } from "../../Utils/axios";
import {
  CreatedFunctionModel,
  CreateFunctionPayload,
  DeleteFunctionResultModel,
  DeploymentModel,
  FunctionAction,
  FunctionCredentialsModel,
  FunctionDetailModel,
  FunctionsConfigModel,
  FunctionSummaryModel,
  InvokeResultModel,
  UpdateFunctionPayload,
} from "./Models/functions.model";

export const functionsServiceKeys = {
  config: "functions-config",
  functions: "functions",
  function: "function",
  deployments: "function-deployments",
};

const functionPath = (name: string) => `/functions/${encodeURIComponent(name)}`;

/** Sends a body as is (axios would re-encode a string as JSON). */
const raw = [(data: unknown) => data];

export const functionsService = {
  config: async () => {
    const { data } =
      await AxiosInstance.get<FunctionsConfigModel>("/functions/config");
    return data;
  },
  list: async () => {
    const { data } =
      await AxiosInstance.get<FunctionSummaryModel[]>("/functions");
    return data;
  },
  get: async (name: string) => {
    const { data } = await AxiosInstance.get<FunctionDetailModel>(
      functionPath(name),
    );
    return data;
  },
  /** The invoke credentials are in the response, shown once. */
  create: async (payload: CreateFunctionPayload) => {
    const { data } = await AxiosInstance.post<CreatedFunctionModel>(
      "/functions",
      payload,
    );
    return data;
  },
  update: async ({
    name,
    ...payload
  }: UpdateFunctionPayload & { name: string }) => {
    const { data } = await AxiosInstance.patch<FunctionDetailModel>(
      functionPath(name),
      payload,
    );
    return data;
  },
  /** purge: a function edited outside the panel, none of its files run. */
  delete: async ({ name, purge }: { name: string; purge?: boolean }) => {
    const { data } = await AxiosInstance.delete<DeleteFunctionResultModel>(
      functionPath(name),
      purge ? { params: { purge: true } } : undefined,
    );
    return data;
  },
  action: async ({
    name,
    action,
  }: {
    name: string;
    action: FunctionAction;
  }) => {
    await AxiosInstance.post(`${functionPath(name)}/${action}`);
  },
  /**
   * Deploys a zip (streamed by the server, activated at once). The upload is
   * sent as is (`application/zip`), with its progress.
   */
  deploy: async ({
    name,
    file,
    onProgress,
    signal,
  }: {
    name: string;
    file: Blob;
    /** 0 to 100 */
    onProgress?: (percent: number) => void;
    signal?: AbortSignal;
  }) => {
    const { data } = await AxiosInstance.post<DeploymentModel>(
      `${functionPath(name)}/code`,
      file,
      {
        headers: { "Content-Type": "application/zip" },
        transformRequest: raw,
        signal,
        onUploadProgress: (e) => {
          const total = e.total ?? file.size;
          if (onProgress && total > 0) {
            onProgress(Math.min(100, Math.round((e.loaded / total) * 100)));
          }
        },
      },
    );
    return data;
  },
  deployments: async (name: string) => {
    const { data } = await AxiosInstance.get<DeploymentModel[]>(
      `${functionPath(name)}/deployments`,
    );
    return data;
  },
  activate: async ({ name, id }: { name: string; id: string }) => {
    await AxiosInstance.post(
      `${functionPath(name)}/deployments/${encodeURIComponent(id)}/activate`,
    );
  },
  /** The value is never returned: only the names can be read back. */
  setSecret: async ({
    name,
    key,
    value,
  }: {
    name: string;
    key: string;
    value: string;
  }) => {
    await AxiosInstance.put(
      `${functionPath(name)}/secrets/${encodeURIComponent(key)}`,
      { value },
    );
  },
  deleteSecret: async ({ name, key }: { name: string; key: string }) => {
    await AxiosInstance.delete(
      `${functionPath(name)}/secrets/${encodeURIComponent(key)}`,
    );
  },
  /** New credentials, returned once; sigv4 enables or disables the key pair. */
  rotateCredentials: async ({
    name,
    sigv4,
  }: {
    name: string;
    sigv4?: boolean;
  }) => {
    const { data } = await AxiosInstance.post<FunctionCredentialsModel>(
      `${functionPath(name)}/credentials/rotate`,
      sigv4 === undefined ? undefined : { sigv4 },
    );
    return data;
  },
  /**
   * Invokes with `event` (JSON text) as the body. Synchronous: the server
   * bounds it by the function timeout.
   */
  invoke: async ({ name, event }: { name: string; event: string }) => {
    const { data } = await AxiosInstance.post<InvokeResultModel>(
      `${functionPath(name)}/invoke`,
      event,
      {
        headers: { "Content-Type": "application/json" },
        transformRequest: raw,
        // The envelope is JSON whatever the function answered.
        responseType: "json",
      },
    );
    return data;
  },
  setNetworks: async ({
    name,
    networks,
  }: {
    name: string;
    networks: string[];
  }) => {
    const { data } = await AxiosInstance.put<{ networks: string[] }>(
      `${functionPath(name)}/networks`,
      { networks },
    );
    return data.networks;
  },
  setEgress: async ({ name, enabled }: { name: string; enabled: boolean }) => {
    const { data } = await AxiosInstance.put<{ egress: boolean }>(
      `${functionPath(name)}/egress`,
      { enabled },
    );
    return data.egress;
  },
};
