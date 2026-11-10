import { AxiosInstance } from "../../Utils/axios";
import {
  ApiKeyModel,
  CreateApiKeyRequest,
  CreateApiKeyResponse,
} from "./Models/apikeys.model";

export const ApiKeysServiceKeys = {
  apiKeys: "api-keys",
};

export const ApiKeysService = {
  list: async () => {
    const { data } =
      await AxiosInstance.get<ApiKeyModel[]>(`/users/me/api-keys`);
    return data;
  },
  create: async (body: CreateApiKeyRequest) => {
    const { data } = await AxiosInstance.post<CreateApiKeyResponse>(
      `/users/me/api-keys`,
      body,
    );
    return data;
  },
  delete: async ({ id }: { id: string }) => {
    return AxiosInstance.delete(`/users/me/api-keys/${id}`);
  },
};
