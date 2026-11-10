export interface ApiKeyModel {
  id: string;
  name: string;
  keyPrefix: string;
  scopes?: string[];
  expiresAt: string;
  createdAt: string;
  lastUsedAt?: string;
}

export interface CreateApiKeyRequest {
  name: string;
  expiresInDays: number;
  scopes: string[];
}

export interface CreateApiKeyResponse {
  id: string;
  name: string;
  token: string;
  scopes: string[];
  expiresAt: string;
}

/** Scope identifiers accepted by the API (labels: see `apiKeyScopeLabel`). */
export const API_KEY_SCOPES = [
  "*",
  "containers:read",
  "containers:logs",
  "containers:actions",
  "containers:shell",
  "containers:stats",
  "containers:proxy",
  "containers:cp",
  "cron",
  "sftp:read",
  "sftp:write",
  "hosting:read",
  "hosting:write",
  "networks:read",
  "networks:write",
  "services:read",
  "services:write",
  "services:expose",
  "audit:read",
] as const;

export type ApiKeyScope = (typeof API_KEY_SCOPES)[number];
