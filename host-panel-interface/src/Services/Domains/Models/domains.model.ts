export interface TraefikRouter {
  name: string;
  rule: string;
  domains: string[];
  entrypoints: string[];
  tls: boolean;
  certResolver?: string;
  middlewares?: string[];
  serviceName: string;
}

export interface TraefikService {
  name: string;
  port?: number;
}

export interface DomainEntry {
  containerId: string;
  containerName: string;
  containerState: string;
  containerImage: string;
  traefikEnabled: boolean;
  routers: TraefikRouter[];
  services: TraefikService[];
}
