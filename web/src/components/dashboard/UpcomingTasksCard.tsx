import { CalendarClockRegular } from "@fluentui/react-icons";
import { useNavigate } from "react-router-dom";
import type { DashboardUpcomingTask } from "../../types";
import { useI18n } from "../../lib/i18n";

function formatRunAt(value: string): string {
  if (!value || value.startsWith("0001-")) return "--";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "--";
  return date.toLocaleString(undefined, {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

// 将要执行的定时任务卡：按 nextRunAt 升序取前 3 条已启用任务。
export function UpcomingTasksCard({ tasks }: { tasks: DashboardUpcomingTask[] }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  return (
    <div className="ui-panel p-4">
      <div className="mb-3 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <CalendarClockRegular className="h-4 w-4 text-sky-500" />
          <h3 className="text-sm font-bold text-gray-800 dark:text-gray-100">{t("将要执行的定时任务")}</h3>
        </div>
        <button
          type="button"
          onClick={() => navigate("/automatic-tasks")}
          className="text-xs font-medium text-sky-600 transition-colors hover:text-sky-700 dark:text-sky-400 dark:hover:text-sky-300"
        >
          {t("查看全部")}
        </button>
      </div>
      {tasks.length === 0 ? (
        <div className="flex h-[4.5rem] items-center justify-center text-xs text-gray-400">
          {t("暂无定时任务")}
        </div>
      ) : (
        <div className="divide-y divide-gray-100 dark:divide-white/5">
          {tasks.map((task) => (
            <div key={task.id} className="flex items-center justify-between gap-3 py-2 first:pt-0 last:pb-0">
              <span className="min-w-0 flex-1 truncate text-xs font-medium text-gray-700 dark:text-gray-300" title={task.name}>
                {task.name}
              </span>
              <span className="flex-shrink-0 font-mono text-xs tabular-nums text-gray-500 dark:text-gray-400">
                {formatRunAt(task.nextRunAt)}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
