import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import { memo, useEffect, useRef } from "react";
import { socket } from "../../Utils/socket";
import i18n from "../../i18n";

import "@xterm/xterm/css/xterm.css";

interface LogViewerProps {
  id: string;
  tail?: number;
}

const LogViewer = ({ id, tail = 100 }: Readonly<LogViewerProps>) => {
  const terminalRef = useRef<HTMLDivElement>(null);
  const startedRef = useRef(false);

  useEffect(() => {
    if (!terminalRef.current) return;

    const fitAddon = new FitAddon();

    const term = new Terminal({
      disableStdin: true,
      cursorBlink: false,
      cursorStyle: "bar",
      cursorInactiveStyle: "none",
      scrollback: 5000,
      convertEol: true,
    });

    term.loadAddon(fitAddon);
    term.open(terminalRef.current);
    fitAddon.fit();

    const resizeObserver = new ResizeObserver(() => {
      fitAddon.fit();
    });
    resizeObserver.observe(terminalRef.current);

    const handleLogsData = (data: string) => {
      term.write(data);
    };

    const handleLogsError = (error: string) => {
      term.writeln(`\r\n\x1b[31m[${i18n.t("containers:logViewer.error")}] ${error}\x1b[0m`);
    };

    const handleLogsEnd = (message: string) => {
      term.writeln(`\r\n\x1b[33m[${message}]\x1b[0m`);
    };

    socket.on("logsData", handleLogsData);
    socket.on("logsError", handleLogsError);
    socket.on("logsEnd", handleLogsEnd);

    if (!startedRef.current) {
      startedRef.current = true;
      socket.emit("logsStart", { id, tail });
    }

    return () => {
      socket.emit("logsStop");
      socket.off("logsData", handleLogsData);
      socket.off("logsError", handleLogsError);
      socket.off("logsEnd", handleLogsEnd);
      resizeObserver.disconnect();
      term.dispose();
      startedRef.current = false;
    };
  }, [id, tail]);

  return (
    <div
      ref={terminalRef}
      className="terminal"
      style={{ height: 400, overflow: "hidden" }}
    />
  );
};

export default memo(LogViewer);
