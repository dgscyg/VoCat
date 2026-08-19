import { ServerRegular } from "@fluentui/react-icons";
import type { DashboardHostInfo } from "../../types";
import { useI18n } from "../../lib/i18n";

interface Row {
  label: string;
  value: string;
}

// 宿主机信息卡：CPU / 主板 / 内存 / 硬盘型号，后端一次性探测后缓存。
export function HostInfoCard({ info }: { info?: DashboardHostInfo | null }) {
  const { t } = useI18n();
  const rows: Row[] = [
    { label: t("CPU 型号"), value: info?.cpuModel || "" },
    { label: t("主板型号"), value: info?.boardModel || "" },
    { label: t("内存型号"), value: info?.memoryModel || "" },
    { label: t("硬盘型号"), value: info?.diskModel || "" },
  ];
  return (
    <div className="ui-panel p-4">
      <div className="mb-3 flex items-center gap-2">
        <ServerRegular className="h-4 w-4 text-sky-500" />
        <h3 className="text-sm font-bold text-gray-800 dark:text-gray-100">{t("宿主机信息")}</h3>
      </div>
      <div className="space-y-2.5">
        {rows.map((row) => (
          <div key={row.label} className="flex items-baseline justify-between gap-3">
            <span className="flex-shrink-0 text-xs text-gray-400">{row.label}</span>
            <span
              className="min-w-0 flex-1 truncate text-right text-xs font-medium text-gray-700 dark:text-gray-300"
              title={row.value || undefined}
            >
              {row.value || "—"}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}
