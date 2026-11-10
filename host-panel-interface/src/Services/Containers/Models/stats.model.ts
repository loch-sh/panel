export interface ContainerStats {
  id: string;
  name: string;
  cpuPct: number;
  memUsage: number;
  memLimit: number;
  memPct: number;
  netRx: number;
  netTx: number;
  pids: number;
}
