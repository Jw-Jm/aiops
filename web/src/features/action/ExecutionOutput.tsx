import { useEffect, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { Alert } from "antd";
import { boundedText } from "../../components/data";
export function terminalSafe(value: string) {
  return value
    .replace(/\x1b\][^\x07]*(?:\x07|\x1b\\)/g, "")
    .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "")
    .replace(/[\x00-\x08\x0b-\x1f\x7f]/g, "");
}
export function ExecutionOutput({
  output,
  truncated,
}: {
  output: string;
  truncated: boolean;
}) {
  const container = useRef<HTMLDivElement>(null);
  const terminal = useRef<Terminal | undefined>(undefined);
  useEffect(() => {
    if (!container.current) return;
    const t = new Terminal({
      disableStdin: true,
      convertEol: true,
      scrollback: 1000,
      fontSize: 14,
      theme: { background: "#172a3a", foreground: "#e4edf3" },
      allowProposedApi: false,
    });
    const fit = new FitAddon();
    t.loadAddon(fit);
    t.open(container.current);
    fit.fit();
    terminal.current = t;
    const resize = new ResizeObserver(() => fit.fit());
    resize.observe(container.current);
    return () => {
      resize.disconnect();
      t.dispose();
      terminal.current = undefined;
    };
  }, []);
  useEffect(() => {
    terminal.current?.reset();
    terminal.current?.write(terminalSafe(boundedText(output)));
  }, [output]);
  return (
    <>
      {truncated && (
        <Alert
          showIcon
          type="warning"
          title="输出已截断"
          description="浏览器缓冲或后端输出预算已到上限，完整程度以执行记录和归档说明为准。"
        />
      )}
      <div
        ref={container}
        className="execution-terminal"
        aria-label="只读 stdout / stderr 输出"
      />
      <details>
        <summary>无终端依赖的文本输出</summary>
        <pre className="json-view">
          {terminalSafe(boundedText(output)) || "尚无正式输出"}
        </pre>
      </details>
    </>
  );
}
