import { AxiosInstance } from "../../Utils/axios";
import {
  CheckNameResponse,
  HostingConfigModel,
  HostingOperationResponse,
  HostingSiteModel,
  HostingStackDetail,
  HostingStackPayload,
  HostingStackSummary,
  HostingTemplate,
  Framework,
  PhpModulesResponse,
  RoutingTemplateResponse,
} from "./Models/hosting.model";

export const hostingServiceKeys = {
  config: "hosting-config",
  sites: "hosting-sites",
  stacks: "hosting-stacks",
  stack: "hosting-stack",
  phpModules: "hosting-php-modules",
};

const stackPath = (name: string) =>
  `/hosting/stacks/${encodeURIComponent(name)}`;

export const hostingService = {
  config: async () => {
    const { data } =
      await AxiosInstance.get<HostingConfigModel>("/hosting/config");
    return data;
  },
  listSites: async () => {
    const { data } =
      await AxiosInstance.get<HostingSiteModel[]>("/hosting/sites");
    return data;
  },
  phpModules: async (template: HostingTemplate, version: string) => {
    const { data } = await AxiosInstance.get<PhpModulesResponse>(
      "/hosting/php-modules",
      { params: { template, version } },
    );
    return data.modules ?? [];
  },
  /** Framework rules the server would generate, to prefill custom rules. */
  routingTemplate: async (params: {
    template: HostingTemplate;
    framework: Framework;
    documentRoot: string;
    assetDirs: string[];
    maxExecutionTime: number;
  }) => {
    const { data } = await AxiosInstance.get<RoutingTemplateResponse>(
      "/hosting/routing-template",
      { params: { ...params, assetDirs: params.assetDirs.join(",") } },
    );
    return data.rules ?? "";
  },
  listStacks: async () => {
    const { data } =
      await AxiosInstance.get<HostingStackSummary[]>("/hosting/stacks");
    return data;
  },
  get: async (name: string) => {
    const { data } = await AxiosInstance.get<HostingStackDetail>(
      stackPath(name),
    );
    return data;
  },
  checkName: async (name: string, signal?: AbortSignal) => {
    const { data } = await AxiosInstance.get<CheckNameResponse>(
      "/hosting/stacks/check-name",
      { params: { name }, signal },
    );
    return data;
  },
  create: async (payload: HostingStackPayload) => {
    const { data } = await AxiosInstance.post<HostingOperationResponse>(
      "/hosting/stacks",
      payload,
    );
    return data;
  },
  update: async ({
    payload,
    force,
  }: {
    payload: HostingStackPayload;
    force: boolean;
  }) => {
    const { data } = await AxiosInstance.put<HostingOperationResponse>(
      stackPath(payload.name),
      payload,
      { params: force ? { force: true } : undefined },
    );
    return data;
  },
  deploy: async ({
    name,
    pull,
    force,
  }: {
    name: string;
    pull: boolean;
    /** required when the compose file was edited outside the panel */
    force: boolean;
  }) => {
    const { data } = await AxiosInstance.post<HostingOperationResponse>(
      `${stackPath(name)}/deploy`,
      undefined,
      {
        params: {
          ...(pull ? { pull: true } : {}),
          ...(force ? { force: true } : {}),
        },
      },
    );
    return data;
  },
  delete: async ({ name, purgeData }: { name: string; purgeData: boolean }) => {
    const { data } = await AxiosInstance.delete<HostingOperationResponse>(
      stackPath(name),
      { params: purgeData ? { purgeData: true } : undefined },
    );
    return data;
  },
};
