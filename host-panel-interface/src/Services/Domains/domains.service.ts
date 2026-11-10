import { AxiosInstance } from "../../Utils/axios";
import { DomainEntry } from "./Models/domains.model";

export const domainsServiceKeys = {
  domains: "domains",
};

export const domainsService = {
  list: async () => {
    const { data } = await AxiosInstance.get<DomainEntry[]>("/domains");
    return data;
  },
};
