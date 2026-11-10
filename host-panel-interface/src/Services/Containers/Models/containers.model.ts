export enum ContainerActionEnum {
  START = "start",
  STOP = "stop",
  RESTART = "restart",
  KILL = "kill",
  DELETE = "delete",
}

export enum StackActionEnum {
  START = "start",
  STOP = "stop",
  RESTART = "restart",
}

export interface StackActionResult {
  id: string;
  name: string;
  service: string;
  ok: boolean;
  error?: string;
}

export interface StackActionResponse {
  stack: string;
  action: StackActionEnum;
  results: StackActionResult[];
  failed: number;
}
