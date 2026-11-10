import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App.tsx";
import "./index.css";
import "./i18n";
import { appConfig } from "./config";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router";

// Branding from the runtime configuration
document.title = appConfig.appName;
document
  .querySelector<HTMLLinkElement>("link[rel='icon']")
  ?.setAttribute("href", appConfig.faviconUrl);

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 4000,
      gcTime: 10 * 60 * 1000,
      refetchOnWindowFocus: false,
      retry: 1,
    },
  },
});

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <BrowserRouter>
      <QueryClientProvider client={queryClient}>
        <App />
      </QueryClientProvider>
    </BrowserRouter>
  </React.StrictMode>
);
