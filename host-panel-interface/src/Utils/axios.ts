import axios from "axios";
import { appConfig } from "../config";

const API_URL = appConfig.apiUrl;

export const AxiosInstance = axios.create({
  baseURL: API_URL,
  headers: {
    accept: "application/json",
  },
  withCredentials: true,
});

AxiosInstance.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      window.location.href = `${API_URL}/openid-connect/login`;
    }

    return Promise.reject(error);
  },
);
