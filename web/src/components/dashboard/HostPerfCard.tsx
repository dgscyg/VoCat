import { ArrowDownRegular, ArrowUpRegular, GaugeRegular } from "@fluentui/react-icons";
import type { DashboardHostPerf } from "../../types";
import { useI18n } from "../../lib/i18n";
import { cx, formatBytes } from "../../lib/utils";

function percentText(value: number) {
  return `${value.toFixed(1)}%`;
}

// 利用率越高颜色越危险：<70 绿，<90 黄，其余红。
function barColor(percent: number) {
  if (percent >= 90) return "bg-red-500";
  if (percent >= 70) return "bg-amber-500";
  return "bg-emerald-500";
}

function textColor(percent: number) {
  if (percent >= 90) return "text-red-600 dark:text-red-400";
  if (percent >= 70) return "text-amber-600 dark:text-amber-400";
  return "text-emerald-600 dark:text-emerald-400";
}

function UsageBar({ label, percent, detail }: { label: string; percent: number; detail?: string }) {
  const clamped = Math.min(100, Math.max(0, percent || 0));
  return (
    <div>
      <div className="mb-1 flex items-baseline justify-between gap-2">
        <span className="text-xs text-gray-400">{label}</span>
        <span className="flex items-baseline gap-1.5">
          {detail ? <span className="text-[10px] text-gray-400">{detail}</span> : null}
          <span className={cx("text-xs font-bold tabular-nums", textColor(clamped))}>{percentText(clamped)}</span>
        </span>
      </div>
      <div className="h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-white/10">
        <div
          className={cx("h-full rounded-full transition-all duration-500", barColor(clamped))}
          style={{ width: `${clamped}%` }}
        />
      </div>
    </div>
  );
}

// 性能信息卡：CPU / 内存 / 硬盘进度条 + 实时网络上下行速率。
export function HostPerfCard({ perf }: { perf?: DashboardHostPerf | null }) {
  const { t } = useI18n();
  return (
    <div className="ui-panel p-4">
      <div className="mb-3 flex items-center gap-2">
        <GaugeRegular className="h-4 w-4 text-sky-500" />
        <h3 className="text-sm font-bold text-gray-800 dark:text-gray-100">{t("性能信息")}</h3>
      </div>
      <div className="space-y-2.5">
        <UsageBar label={t("CPU 使用率")} percent={perf?.cpuPercent ?? 0} />
        <UsageBar
          label={t("内存使用率")}
          percent={perf?.memoryPercent ?? 0}
          detail={perf && perf.memoryTotalBytes > 0 ? `${formatBytes(perf.memoryUsedBytes)} / ${formatBytes(perf.memoryTotalBytes)}` : undefined}
        />
        <UsageBar
          label={t("硬盘使用率")}
          percent={perf?.diskPercent ?? 0}
          detail={perf && perf.diskTotalBytes > 0 ? `${formatBytes(perf.diskUsedBytes)} / ${formatBytes(perf.diskTotalBytes)}` : undefined}
        />
        <div className="flex items-center justify-between gap-2 pt-0.5">
          <span className="text-xs text-gray-400">{t("网络")}</span>
          <span className="flex items-center gap-3 text-xs font-semibold tabular-nums">
            <span className="flex items-center gap-1 text-sky-600 dark:text-sky-400" title={t("实时上传速率")}>
              <ArrowUpRegular className="h-3.5 w-3.5" />
              {formatBytes(perf?.netTxBps ?? 0)}/s
            </span>
            <span className="flex items-center gap-1 text-emerald-600 dark:text-emerald-400" title={t("实时下载速率")}>
              <ArrowDownRegular className="h-3.5 w-3.5" />
              {formatBytes(perf?.netRxBps ?? 0)}/s
            </span>
          </span>
        </div>
      </div>
    </div>
  );
}
