import { PlugConnectedRegular } from "@fluentui/react-icons";
import { useI18n, tf } from "../../lib/i18n";
import { cx } from "../../lib/utils";

// 模块在线率分四档：100% 绿，80-99% 黄，50-79% 橙，低于 50% 红。
type RateLevel = "green" | "yellow" | "orange" | "red";

function rateLevel(percent: number): RateLevel {
  if (percent >= 100) return "green";
  if (percent >= 80) return "yellow";
  if (percent >= 50) return "orange";
  return "red";
}

const LEVEL_STYLES: Record<RateLevel, { text: string; dot: string; labelKey: string }> = {
  green: { text: "text-emerald-600 dark:text-emerald-400", dot: "bg-emerald-500", labelKey: "优秀" },
  yellow: { text: "text-yellow-600 dark:text-yellow-400", dot: "bg-yellow-500", labelKey: "良好" },
  orange: { text: "text-orange-600 dark:text-orange-400", dot: "bg-orange-500", labelKey: "一般" },
  red: { text: "text-red-600 dark:text-red-400", dot: "bg-red-500", labelKey: "较差" },
};

// 模块在线率卡：汇总全部已添加且可识别的模块，大字号百分比按四档着色。
export function OnlineRateCard({ online, total }: { online: number; total: number }) {
  const { t } = useI18n();
  const percent = total > 0 ? Math.round((online / total) * 100) : null;
  const level = percent === null ? null : rateLevel(percent);
  const styles = level ? LEVEL_STYLES[level] : null;

  return (
    <div className="ui-panel p-4">
      <div className="mb-1 flex items-center gap-2">
        <PlugConnectedRegular className="h-4 w-4 text-sky-500" />
        <h3 className="text-sm font-bold text-gray-800 dark:text-gray-100">{t("模块在线率")}</h3>
      </div>
      <div className="flex items-center justify-center py-1">
        {percent === null ? (
          <div className="text-4xl font-extrabold text-gray-300 dark:text-gray-600">--%</div>
        ) : (
          <div className={cx("text-5xl font-extrabold tabular-nums leading-none", styles!.text)}>
            {percent}
            <span className="text-2xl">%</span>
          </div>
        )}
      </div>
      <div className="mt-2 flex items-center justify-center gap-2 text-xs text-gray-500 dark:text-gray-400">
        {styles ? (
          <span className="flex items-center gap-1">
            <span className={cx("inline-block h-1.5 w-1.5 rounded-full", styles.dot)} />
            {t(styles.labelKey)}
          </span>
        ) : null}
        <span className="tabular-nums">{tf("{online}/{total} 台在线", { online, total })}</span>
      </div>
    </div>
  );
}
