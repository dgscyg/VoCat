import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  PlayRegular,
  PauseRegular,
  DeleteRegular,
  ArrowDownloadRegular,
  DismissRegular,
} from "@fluentui/react-icons";
import { api, eventStreamURL } from "../api";
import type { LogEntry } from "../types";
import { cx } from "../lib/utils";
import { useI18n } from "../lib/i18n";
import { PageHeader } from "../components/ui/PageHeader";
import { Button } from "../components/ui/Button";
import { Switch } from "../components/ui/Switch";
import { Select } from "../components/ui/Select";
import { Input } from "../components/ui/Input";
import { message } from "../components/ui/message";
import { confirmDialog } from "../components/ui/MessageBox";
import { LogRetentionCard } from "../components/logs/LogRetentionCard";

const MAX_LOGS = 1000;

type Level = "all" | "debug" | "info" | "warn" | "error";
type Category = "all" | "hardware" | "network" | "vowifi" | "sms" | "call" | "operation" | "system";

const LEVEL_OPTIONS: { value: Level; label: string }[] = [
  { value: "all", label: "全部" },
  { value: "debug", label: "DEBUG" },
  { value: "info", label: "INFO" },
  { value: "warn", label: "WARN" },
  { value: "error", label: "ERROR" },
];

const CATEGORY_OPTIONS: { value: Category; label: string }[] = [
  { value: "all", label: "全部业务" },
  { value: "hardware", label: "硬件与模块" },
  { value: "network", label: "驻网" },
  { value: "vowifi", label: "WiFi Calling" },
  { value: "sms", label: "短信" },
  { value: "call", label: "通话" },
  { value: "operation", label: "用户操作" },
  { value: "system", label: "系统错误" },
];

function levelColor(level: string): string {
  switch (level.toLowerCase()) {
    case "debug":
      return "text-purple-500";
    case "info":
      return "text-blue-500";
    case "warn":
      return "text-yellow-500";
    case "error":
      return "text-red-500";
    case "fatal":
      return "text-red-600 font-bold";
    default:
      return "text-gray-500";
  }
}

function fieldsText(fields: LogEntry["fields"]): string {
  if (fields === undefined || fields === null) return "";
  return typeof fields === "string" ? fields : JSON.stringify(fields);
}

function logFields(entry: LogEntry): Record<string, unknown> {
  return entry.fields && typeof entry.fields === "object" ? entry.fields : {};
}

function logCategory(entry: LogEntry): Exclude<Category, "all"> {
  const explicit = String(logFields(entry).category ?? "").toLowerCase();
  if (CATEGORY_OPTIONS.some((item) => item.value === explicit && item.value !== "all")) {
    return explicit as Exclude<Category, "all">;
  }
  const text = `${entry.message} ${fieldsText(entry.fields)}`.toLowerCase();
  if (/\bsms\b|短信|tpdu|rp-data|rpdu/.test(text)) return "sms";
  if (/incoming call|\bcall\b|invite|来电|通话/.test(text)) return "call";
  if (/vowifi|wi-?fi calling|\bims\b|\bike\b|epdg|ipsec/.test(text)) return "vowifi";
  if (/registration|operator|network|驻网|注册网络/.test(text)) return "network";
  if (/device|modem|hardware|sim|uicc|esim|qmi|串口|模块|设备/.test(text)) return "hardware";
  if (/operation|audit|setting|操作/.test(text)) return "operation";
  return "system";
}

function categoryColor(category: Exclude<Category, "all">): string {
  switch (category) {
    case "hardware": return "bg-cyan-500/15 text-cyan-300";
    case "network": return "bg-emerald-500/15 text-emerald-300";
    case "vowifi": return "bg-sky-500/15 text-sky-300";
    case "sms": return "bg-violet-500/15 text-violet-300";
    case "call": return "bg-pink-500/15 text-pink-300";
    case "operation": return "bg-amber-500/15 text-amber-300";
    default: return "bg-gray-500/20 text-gray-300";
  }
}

function isHTTPAccessLog(entry: LogEntry): boolean {
  return entry.message.trim().toLowerCase() === "http request" ||
    String(logFields(entry).category ?? "").toLowerCase() === "http_access";
}

// Reference renders a fixed YYYY-MM-DD HH:mm:ss timestamp.
function displayTime(time: string): string {
  try {
    const d = new Date(time);
    const pad = (n: number) => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
  } catch {
    return time;
  }
}

export default function LogsPage() {
  const { t } = useI18n();
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [connected, setConnected] = useState(false);
  const [paused, setPaused] = useState(false);
  const [autoTail, setAutoTail] = useState(true);
  const [level, setLevel] = useState<Level>("all");
  const [category, setCategory] = useState<Category>("all");
  const [search, setSearch] = useState("");
  const [connError, setConnError] = useState("");
  const [clearing, setClearing] = useState(false);
  const [retentionRefreshKey, setRetentionRefreshKey] = useState(0);

  const esRef = useRef<EventSource | null>(null);
  const logContainerRef = useRef<HTMLDivElement>(null);
  const pausedRef = useRef(false);
  const levelRef = useRef<Level>("all");

  const appendLog = useCallback((entry: LogEntry) => {
    if (isHTTPAccessLog(entry)) return;
    setLogs((prev) => {
      const next = [...prev, entry];
      return next.length > MAX_LOGS ? next.slice(-MAX_LOGS) : next;
    });
  }, []);

  const connect = useCallback(() => {
    esRef.current?.close();
    const params = new URLSearchParams();
    if (levelRef.current !== "all") params.set("level", levelRef.current);
    const es = new EventSource(eventStreamURL("/logs/stream", params));
    esRef.current = es;

    es.onopen = () => {
      setConnected(true);
      setConnError("");
    };
    es.onerror = () => {
      setConnected(false);
      setConnError(t("连接中断，正在尝试重连…"));
    };
    const handle = (ev: MessageEvent<string>) => {
      if (pausedRef.current) return;
      try {
        appendLog(JSON.parse(ev.data) as LogEntry);
      } catch {
        /* 忽略无法解析的日志帧 */
      }
    };
    es.onmessage = handle;
    es.addEventListener("log", handle);
  }, [appendLog]);

  const loadHistory = useCallback(async () => {
    setLoading(true);
    try {
      const res = await api<LogEntry[] | { logs?: LogEntry[] }>("/logs/history?lines=500");
      const list = Array.isArray(res) ? res : (res?.logs ?? []);
      setLogs(list.filter((entry) => !isHTTPAccessLog(entry)).slice(-MAX_LOGS));
    } catch {
      /* 历史回填失败不阻塞实时流 */
    } finally {
      setLoading(false);
    }
  }, []);

  // 挂载：回填历史并建立实时流；卸载：断开。
  useEffect(() => {
    pausedRef.current = false;
    void (async () => {
      await loadHistory();
      if (!pausedRef.current) connect();
    })();
    return () => {
      esRef.current?.close();
      esRef.current = null;
    };
  }, [connect, loadHistory]);

  // 级别变化：更新服务端过滤并（未暂停时）重连。
  useEffect(() => {
    levelRef.current = level;
    if (!pausedRef.current) connect();
  }, [level, connect]);

  // 自动追尾：新日志到达且未暂停时滚动到底部。
  useEffect(() => {
    if (!autoTail || paused) return;
    const el = logContainerRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [logs, autoTail, paused]);

  const togglePause = useCallback(() => {
    const next = !pausedRef.current;
    pausedRef.current = next;
    setPaused(next);
    if (next) {
      esRef.current?.close();
      setConnected(false);
    } else {
      connect();
    }
  }, [connect]);

  const clearLogs = useCallback(async () => {
    const confirmed = await confirmDialog(
      t("此操作会永久删除服务端保存的全部日志，无法恢复。"),
      t("确认清空日志？"),
      { type: "warning", confirmText: t("清空"), cancelText: t("取消") },
    );
    if (!confirmed) return;
    setClearing(true);
    try {
      await api<{ cleared: boolean; deleted: number }>("/logs/history", { method: "DELETE" });
      setLogs([]);
      setRetentionRefreshKey((value) => value + 1);
      message.success(t("日志已清空"));
    } catch (error) {
      message.error(error instanceof Error ? error.message : t("清空日志失败"));
    } finally {
      setClearing(false);
    }
  }, [t]);

  const filtered = useMemo(() => {
    let list = logs;
    if (level !== "all") {
      list = list.filter((e) => e.level.toLowerCase() === level.toLowerCase());
    }
    if (category !== "all") {
      list = list.filter((entry) => logCategory(entry) === category);
    }
    if (search.trim()) {
      const q = search.toLowerCase();
      list = list.filter(
        (e) =>
          e.message.toLowerCase().includes(q) ||
          (e.caller ?? "").toLowerCase().includes(q) ||
          fieldsText(e.fields).toLowerCase().includes(q),
      );
    }
    return list;
  }, [logs, level, category, search]);

  const exportLogs = useCallback(() => {
    const text = filtered
      .map((v) => {
        const time = new Date(v.time).toLocaleString();
        const fields = v.fields ? ` ${fieldsText(v.fields)}` : "";
        return `[${time}] ${v.level.toUpperCase().padEnd(5)} [${logCategory(v)}] ${v.caller ?? ""} ${v.message}${fields}`;
      })
      .join("\n");
    const blob = new Blob([text], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `logs-${new Date().toISOString().slice(0, 10)}.txt`;
    a.click();
    URL.revokeObjectURL(url);
    message.success(t("已导出日志"));
  }, [filtered]);

  return (
    <div className="max-w-7xl mx-auto">
      <PageHeader
        title={t("设备与业务日志")}
        subtitle={t("记录硬件、驻网、WiFi Calling、短信、通话和用户操作；敏感信息已自动打码")}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Button
              onClick={togglePause}
              variant={paused ? "success" : "warning"}
              className="!border-0 flex-1 justify-center sm:flex-none"
              icon={paused ? <PlayRegular /> : <PauseRegular />}
            >
              {paused ? t("继续") : t("暂停")}
            </Button>
            <Button loading={clearing} onClick={clearLogs} className="!border-0 flex-1 justify-center sm:flex-none" icon={<DeleteRegular />}>
              {t("清空")}
            </Button>
            <Button onClick={exportLogs} variant="primary" className="!border-0 flex-1 justify-center sm:flex-none" icon={<ArrowDownloadRegular />}>
              {t("导出")}
            </Button>
          </div>
        }
      />

      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 mb-4">
        <div className="flex items-center gap-2">
          <span
            className={cx("w-2 h-2 rounded-full", connected ? "bg-green-500 animate-pulse" : "bg-red-500")}
          />
          <span className="text-sm text-gray-500">{connected ? t("已连接") : t("未连接")}</span>
        </div>
        <span className="text-sm text-gray-400">{logs.length} {t("条日志")}</span>
        {!connected && connError ? (
          <span className="text-sm text-red-500 truncate" title={connError}>
            {connError}
          </span>
        ) : null}
        <div className="hidden sm:block flex-1" />
        <label className="flex items-center gap-2">
          <Switch checked={autoTail} onChange={setAutoTail} ariaLabel={t("自动追尾")} />
          <span className="text-sm text-gray-500 dark:text-gray-400">{t("自动追尾")}</span>
        </label>
      </div>

      <div className="ui-card p-4 mb-4">
        <div className="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-center">
          <Select
            value={level}
            onChange={(v) => setLevel(v as Level)}
            placeholder={t("日志级别")}
            className="w-full sm:w-40"
            options={LEVEL_OPTIONS.map((o) => ({ ...o, label: t(o.label) }))}
          />
          <Select
            value={category}
            onChange={(v) => setCategory(v as Category)}
            placeholder={t("业务分类")}
            className="w-full sm:w-44"
            options={CATEGORY_OPTIONS.map((o) => ({ ...o, label: t(o.label) }))}
          />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t("搜索日志内容...")}
            className="w-full sm:w-64"
            suffix={
              search ? (
                <button
                  type="button"
                  aria-label={t("清除搜索")}
                  onClick={() => setSearch("")}
                  className="flex items-center text-gray-400 transition-colors hover:text-gray-600 dark:hover:text-gray-300"
                >
                  <DismissRegular />
                </button>
              ) : undefined
            }
          />
          <span className="text-sm text-gray-400 sm:ml-auto">
            {t("显示")} {filtered.length} / {logs.length} {t("条")}
          </span>
        </div>
      </div>

      <LogRetentionCard refreshKey={retentionRefreshKey} />

      <div className="ui-card overflow-hidden">
        <div
          ref={logContainerRef}
          className="h-[60vh] min-h-[280px] overflow-auto font-mono text-sm bg-gray-900 dark:bg-black text-gray-100 p-4"
        >
          {filtered.length === 0 ? (
            <div className="text-gray-500 text-center py-8">
              {loading ? t("等待日志...") : connected ? t("等待日志...") : t("未连接到日志流")}
            </div>
          ) : null}
          {filtered.map((entry, i) => {
            const entryCategory = logCategory(entry);
            const fields = logFields(entry);
            const hasRawError = fields.raw_error !== undefined || fields.error !== undefined || fields.raw_response !== undefined;
            return (
              <div key={`${entry.time}-${i}`} className="border-b border-white/5 px-2 py-2 -mx-2 last:border-0 hover:bg-white/5">
                <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                  <span className="text-gray-500">[{displayTime(entry.time)}]</span>
                  <span className={cx("font-bold", levelColor(entry.level))}>{entry.level.toUpperCase()}</span>
                  <span className={cx("rounded px-1.5 py-0.5 text-[11px]", categoryColor(entryCategory))}>
                    {t(CATEGORY_OPTIONS.find((item) => item.value === entryCategory)?.label ?? "系统错误")}
                  </span>
                  {entry.caller ? <span className="max-w-48 truncate text-indigo-400" title={entry.caller}>{entry.caller}</span> : null}
                  <span className="break-words text-gray-100">{entry.message}</span>
                </div>
                {entry.fields ? (
                  <pre className={cx(
                    "mt-1 whitespace-pre-wrap break-all pl-2 text-xs leading-5",
                    hasRawError ? "border-l-2 border-red-500/60 text-red-200" : "text-amber-300/70",
                  )}>{typeof entry.fields === "string" ? entry.fields : JSON.stringify(entry.fields, null, 2)}</pre>
                ) : null}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
