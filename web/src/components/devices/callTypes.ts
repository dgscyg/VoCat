export type CallTransport = "vowifi" | "cellular";

export type CallState = "dialing" | "ringing" | "active" | "held" | "ended" | "failed" | string;

export interface DeviceCall {
  id: string;
  number: string;
  direction: "incoming" | "outgoing" | string;
  state: CallState;
  mediaReady?: boolean;
  codec?: string;
  sipCode?: number;
  reason?: string;
  startedAt?: string;
  endedAt?: string;
}

export interface DeviceCallsResponse {
  deviceId?: string;
  transport?: CallTransport;
  audioAvailable?: boolean;
  calls?: DeviceCall[];
}

export function isLiveCall(call: DeviceCall): boolean {
  return call.state !== "ended" && call.state !== "failed";
}

export function isIncomingRinging(call: DeviceCall): boolean {
  return call.direction === "incoming" && call.state === "ringing";
}

export function pickLiveCall(calls: DeviceCall[]): DeviceCall | null {
  return calls.find((call) => isLiveCall(call)) || null;
}

export function pickIncomingCall(calls: DeviceCall[]): DeviceCall | null {
  return calls.find((call) => isIncomingRinging(call)) || null;
}

export function sanitizeDialNumber(value: string): string {
  const trimmed = value.trim();
  const international = trimmed.startsWith("+");
  const body = (international ? trimmed.slice(1) : trimmed).replace(/[^\d*#]/g, "");
  const limit = international ? 31 : 32;
  return (international ? "+" : "") + body.slice(0, limit);
}
