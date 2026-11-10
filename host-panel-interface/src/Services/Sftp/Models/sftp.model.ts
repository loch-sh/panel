export interface SftpConfigModel {
  enabled: boolean;
  directory?: string;
  domain?: string;
  port?: string;
  webUserInterface?: string;
  /** directories proposed for initialization in a new user's home */
  initDirs?: string[];
}

export interface SftpUserModel {
  username: string;
  email?: string;
  site: string;
  /** path within the site the user is confined to; empty = whole site */
  subPath: string;
  homeDir: string;
  /** 1 = enabled, 0 = disabled */
  status: number;
  /** bytes, 0 = unlimited */
  quotaSize: number;
  quotaFiles: number;
  /** KB/s, 0 = unlimited */
  uploadBandwidth: number;
  downloadBandwidth: number;
  usedQuotaSize: number;
  usedQuotaFiles: number;
  requirePasswordChange: boolean;
  /** milliseconds since epoch, 0 = never */
  lastLogin: number;
  createdAt: number;
  publicKeys: string[];
  /** set on creation only: directories created and a non-fatal init error */
  initializedDirs?: string[];
  initDirsError?: string;
}

export interface SftpSiteModel {
  name: string;
  homeDir: string;
  userCount: number;
  totalUsedQuotaSize: number;
}

export interface SftpUserCreatePayload {
  username: string;
  site: string;
  /** optional path within the site; empty = whole site */
  subPath?: string;
  /** create the configured home directories (e.g. web, logs); default true */
  initializeDirs?: boolean;
  password: string;
  publicKey?: string;
  quotaSizeBytes?: number;
  quotaFiles?: number;
  bandwidthKbps?: number;
  requirePasswordChange: boolean;
}

export interface SftpUserUpdatePayload {
  publicKey?: string;
  quotaSizeBytes?: number;
  quotaFiles?: number;
  bandwidthKbps?: number;
  requirePasswordChange: boolean;
}
