export enum CronJobType {
  DOCKER_ACTION = "docker_action",
  DOCKER_EXEC = "docker_exec",
  HTTP = "http",
  FUNCTION_INVOKE = "function_invoke",
}

export enum CronJobStatus {
  PENDING = "pending",
  RUNNING = "running",
  SUCCESS = "success",
  ERROR = "error",
}

export interface DockerActionConfig {
  containerName: string;
  action: "start" | "stop" | "restart" | "kill";
}

export interface DockerExecConfig {
  containerName: string;
  command: string[];
  workingDir?: string;
}

export interface HTTPConfig {
  method: string;
  url: string;
  headers?: Record<string, string>;
  body?: string;
  timeout?: number;
}

/** Invokes a function of the functions module (admins only). */
export interface FunctionInvokeConfig {
  /** function name */
  function: string;
  /** the event, as JSON text (default "{}", at most 64 KiB) */
  event: string;
}

/** Bounds of a function_invoke job (same as the API). */
export const MAX_FUNCTION_EVENT_BYTES = 64 * 1024;
export const MAX_JOBS_PER_FUNCTION = 5;

/** Why the API disabled a job by itself. */
export type CronDisabledReason = "FUNCTION_DELETED";

/**
 * `lastOutput` of a function_invoke job: a summary of the envelope (never
 * the payload), as JSON text.
 */
export interface FunctionInvokeOutput {
  statusCode: number;
  functionError?: string;
  durationMs: number;
  payloadBytes: number;
}

export interface CronJobModel {
  id: string;
  name: string;
  schedule: string;
  type: CronJobType;
  enabled: boolean;
  once: boolean;
  dockerAction?: DockerActionConfig;
  dockerExec?: DockerExecConfig;
  http?: HTTPConfig;
  functionInvoke?: FunctionInvokeConfig;
  /** set by the API when it disabled the job (e.g. its function was deleted) */
  disabledReason?: CronDisabledReason | string;
  /** last run skipped because the previous one was still running */
  lastSkipped?: string;
  lastRun?: string;
  lastStatus: CronJobStatus;
  lastError?: string;
  lastOutput?: string;
  createdAt: string;
  updatedAt: string;
}

export type CronJobCreatePayload = Omit<
  CronJobModel,
  | "id"
  | "lastRun"
  | "lastStatus"
  | "lastError"
  | "lastOutput"
  | "disabledReason"
  | "lastSkipped"
  | "createdAt"
  | "updatedAt"
>;

export type CronJobPatchPayload = Partial<CronJobCreatePayload>;

/** The summary of a function_invoke run (null when it is not one). */
export function parseFunctionInvokeOutput(
  output: string | undefined,
): FunctionInvokeOutput | null {
  if (!output) return null;
  try {
    const v: unknown = JSON.parse(output);
    if (
      v &&
      typeof v === "object" &&
      typeof (v as FunctionInvokeOutput).statusCode === "number" &&
      typeof (v as FunctionInvokeOutput).durationMs === "number"
    ) {
      const o = v as FunctionInvokeOutput;
      return {
        statusCode: o.statusCode,
        durationMs: o.durationMs,
        payloadBytes: typeof o.payloadBytes === "number" ? o.payloadBytes : 0,
        functionError:
          typeof o.functionError === "string" ? o.functionError : undefined,
      };
    }
  } catch {
    // not a summary
  }
  return null;
}
