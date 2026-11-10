import { AxiosInstance } from "../../Utils/axios";
import { MyUserModel, UserActionEnum, UserModel } from "./Models/users.model";

export const UsersServiceKeys = {
  me: "users-me",
  users: "users",
};

const getUserBase = (email: string) => `/users/${email}`;

export const UsersService = {
  me: async () => {
    const { data } = await AxiosInstance.get<MyUserModel>(`/users/me`);
    return data;
  },
  list: async () => {
    const { data } = await AxiosInstance.get<UserModel[]>(`/users`);
    return data;
  },
  deleteUser: async ({ email }: { email: string }) => {
    return AxiosInstance.delete(getUserBase(email));
  },
  doAction: async ({
    email,
    action,
  }: {
    email: string;
    action: UserActionEnum;
  }) => {
    return AxiosInstance.patch(getUserBase(email) + "/" + action);
  },
  updateACL: async ({
    email,
    permissions,
  }: {
    email: string;
    permissions: string[];
  }) => {
    return AxiosInstance.patch(getUserBase(email), {
      permissions,
    });
  },
};
