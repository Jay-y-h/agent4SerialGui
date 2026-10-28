import { useEffect, useState } from "react";
import {
  GetModes,
  GetState,
  SelectMode,
  StartAgent,
  StopAgent,
} from "../wailsjs/go/main/App";

type Mode = {
  id: string;
  name: string;
  description: string;
};

type AppState = {
  activeMode: string;
  status: string;
  agentId: string;
  gateway: string;
  device: string;
  logs: string[];
};

export default function App() {
  const [modes, setModes] = useState<Mode[]>([]);
  const [state, setState] = useState<AppState | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    Promise.all([GetModes(), GetState()])
      .then(([list, current]) => {
        setModes(list);
        setState(current);
      })
      .catch((e) => setError(String(e)));
  }, []);

  useEffect(() => {
    let active = true;

    const init = async () => {
      try {
        const [list, current] = await Promise.all([GetModes(), GetState()]);

        if (active) {
          setModes(list);
          setState(current);
        }
      } catch (e) {
        if (active) setError(String(e));
      }
    };

    void init();

    const timer = window.setInterval(async () => {
      try {
        const current = await GetState();
        if (active) setState(current);
      } catch (e) {
        if (active) setError(String(e));
      }
    }, 500);

    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, []);

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

  if (!state) {
    return (
      <main className="p-3 text-xs text-slate-500">{error || "加载中..."}</main>
    );
  }

  const running = state.status === "running";

  return (
    <main className="flex inset-0 min-h-0 h-full w-full flex-col gap-2 overflow-hidden p-2 text-slate-700">
      {/* 四个模式方块 */}
      <section className="flex shrink-0  gap-1">
        {modes.map((mode, index) => {
          const selected = state.activeMode === mode.id;

          return (
            <button
              key={mode.id}
              disabled={busy || running}
              onClick={() => action(() => SelectMode(mode.id))}
              className={`flex flex-1  h-10 flex-col items-center justify-center gap-1 rounded-md border text-xs transition-colors disabled:cursor-not-allowed ${
                selected
                  ? "border-blue-500 bg-blue-50 text-blue-700"
                  : "border-slate-200 bg-white text-slate-500 hover:bg-slate-50"
              }`}
            >
              <span className="max-w-full truncate px-1">{mode.name}</span>
            </button>
          );
        })}
      </section>

      {/* 日志区域 */}
      <section className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-md border border-slate-200">
        <div className="flex shrink-0 items-center justify-between border-b border-slate-100 px-2 py-1.5">
          <span className="text-xs font-medium">运行日志</span>
          <button
            className="text-xs text-slate-400 hover:text-slate-700"
            onClick={() => setState({ ...state, logs: [] })}
          >
            清空
          </button>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto px-2 py-1.5 font-mono text-[11px] leading-5">
          {state.logs.slice(-200).map((log, index) => (
            <div
              key={`${index}-${log}`}
              className="break-all whitespace-pre-wrap"
            >
              {log}
            </div>
          ))}

          {state.logs.length === 0 && (
            <div className="text-slate-300">暂无日志</div>
          )}
        </div>
      </section>

      {/* 错误提示 */}
      {error && (
        <div className="shrink-0 break-all text-xs text-red-500">{error}</div>
      )}

      {/* 底部启动 / 停止 */}
      <section className="grid shrink-0 grid-cols-2 gap-2">
        <button
          disabled={busy || running}
          onClick={() => action(StartAgent)}
          className="h-9 rounded-md bg-blue-600 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40"
        >
          {busy ? "请稍候..." : "▶ 启动"}
        </button>

        <button
          disabled={busy || !running}
          onClick={() => action(StopAgent)}
          className="h-9 rounded-md border border-slate-200 bg-white text-sm hover:bg-slate-50 disabled:opacity-40"
        >
          ■ 停止
        </button>
      </section>
    </main>
  );
}
