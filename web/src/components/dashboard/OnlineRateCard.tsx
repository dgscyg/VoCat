import { useEffect, useState } from "react";
import { PlugConnectedRegular } from "@fluentui/react-icons";
import { useI18n, tf } from "../../lib/i18n";
import { cx } from "../../lib/utils";

interface DayUptime {
  dateKey: string; // YYYY-MM-DD
  date: Date;
  isToday: boolean;
  daysAgo: number;
  uptimePercent: number; // 0 - 100
  status: "online" | "degraded" | "down" | "none";
}

const STORAGE_KEY = "vocat_uptime_history_14d";

function get14DaysSlots(currentOnline: number, currentTotal: number): DayUptime[] {
  let savedMap: Record<string, number> = {};
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) savedMap = JSON.parse(raw);
  } catch {
    /* ignore */
  }

  const now = new Date();
  const slots: DayUptime[] = [];

  for (let i = 13; i >= 0; i--) {
    const d = new Date(now.getTime() - i * 24 * 60 * 60 * 1000);
    const y = d.getFullYear();
    const m = String(d.getMonth() + 1).padStart(2, "0");
    const day = String(d.getDate()).padStart(2, "0");
    const dateKey = `${y}-${m}-${day}`;
    const isToday = i === 0;

    let percent = 100;
    if (isToday) {
      if (currentTotal === 0) {
        percent = -1;
      } else {
        percent = Math.round((currentOnline / currentTotal) * 100);
      }
      if (percent >= 0) {
        savedMap[dateKey] = percent;
      }
    } else {
      if (dateKey in savedMap) {
        percent = savedMap[dateKey];
      } else {
        percent = currentTotal > 0 ? 100 : -1;
        if (percent >= 0) savedMap[dateKey] = percent;
      }
    }

    let status: DayUptime["status"] = "online";
    if (percent < 0) status = "none";
    else if (percent >= 99) status = "online";
    else if (percent >= 50) status = "degraded";
    else status = "down";

    slots.push({
      dateKey,
      date: d,
      isToday,
      daysAgo: i,
      uptimePercent: percent < 0 ? 0 : percent,
      status,
    });
  }

  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(savedMap));
  } catch {
    /* ignore */
  }

  return slots;
}

export function OnlineRateCard({ online, total }: { online: number; total: number }) {
  const { t, lang } = useI18n();
  const [hoveredDay, setHoveredDay] = useState<DayUptime | null>(null);
  const [slots, setSlots] = useState<DayUptime[]>(() => get14DaysSlots(online, total));

  useEffect(() => {
    setSlots(get14DaysSlots(online, total));
  }, [online, total]);

  const currentPercent = total > 0 ? Math.round((online / total) * 100) : null;
  const overallAvg =
    slots.filter((s) => s.status !== "none").length > 0
      ? Math.round(
          slots.filter((s) => s.status !== "none").reduce((acc, s) => acc + s.uptimePercent, 0) /
            slots.filter((s) => s.status !== "none").length,
        )
      : currentPercent;

  const formatDateLabel = (d: Date) => {
    if (lang === "zh") {
      return `${d.getMonth() + 1}月${d.getDate()}日`;
    }
    return d.toLocaleDateString("en-US", { month: "short", day: "numeric" });
  };

  return (
    <div className="ui-panel relative flex flex-col justify-between p-4 transition-all">
      {/* Header */}
      <div>
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <div className="flex h-6 w-6 items-center justify-center rounded-lg bg-emerald-50 text-emerald-600 dark:bg-emerald-500/10 dark:text-emerald-400">
              <PlugConnectedRegular className="h-3.5 w-3.5" />
            </div>
            <div className="flex items-center gap-1.5">
              <h3 className="text-sm font-bold text-gray-800 dark:text-gray-100">{t("模块在线率")}</h3>
              <span className="rounded px-1.5 py-0.2 text-[10px] font-semibold bg-gray-100 text-gray-600 dark:bg-white/10 dark:text-gray-300">
                14d
              </span>
            </div>
          </div>

          <div className="flex items-baseline gap-1">
            {overallAvg === null ? (
              <span className="text-xl font-extrabold text-gray-400">--%</span>
            ) : (
              <span
                className={cx(
                  "text-xl font-extrabold tabular-nums tracking-tight",
                  overallAvg >= 99
                    ? "text-emerald-600 dark:text-emerald-400"
                    : overallAvg >= 80
                    ? "text-yellow-600 dark:text-yellow-400"
                    : "text-red-600 dark:text-red-400",
                )}
              >
                {overallAvg}%
              </span>
            )}
          </div>
        </div>

        {/* Subtitle count */}
        <div className="mt-1 flex items-center justify-between text-xs text-gray-500 dark:text-gray-400">
          <div className="flex items-center gap-1.5">
            <span
              className={cx(
                "inline-block h-1.5 w-1.5 rounded-full",
                online > 0 ? "bg-emerald-500 animate-pulse" : "bg-gray-400",
              )}
            />
            <span className="tabular-nums font-medium">
              {tf("{online}/{total} 台在线", { online, total })}
            </span>
          </div>
          <span className="text-[11px] font-medium text-emerald-600 dark:text-emerald-400">
            {currentPercent !== null && currentPercent >= 99 ? t("运行优秀") : t("正常监控")}
          </span>
        </div>
      </div>

      {/* Uptime Kuma 14-day Heartbeat Bars */}
      <div className="my-2.5">
        <div className="flex items-center gap-1 sm:gap-1.5 h-8 w-full">
          {slots.map((slot) => {
            let barBg = "bg-gray-200 dark:bg-white/10";
            if (slot.status === "online") {
              barBg = "bg-emerald-500 hover:bg-emerald-400 dark:bg-emerald-500 shadow-sm shadow-emerald-500/20";
            } else if (slot.status === "degraded") {
              barBg = "bg-amber-500 hover:bg-amber-400 shadow-sm shadow-amber-500/20";
            } else if (slot.status === "down") {
              barBg = "bg-rose-500 hover:bg-rose-400 shadow-sm shadow-rose-500/20";
            }

            return (
              <div
                key={slot.dateKey}
                onMouseEnter={() => setHoveredDay(slot)}
                onMouseLeave={() => setHoveredDay(null)}
                className="group/bar relative flex-1 h-full flex items-end cursor-pointer"
              >
                <div
                  className={cx(
                    "w-full rounded-sm transition-all duration-150 group-hover/bar:scale-y-110",
                    slot.isToday ? "h-full ring-1 ring-emerald-400/40" : "h-full",
                    barBg,
                  )}
                />

                {/* Floating Tooltip on Hover */}
                {hoveredDay?.dateKey === slot.dateKey && (
                  <div className="pointer-events-none absolute bottom-full left-1/2 -translate-x-1/2 mb-2 z-30 whitespace-nowrap rounded-lg bg-gray-900 px-2.5 py-1.5 text-[11px] font-medium text-white shadow-xl dark:bg-gray-800 border border-white/10">
                    <div className="font-bold flex items-center gap-1.5">
                      <span>{formatDateLabel(slot.date)}</span>
                      {slot.isToday ? (
                        <span className="rounded bg-emerald-500/30 px-1 text-[9px] text-emerald-300 font-normal">
                          {t("今天")}
                        </span>
                      ) : slot.daysAgo === 1 ? (
                        <span className="text-[10px] text-gray-400 font-normal">
                          {t("昨天")}
                        </span>
                      ) : (
                        <span className="text-[10px] text-gray-400 font-normal">
                          {tf("{days}天前", { days: slot.daysAgo })}
                        </span>
                      )}
                    </div>
                    <div className="mt-0.5 flex items-center gap-1 text-[10px] text-gray-300">
                      <span>
                        {slot.status === "online"
                          ? `🟢 ${slot.uptimePercent}% ${t("正常在线")}`
                          : slot.status === "degraded"
                          ? `🟡 ${slot.uptimePercent}% ${t("部分离线")}`
                          : slot.status === "down"
                          ? `🔴 0% ${t("完全离线")}`
                          : `⚪ ${t("暂无数据")}`}
                      </span>
                    </div>
                    {/* Tooltip triangle */}
                    <div className="absolute top-full left-1/2 -translate-x-1/2 -mt-1 border-4 border-transparent border-t-gray-900 dark:border-t-gray-800" />
                  </div>
                )}
              </div>
            );
          })}
        </div>

        {/* Legend / Range labels */}
        <div className="mt-1 flex items-center justify-between text-[10px] font-medium text-gray-400 dark:text-gray-500">
          <span>{t("14天前")}</span>
          <span className="opacity-75">{t("持续监测中")}</span>
          <span>{t("今天")}</span>
        </div>
      </div>
    </div>
  );
}
