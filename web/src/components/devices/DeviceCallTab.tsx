import { useMemo, useState } from "react";
import {
  CallEndRegular,
  CallRegular,
  DeleteRegular,
  MicOffRegular,
  MicRegular,
  Speaker2Regular,
} from "@fluentui/react-icons";
import { Button, Input, Tag } from "../ui";
import { useI18n } from "../../lib/i18n";
import type { DeviceDetail } from "./types";
import { isLiveCall, sanitizeDialNumber, type DeviceCall } from "./callTypes";
import { useDeviceCalls } from "./useDeviceCalls";
import { useCallMedia, type MediaStatus } from "./useCallMedia";

const DIAL_KEYS = ["1", "2", "3", "4", "5", "6", "7", "8", "9", "*", "0", "#"] as const;

function stateLabel(state: string, t: (value: string) => string): string {
  switch (state) {
    case "dialing":
      return t("拨号中");
    case "ringing":
      return t("振铃中");
    case "active":
      return t("通话中");
    case "held":
      return t("保持");
    case "ended":
      return t("已结束");
    case "failed":
      return t("失败");
    default:
      return state || t("未知");
  }
}

function mediaHint(status: MediaStatus, error: string, t: (value: string) => string): string {
  if (status === "live") return t("浏览器听筒和麦克风已接通");
  if (status === "connecting") return t("正在接通浏览器音频...");
  if (error === "secure_context") return t("麦克风需要 HTTPS 或本机访问");
  if (error === "microphone") return t("浏览器拒绝了麦克风权限，目前只能听不能说");
  if (status === "error") return t("音频通道异常，信令仍可用");
  return "";
}

export function IncomingCallBanner({
  call,
  busy,
  onAnswer,
  onHangup,
  onOpenTab,
}: {
  call: DeviceCall;
  busy?: string;
  onAnswer: () => void;
  onHangup: () => void;
  onOpenTab?: () => void;
}) {
  const { t } = useI18n();
  return (
    <div className="flex flex-col gap-3 rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-500/20 dark:bg-emerald-500/10 dark:text-emerald-200 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <div className="font-semibold">{t("来电")}</div>
        <div className="font-mono text-base">{call.number || t("未知号码")}</div>
      </div>
      <div className="flex flex-wrap gap-2">
        {onOpenTab ? (
          <Button size="small" onClick={onOpenTab}>
            {t("查看")}
          </Button>
        ) : null}
        <Button size="small" variant="primary" loading={busy === "answer"} onClick={onAnswer}>
          {t("接听")}
        </Button>
        <Button size="small" variant="danger" loading={busy === "hangup"} onClick={onHangup}>
          {t("挂断")}
        </Button>
      </div>
    </div>
  );
}

export function DeviceCallTab({
  device,
  online,
  session,
}: {
  device: DeviceDetail;
  online: boolean;
  session: ReturnType<typeof useDeviceCalls>;
}) {
  const { t } = useI18n();
  const [number, setNumber] = useState("");
  const [muted, setMuted] = useState(false);
  const live = session.live;
  const audioEnabled = session.audioAvailable && live?.state === "active";
  const media = useCallMedia(device.id, live?.id || "", !!audioEnabled, muted);
  const history = useMemo(
    () => session.calls.filter((call) => !isLiveCall(call)).slice(-4).reverse(),
    [session.calls],
  );

  function appendDigit(digit: string) {
    setNumber((current) => sanitizeDialNumber(current + digit));
  }

  async function handleDial() {
    const value = sanitizeDialNumber(number);
    if (value.length < 2) return;
    await session.dial(value);
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-sky-50 text-sky-600 dark:bg-sky-500/10 dark:text-sky-300">
          <CallRegular className="text-[22px]" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="text-lg font-bold text-gray-900 dark:text-white">{t("语音通话")}</div>
          <div className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">
            {session.audioAvailable
              ? t("VoWiFi IMS 已就绪，接通后可在浏览器里通话")
              : t("当前走基站电路域，只能拨打、接听和挂断，浏览器没有声音")}
          </div>
        </div>
        <Tag type={session.audioAvailable ? "success" : "warning"}>
          {session.audioAvailable ? t("VoWiFi 音频") : t("电路域 · 无音频")}
        </Tag>
      </div>

      {session.incoming ? (
        <IncomingCallBanner
          call={session.incoming}
          busy={session.busy}
          onAnswer={() => void session.answer(session.incoming!.id)}
          onHangup={() => void session.hangup(session.incoming!.id)}
        />
      ) : null}

      {live && live !== session.incoming ? (
        <div className="ui-panel-muted rounded-xl border border-gray-100 p-4 dark:border-white/10">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <div className="text-xs font-bold uppercase tracking-wider text-gray-500">{live.direction === "incoming" ? t("来电") : t("去电")}</div>
              <div className="mt-1 font-mono text-2xl font-semibold text-gray-900 dark:text-white">{live.number || t("未知号码")}</div>
              <div className="mt-1 text-sm text-gray-500">{stateLabel(live.state, t)}</div>
              {live.reason ? <div className="mt-1 text-xs text-red-500">{live.reason}</div> : null}
            </div>
            <div className="flex flex-wrap gap-2">
              {audioEnabled ? (
                <Button
                  icon={muted ? <MicOffRegular /> : <MicRegular />}
                  onClick={() => setMuted((value) => !value)}
                >
                  {muted ? t("已静音") : t("麦克风")}
                </Button>
              ) : null}
              <Button
                variant="danger"
                icon={<CallEndRegular />}
                loading={session.busy === "hangup"}
                onClick={() => void session.hangup(live.id)}
              >
                {t("挂断")}
              </Button>
            </div>
          </div>
          {audioEnabled || media.status !== "idle" ? (
            <div className="mt-3 flex items-center gap-2 text-xs text-gray-500">
              <Speaker2Regular />
              <span>{mediaHint(media.status, media.error, t) || t("等待对端接通")}</span>
            </div>
          ) : null}
        </div>
      ) : null}

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,280px)_1fr]">
        <div className="space-y-3">
          <Input
            value={number}
            onChange={(event) => setNumber(sanitizeDialNumber(event.target.value))}
            placeholder={t("输入号码")}
            disabled={!!live}
            onKeyDown={(event) => {
              if (event.key === "Enter") void handleDial();
            }}
          />
          <div className="grid grid-cols-3 gap-2">
            {DIAL_KEYS.map((key) => (
              <button
                key={key}
                type="button"
                disabled={!!live}
                onClick={() => appendDigit(key)}
                className="ui-glass-border h-12 rounded-xl text-lg font-semibold text-gray-800 disabled:opacity-40 dark:text-gray-100"
              >
                {key}
              </button>
            ))}
          </div>
          <div className="flex gap-2">
            <Button className="flex-1" disabled={!!live || number.includes("+")} onClick={() => appendDigit("+")}>
              +
            </Button>
            <Button className="flex-1" disabled={!!live || !number} icon={<DeleteRegular />} onClick={() => setNumber((value) => value.slice(0, -1))}>
              {t("删除")}
            </Button>
          </div>
          <Button
            variant="primary"
            className="w-full !border-0"
            icon={<CallRegular />}
            disabled={!online || !!live || number.trim().length < 2 || (device.deviceType === "usb_sim_reader" && !session.audioAvailable)}
            loading={session.busy === "dial"}
            onClick={() => void handleDial()}
          >
            {t("拨打")}
          </Button>
          {!online ? <div className="text-xs text-amber-600">{t("设备离线时无法发起通话")}</div> : null}
          {device.deviceType === "usb_sim_reader" && !session.audioAvailable ? (
            <div className="text-xs text-amber-600">{t("USB SIM 读卡器只能在 VoWiFi IMS 就绪后通话")}</div>
          ) : null}
        </div>
        <div className="ui-panel-muted rounded-xl border border-gray-100 p-4 text-sm dark:border-white/10">
          <div className="mb-2 text-xs font-bold uppercase tracking-wider text-gray-500">{t("最近通话")}</div>
          {history.length === 0 ? (
            <div className="py-8 text-center text-gray-400">{t("还没有结束的通话")}</div>
          ) : (
            <div className="space-y-2">
              {history.map((call) => (
                <button
                  key={call.id}
                  type="button"
                  className="flex w-full items-center justify-between rounded-lg px-2 py-2 text-left hover:bg-white/60 dark:hover:bg-white/5"
                  onClick={() => setNumber(call.number || "")}
                >
                  <span className="font-mono">{call.number || t("未知号码")}</span>
                  <span className="text-xs text-gray-500">{stateLabel(call.state, t)}</span>
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
