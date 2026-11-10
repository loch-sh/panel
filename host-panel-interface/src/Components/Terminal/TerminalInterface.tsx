import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import {
  forwardRef,
  memo,
  useEffect,
  useImperativeHandle,
  useRef,
} from "react";
import { socket as defaultSocket, type JsonWebSocket } from "../../Utils/socket";

import "@xterm/xterm/css/xterm.css";

interface TerminalProps {
  id: string;
  socket?: JsonWebSocket;
  initialContent?: string;
}

export interface TerminalHandle {
  getBufferContent: () => string;
}

const TerminalInterface = forwardRef<TerminalHandle, TerminalProps>(
  ({ id, socket, initialContent }, ref) => {
    const terminalRef = useRef<HTMLDivElement>(null);
    const termRef = useRef<Terminal | null>(null);
    const ws = socket ?? defaultSocket;

    useImperativeHandle(ref, () => ({
      getBufferContent: () => {
        const term = termRef.current;
        if (!term) return "";
        const buf = term.buffer.active;
        const lines: string[] = [];
        for (let i = 0; i < buf.length; i++) {
          const line = buf.getLine(i);
          if (line) lines.push(line.translateToString(true));
        }
        // Trim trailing empty lines
        while (lines.length > 0 && lines[lines.length - 1] === "") {
          lines.pop();
        }
        return lines.join("\r\n");
      },
    }));

    useEffect(() => {
      if (!terminalRef.current) return;

      const term = new Terminal({
        cursorBlink: true,
        scrollback: 1000,
      });
      termRef.current = term;

      const fitAddon = new FitAddon();
      term.loadAddon(fitAddon);
      term.open(terminalRef.current);
      fitAddon.fit();

      // Restore previous content if provided
      if (initialContent) {
        term.write(initialContent);
        term.write("\r\n");
      }

      const sendResize = () => {
        ws.emit("resize", { cols: term.cols, rows: term.rows });
      };

      ws.emit("attach", { id });

      // Send initial size once the server confirms attachment
      let attachHandled = false;
      const onAttach = () => {
        if (!attachHandled) {
          attachHandled = true;
          sendResize();
          ws.off("attach", onAttach);
        }
      };
      ws.on("attach", onAttach);

      const onStdout = (data: string) => {
        term.write(data);
      };
      const onStderr = (data: string) => {
        term.write(data);
      };

      ws.on("stdout", onStdout);
      ws.on("stderr", onStderr);

      const dataDisposable = term.onData((input) => {
        ws.emit("streamInput", input);
      });

      // Handle resize
      const resizeObserver = new ResizeObserver(() => {
        fitAddon.fit();
        sendResize();
      });
      resizeObserver.observe(terminalRef.current);

      return () => {
        resizeObserver.disconnect();
        dataDisposable.dispose();
        ws.emit("streamClose");
        ws.off("stdout", onStdout);
        ws.off("stderr", onStderr);
        ws.off("attach", onAttach);
        termRef.current = null;
        term.dispose();
      };
    }, [id, ws, initialContent]);

    return (
      <div
        ref={terminalRef}
        className="terminal"
        style={{ height: "100%", minHeight: 300, overflow: "hidden" }}
      />
    );
  }
);

export default memo(TerminalInterface);
