import { AxiosInstance } from "../../Utils/axios";
import {
  SftpConfigModel,
  SftpSiteModel,
  SftpUserCreatePayload,
  SftpUserModel,
  SftpUserUpdatePayload,
} from "./Models/sftp.model";

export const sftpServiceKeys = {
  config: "sftp-config",
  sites: "sftp-sites",
  users: "sftp-users",
};

export const sftpService = {
  config: async () => {
    const { data } = await AxiosInstance.get<SftpConfigModel>("/sftp/config");
    return data;
  },
  listSites: async () => {
    const { data } = await AxiosInstance.get<SftpSiteModel[]>("/sftp/sites");
    return data;
  },
  listUsers: async () => {
    const { data } = await AxiosInstance.get<SftpUserModel[]>("/sftp/users");
    return data;
  },
  create: async (payload: SftpUserCreatePayload) => {
    const { data } = await AxiosInstance.post<SftpUserModel>(
      "/sftp/users",
      payload,
    );
    return data;
  },
  update: async ({
    username,
    ...payload
  }: SftpUserUpdatePayload & { username: string }) => {
    const { data } = await AxiosInstance.put<SftpUserModel>(
      `/sftp/users/${encodeURIComponent(username)}`,
      payload,
    );
    return data;
  },
  delete: async (username: string) => {
    return AxiosInstance.delete(`/sftp/users/${encodeURIComponent(username)}`);
  },
  resetPassword: async ({
    username,
    password,
  }: {
    username: string;
    password: string;
  }) => {
    return AxiosInstance.post(
      `/sftp/users/${encodeURIComponent(username)}/password`,
      { password },
    );
  },
  setEnabled: async ({
    username,
    enabled,
  }: {
    username: string;
    enabled: boolean;
  }) => {
    const { data } = await AxiosInstance.post<SftpUserModel>(
      `/sftp/users/${encodeURIComponent(username)}/status`,
      { enabled },
    );
    return data;
  },
};
