import { useEffect, useRef } from "react";
import { useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { JsonWebSocket, WS_BASE } from "../../Utils/socket";
import TerminalInterface from "../../Components/Terminal/TerminalInterface";

import "@xterm/xterm/css/xterm.css";
import { appConfig } from "../../config";

export default function PopupTerminal() {
  const { id } = useParams<{ id: string }>();
  const wsRef = useRef<JsonWebSocket | null>(null);
  const { t } = useTranslation("containers");

  const [searchParams] = useSearchParams();
  const containerName = searchParams.get("name");

  // Create the socket once (survives Strict Mode double-invocation)
  if (!wsRef.current) {
    wsRef.current = new JsonWebSocket(`${WS_BASE}/ws`);
  }

  // Read and consume history from sessionStorage
  const historyKey = id ? `terminal-history-${id}` : null;
  const initialContent = historyKey
    ? (sessionStorage.getItem(historyKey) ?? undefined)
    : undefined;

  useEffect(() => {
    document.title = containerName
      ? t("popupTerminal.windowTitleWithName", {
          appName: appConfig.appName,
          name: containerName,
        })
      : t("popupTerminal.windowTitle", { appName: appConfig.appName });

    // Clean up history after reading it
    if (historyKey) sessionStorage.removeItem(historyKey);

    const ws = wsRef.current;
    const handleUnload = () => ws?.close();
    window.addEventListener("beforeunload", handleUnload);
    return () => {
      window.removeEventListener("beforeunload", handleUnload);
    };
  }, [historyKey, containerName, t]);

  if (!id) {
    return <p style={{ color: "#ccc", padding: 16 }}>{t("popupTerminal.missingId")}</p>;
  }

  return (
    <div
      style={{
        width: "100vw",
        height: "100vh",
        backgroundColor: "#000",
        overflow: "hidden",
      }}
    >
      <TerminalInterface
        id={id}
        socket={wsRef.current}
        initialContent={initialContent}
      />
    </div>
  );
}
