import { appConfig } from "../config";

export const WS_BASE = appConfig.wsUrl;

type EventHandler = (data: string) => void;

/**
 * Wrapper WebSocket natif avec une API compatible emit/on/off.
 * Protocole JSON : { "event": "...", "data": ... }
 */
export class JsonWebSocket {
  private ws: WebSocket | null = null;
  private listeners = new Map<string, Set<EventHandler>>();
  private url: string;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private shouldReconnect = true;
  private pendingQueue: Array<{ event: string; data: unknown }> = [];
  private reconnectAttempt = 0;

  constructor(url: string) {
    this.url = url;
    this.connect();
  }

  private connect() {
    this.ws = new WebSocket(this.url);

    this.ws.addEventListener("open", () => {
      this.reconnectAttempt = 0;
      for (const msg of this.pendingQueue) {
        this.ws!.send(JSON.stringify(msg));
      }
      this.pendingQueue = [];
    });

    this.ws.addEventListener("message", (event) => {
      try {
        const msg = JSON.parse(event.data) as {
          event: string;
          data: unknown;
        };
        const handlers = this.listeners.get(msg.event);
        if (handlers) {
          for (const handler of handlers) {
            handler(msg.data as string);
          }
        }
      } catch {
        // Ignorer les messages non-JSON
      }
    });

    this.ws.addEventListener("close", () => {
      if (this.shouldReconnect) {
        const delay = Math.min(1000 * 2 ** this.reconnectAttempt, 30000);
        this.reconnectAttempt++;
        this.reconnectTimer = setTimeout(() => this.connect(), delay);
      }
    });

    // Let the browser handle the close naturally — the close handler drives reconnect
    this.ws.addEventListener("error", () => {});
  }

  emit(event: string, data?: unknown) {
    const msg = { event, data: data ?? null };
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
    } else {
      this.pendingQueue.push(msg);
    }
  }

  on(event: string, handler: EventHandler) {
    const set = this.listeners.get(event) ?? new Set<EventHandler>();
    this.listeners.set(event, set);
    set.add(handler);
  }

  off(event: string, handler?: EventHandler) {
    if (!handler) {
      this.listeners.delete(event);
      return;
    }
    this.listeners.get(event)?.delete(handler);
  }

  close() {
    this.shouldReconnect = false;
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.pendingQueue = [];
    this.ws?.close();
  }
}

export const socket = new JsonWebSocket(`${WS_BASE}/ws`);
