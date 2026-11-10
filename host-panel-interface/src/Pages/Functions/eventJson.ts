/** Why an event is refused: one JSON value, within the size limit. */
export type EventProblem = "empty" | "invalid" | "tooLarge";

export function eventProblem(
  text: string,
  maxBytes: number,
): EventProblem | null {
  if (text.trim() === "") return "empty";
  try {
    JSON.parse(text);
  } catch {
    return "invalid";
  }
  if (new TextEncoder().encode(text).length > maxBytes) return "tooLarge";
  return null;
}

/** The event indented (the text itself when it is not JSON). */
export function formatEvent(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

/**
 * A response of the function for display: indented when it is JSON. Always
 * a string, rendered as text (never as HTML).
 */
export function displayPayload(payload: string): string {
  const trimmed = payload.trim();
  if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) return payload;
  return formatEvent(payload);
}

/** Bytes of a base64 payload. */
export function base64Size(b64: string): number {
  const padding = b64.endsWith("==") ? 2 : b64.endsWith("=") ? 1 : 0;
  return Math.max(0, Math.floor((b64.length * 3) / 4) - padding);
}
