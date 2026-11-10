import { ContainerInfo, ContainerInspectInfo } from "dockerode";
import { AxiosInstance } from "../../Utils/axios";
import {
  ContainerActionEnum,
  StackActionEnum,
  StackActionResponse,
} from "./Models/containers.model";

export const containersServiceKeys = {
  containers: "containers",
};

export const containersService = {
  list: async () => {
    const { data } = await AxiosInstance.get<ContainerInfo[]>("/docker");
    return data;
  },
  inspect: async (id: string) => {
    const { data } = await AxiosInstance.get<ContainerInspectInfo>(
      `/docker/${id}`,
    );
    return data;
  },
  action: async ({
    id,
    action,
  }: {
    id: string;
    action: ContainerActionEnum;
  }) => {
    const { data } = await AxiosInstance.post(`/docker/${id}/${action}`);
    return data;
  },
  stackAction: async ({
    stack,
    action,
  }: {
    stack: string;
    action: StackActionEnum;
  }) => {
    const { data } = await AxiosInstance.post<StackActionResponse>(
      `/stacks/${encodeURIComponent(stack)}/${action}`,
    );
    return data;
  },
};
