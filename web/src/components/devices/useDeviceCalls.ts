import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, apiMessage } from "../../api";
import { message } from "../ui";
import { useI18n } from "../../lib/i18n";
import {
  pickIncomingCall,
  pickLiveCall,
  type DeviceCall,
  type DeviceCallsResponse,
  type CallTransport,
} from "./callTypes";

const POLL_MS = 2000;

export function useDeviceCalls(deviceId: string, enabled = true) {
  const { t } = useI18n();
  const [transport, setTransport] = useState<CallTransport>("cellular");
  const [audioAvailable, setAudioAvailable] = useState(false);
  const [calls, setCalls] = useState<DeviceCall[]>([]);
  const [busy, setBusy] = useState("");
  const deviceIdRef = useRef(deviceId);
  deviceIdRef.current = deviceId;

  const load = useCallback(async () => {
    const id = deviceIdRef.current;
    if (!id) return;
    try {
      const data = await api<DeviceCallsResponse>(`/devices/${encodeURIComponent(id)}/calls`);
      if (deviceIdRef.current !== id) return;
      setTransport(data.transport === "vowifi" ? "vowifi" : "cellular");
      setAudioAvailable(!!data.audioAvailable);
      setCalls(data.calls || []);
    } catch {
      if (deviceIdRef.current !== id) return;
    }
  }, []);

  useEffect(() => {
    setCalls([]);
    if (!enabled || !deviceId) return;
    void load();
    const timer = window.setInterval(() => void load(), POLL_MS);
    return () => window.clearInterval(timer);
  }, [deviceId, enabled, load]);

  const act = useCallback(async (action: "dial" | "answer" | "hangup" | "dtmf", body: Record<string, unknown>) => {
    const id = deviceIdRef.current;
    if (!id) return;
    setBusy(action);
    try {
      await api(`/devices/${encodeURIComponent(id)}/calls/${action}`, { method: "POST", body });
      await load();
    } catch (err) {
      message.error(apiMessage(err) || t("通话操作失败"));
    } finally {
      setBusy("");
    }
  }, [load, t]);

  const dial = useCallback((number: string) => act("dial", { number, durationSeconds: 0 }), [act]);
  const answer = useCallback((callId: string) => act("answer", { callId }), [act]);
  const hangup = useCallback((callId: string) => act("hangup", { callId }), [act]);
  const dtmf = useCallback((callId: string, digits: string) => act("dtmf", { callId, digits }), [act]);

  const live = useMemo(() => pickLiveCall(calls), [calls]);
  const incoming = useMemo(() => pickIncomingCall(calls), [calls]);

  return {
    transport,
    audioAvailable,
    calls,
    live,
    incoming,
    busy,
    refresh: load,
    dial,
    answer,
    hangup,
    dtmf,
  };
}
