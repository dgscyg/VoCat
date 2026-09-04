import { useCallback, useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { CallRegular } from "@fluentui/react-icons";
import { api, apiMessage } from "../api";
import type { DeviceListItem, DevicesResponse } from "../types";
import { usePolling } from "../lib/usePolling";
import { useI18n } from "../lib/i18n";
import { Button, ErrorState, PageHeader, RefreshButton, Select, Spinner, message } from "../components/ui";
import { DeviceCallTab } from "../components/devices/DeviceCallTab";
import { useDeviceCalls } from "../components/devices/useDeviceCalls";
import { isDeviceOnline } from "../components/devices/shared";
import type { DeviceDetail } from "../components/devices/types";

interface LoadError {
  message: string;
  status?: number;
}

interface SoftphoneInfo {
  enabled?: boolean;
  username?: string;
  password?: string;
  realm?: string;
  wsUrl?: string;
  udpPort?: number;
  host?: string;
}

function toDetail(item: DeviceListItem): DeviceDetail {
  return {
    ...item,
    modem: item.modem || {},
  } as DeviceDetail;
}

export default function SoftphonePage() {
  const { t } = useI18n();
  const [searchParams, setSearchParams] = useSearchParams();
  const [devices, setDevices] = useState<DeviceListItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<LoadError | null>(null);
  const requestedId = (searchParams.get("device") || "").trim();

  const loadDevices = useCallback(async () => {
    try {
      const data = await api<DevicesResponse>("/devices");
      setDevices(data.devices || []);
      setError(null);
    } catch (err) {
      setError({ message: apiMessage(err) || t("加载失败"), status: (err as { status?: number })?.status });
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void loadDevices();
  }, [loadDevices]);
  usePolling(loadDevices, 8000);

  const callable = useMemo(
    () => devices.filter((device) => device.deviceType !== "wifi_410"),
    [devices],
  );
  const selectedId = useMemo(() => {
    if (requestedId && callable.some((device) => device.id === requestedId)) return requestedId;
    return callable[0]?.id || "";
  }, [callable, requestedId]);
  const selected = callable.find((device) => device.id === selectedId) || null;
  const session = useDeviceCalls(selectedId, !!selectedId);
  const [sip, setSip] = useState<SoftphoneInfo | null>(null);

  useEffect(() => {
    if (!selectedId) {
      setSip(null);
      return;
    }
    let cancelled = false;
    api<SoftphoneInfo>(`/devices/${encodeURIComponent(selectedId)}/softphone`)
      .then((info) => {
        if (!cancelled) setSip(info);
      })
      .catch(() => {
        if (!cancelled) setSip({ enabled: false });
      });
    return () => {
      cancelled = true;
    };
  }, [selectedId]);

  function selectDevice(id: string) {
    const next = new URLSearchParams(searchParams);
    if (id) next.set("device", id);
    else next.delete("device");
    setSearchParams(next, { replace: true });
  }

  return (
    <div>
      <PageHeader
        title={t("软电话")}
        subtitle={t("在浏览器里拨打和接听当前线路，无需外部 SIP 客户端")}
        actions={<RefreshButton loading={loading} onClick={() => void loadDevices()} />}
      />
      {error ? (
        <ErrorState
          className="mb-6"
          title={t("设备列表加载失败")}
          message={error.message}
          statusCode={error.status}
          retryText={t("重试")}
          onRetry={() => void loadDevices()}
        />
      ) : null}
      {loading && devices.length === 0 ? (
        <div className="ui-card flex items-center justify-center p-16 text-gray-400">
          <Spinner />
        </div>
      ) : callable.length === 0 ? (
        <div className="ui-card p-10 text-center text-sm text-gray-400">{t("暂无可用于通话的设备")}</div>
      ) : (
        <div className="space-y-4">
          <div className="ui-card flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-300">
              <CallRegular className="text-lg" />
              <span>{t("选择线路")}</span>
            </div>
            <Select
              className="sm:min-w-[280px]"
              value={selectedId}
              onChange={selectDevice}
              options={callable.map((device) => ({
                value: device.id,
                label: `${device.name || device.id}${isDeviceOnline(device) ? "" : ` · ${t("离线")}`}`,
              }))}
            />
          </div>
          {selected ? (
            <div className="ui-card p-6">
              <DeviceCallTab device={toDetail(selected)} online={isDeviceOnline(selected)} session={session} />
            </div>
          ) : null}
          {sip?.enabled ? (
            <div className="ui-card space-y-3 p-6 text-sm">
              <div className="text-base font-bold text-gray-900 dark:text-white">{t("SIP 分机")}</div>
              <div className="text-gray-500">{t("浏览器 WebRTC 分机和外部 SIP 客户端可注册到这条线路")}</div>
              <div className="grid gap-2 font-mono text-xs sm:grid-cols-2">
                <div>{t("用户名")}: {sip.username}</div>
                <div className="flex items-center gap-2">
                  {t("密码")}: {sip.password}
                  <Button
                    size="small"
                    onClick={() => {
                      void navigator.clipboard.writeText(sip.password || "");
                      message.success(t("已复制 SIP 密码"));
                    }}
                  >
                    {t("复制")}
                  </Button>
                </div>
                <div className="sm:col-span-2 break-all">{t("WSS 地址")}: {sip.wsUrl}</div>
                <div>{t("SIP UDP")}: {sip.host}:{sip.udpPort}</div>
              </div>
            </div>
          ) : null}
        </div>
      )}
    </div>
  );
}
