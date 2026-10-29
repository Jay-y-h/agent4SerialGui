import { useCallback, useEffect, useRef, useState } from "react";
import {
  ClearLogs,
  GetLogs,
  GetModes,
  GetState,
  SelectMode,
  StartAgent,
  StopAgent,
} from "../wailsjs/go/main/App";
import { EventsOn } from "../wailsjs/runtime/runtime";

type Mode = {
  id: string;
  name: string;
  description: string;
};

type DeviceState = {
  id: string;
  name: string;
  port: string;
  state: string; // open | closed
};

type AppState = {
  activeMode: string;
  status: string; // stopped | running | error
  error: string;
  agentId: string;
  gateway: string;
  gatewayState: string; // connected | disconnected
  devices: DeviceState[];
};

type LogLine = { id: number; text: string };

const MAX_LOGS = 500;

function Dot({ on }: { on: boolean }) {
  return (
    <span
      className={`inline-block h-2 w-2 shrink-0 rounded-full ${
        on ? "bg-emerald-500" : "bg-slate-300"
      }`}
    />
  );
}

export default function App() {
  const [modes, setModes] = useState<Mode[]>([]);
  const [state, setState] = useState<AppState | null>(null);
  const [logs, setLogs] = useState<LogLine[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const nextId = useRef(0);
  const logBox = useRef<HTMLDivElement>(null);
  const stickToBottom = useRef(true);

  const toLines = useCallback(
    (texts: string[]): LogLine[] =>
      texts.map((text) => ({ id: nextId.current++, text })),
    [],
  );

  // 首次加载：模式、状态、已有日志；之后全部靠后端事件推送，不再轮询。
  useEffect(() => {
    let active = true;

    const offState = EventsOn("agent:state", (s: AppState) => {
      if (active) setState(s);
    });
    const offLog = EventsOn("agent:log", (line: string) => {
      if (!active) return;
      setLogs((prev) => [...prev, ...toLines([line])].slice(-MAX_LOGS));
    });

    Promise.all([GetModes(), GetState(), GetLogs()])
      .then(([list, current, history]) => {
        if (!active) return;
        setModes(list);
        setState(current);
        // 事件可能先于 GetLogs 返回：以历史为底，保留已收到的新行。
        setLogs((prev) =>
          prev.length > 0 ? prev : toLines(history).slice(-MAX_LOGS),
        );
      })
      .catch((e) => active && setError(String(e)));

    return () => {
      active = false;
      offState();
      offLog();
    };
  }, [toLines]);

  // 用户停在底部时才自动滚动，向上翻看历史时不打扰。
  useEffect(() => {
    const el = logBox.current;
    if (el && stickToBottom.current) el.scrollTop = el.scrollHeight;
  }, [logs]);

  const onScroll = () => {
    const el = logBox.current;
    if (el)
      stickToBottom.current =
        el.scrollHeight - el.scrollTop - el.clientHeight < 24;
  };

  const action = async (fn: () => Promise<AppState>) => {
    setBusy(true);
    setError("");
    try {
      setState(await fn());
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  const clearLogs = async () => {
    await ClearLogs(); // 先清后端缓冲，否则刷新后又会回来
    setLogs([]);
  };

  if (!state) {
    return (
      <main className="p-3 text-xs text-slate-500">{error || "加载中..."}</main>
    );
  }

  const running = state.status === "running";
  const active = modes.find((m) => m.id === state.activeMode);
  const shownError = error || state.error;

  return (
    <main className="flex h-full min-h-0 w-full flex-col gap-2 overflow-hidden p-2 text-slate-700">
      {/* 模式选择（来自 configs/ 目录） */}
      <section className="flex shrink-0 flex-col gap-1">
        <div className="flex gap-1">
          {modes.map((mode) => {
            const selected = state.activeMode === mode.id;
            return (
              <button
                key={mode.id}
                title={mode.description}
                disabled={busy || running}
                onClick={() => action(() => SelectMode(mode.id))}
                className={`flex h-10 min-w-0 flex-1 items-center justify-center rounded-md border text-xs transition-colors disabled:cursor-not-allowed ${
                  selected
                    ? "border-blue-500 bg-blue-50 text-blue-700"
                    : "border-slate-200 bg-white text-slate-500 hover:bg-slate-50"
                }`}
              >
                <span className="max-w-full truncate px-1">{mode.name}</span>
              </button>
            );
          })}
        </div>
        {active && (
          <div className="truncate px-1 text-[11px] text-slate-400">
            {active.description}
          </div>
        )}
      </section>

      {/* 连接状态：网关 + 每个串口设备 */}
      <section className="shrink-0 rounded-md border border-slate-200 px-2 py-1.5 text-[11px] leading-5">
        <div className="flex items-center gap-1.5">
          <Dot on={running && state.gatewayState === "connected"} />
          <span className="font-medium">网关</span>
          <span className="truncate text-slate-400">
            {!running
              ? "未运行"
              : state.gatewayState === "connected"
                ? "已连接"
                : "连接中…"}
          </span>
        </div>
        {state.devices.map((d) => (
          <div key={d.id} className="flex items-center gap-1.5">
            <Dot on={running && d.state === "open"} />
            <span className="truncate">{d.name || d.id}</span>
            <span className="shrink-0 text-slate-400">
              {d.port} ·{" "}
              {!running ? "未运行" : d.state === "open" ? "已打开" : "等待设备…"}
            </span>
          </div>
        ))}
      </section>

      {/* 日志区域 */}
      <section className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-md border border-slate-200">
        <div className="flex shrink-0 items-center justify-between border-b border-slate-100 px-2 py-1.5">
          <span className="text-xs font-medium">运行日志</span>
          <button
            className="text-xs text-slate-400 hover:text-slate-700"
            onClick={clearLogs}
          >
            清空
          </button>
        </div>

        <div
          ref={logBox}
          onScroll={onScroll}
          className="min-h-0 flex-1 overflow-y-auto px-2 py-1.5 font-mono text-[11px] leading-5"
        >
          {logs.map((l) => (
            <div key={l.id} className="break-all whitespace-pre-wrap">
              {l.text}
            </div>
          ))}

          {logs.length === 0 && (
            <div className="text-slate-300">暂无日志</div>
          )}
        </div>
      </section>

      {/* 错误提示 */}
      {shownError && (
        <div className="shrink-0 break-all text-xs text-red-500">
          {shownError}
        </div>
      )}

      {/* 底部启动 / 停止 */}
      <section className="grid shrink-0 grid-cols-2 gap-2">
        <button
          disabled={busy || running}
          onClick={() => action(StartAgent)}
          className="h-9 rounded-md bg-blue-600 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40"
        >
          {busy && !running ? "请稍候..." : "▶ 启动"}
        </button>

        <button
          disabled={busy || !running}
          onClick={() => action(StopAgent)}
          className="h-9 rounded-md border border-slate-200 bg-white text-sm hover:bg-slate-50 disabled:opacity-40"
        >
          {busy && running ? "停止中..." : "■ 停止"}
        </button>
      </section>
    </main>
  );
}
