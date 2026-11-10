import { AxiosInstance } from "../../Utils/axios";
import {
  CronJobCreatePayload,
  CronJobModel,
  CronJobPatchPayload,
} from "./Models/cron.model";

export const cronServiceKeys = {
  jobs: "cron-jobs",
};

export const cronService = {
  list: async () => {
    const { data } = await AxiosInstance.get<CronJobModel[]>("/cron");
    return data;
  },
  get: async (id: string) => {
    const { data } = await AxiosInstance.get<CronJobModel>(`/cron/${id}`);
    return data;
  },
  create: async (payload: CronJobCreatePayload) => {
    const { data } = await AxiosInstance.post<CronJobModel>("/cron", payload);
    return data;
  },
  update: async ({ id, ...payload }: CronJobCreatePayload & { id: string }) => {
    const { data } = await AxiosInstance.put<CronJobModel>(
      `/cron/${id}`,
      payload,
    );
    return data;
  },
  delete: async (id: string) => {
    return AxiosInstance.delete(`/cron/${id}`);
  },
  patch: async (id: string, payload: CronJobPatchPayload) => {
    const { data } = await AxiosInstance.patch<CronJobModel>(
      `/cron/${id}`,
      payload,
    );
    return data;
  },
  run: async (id: string) => {
    const { data } = await AxiosInstance.post(`/cron/${id}/run`);
    return data;
  },
};
