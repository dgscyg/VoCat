import { useEffect, useRef, useState } from "react";

const PCM_RATE = 8000;
const FRAME_SAMPLES = 160;

export type MediaStatus = "idle" | "connecting" | "live" | "blocked" | "error";

function resample(input: Float32Array, fromRate: number, toRate: number): Float32Array {
  if (fromRate === toRate || input.length === 0) return input;
  const ratio = fromRate / toRate;
  const length = Math.max(1, Math.floor(input.length / ratio));
  const output = new Float32Array(length);
  for (let index = 0; index < length; index += 1) {
    const source = index * ratio;
    const left = Math.min(Math.floor(source), input.length - 1);
    const right = Math.min(left + 1, input.length - 1);
    const mix = source - left;
    output[index] = input[left] * (1 - mix) + input[right] * mix;
  }
  return output;
}

function floatToPcm16(samples: Float32Array): ArrayBuffer {
  const bytes = new ArrayBuffer(samples.length * 2);
  const view = new DataView(bytes);
  for (let index = 0; index < samples.length; index += 1) {
    const clipped = Math.max(-1, Math.min(1, samples[index]));
    view.setInt16(index * 2, clipped < 0 ? clipped * 0x8000 : clipped * 0x7fff, true);
  }
  return bytes;
}

function pcm16ToFloat(buffer: ArrayBuffer): Float32Array {
  const view = new DataView(buffer);
  const count = Math.floor(buffer.byteLength / 2);
  const output = new Float32Array(count);
  for (let index = 0; index < count; index += 1) {
    const sample = view.getInt16(index * 2, true);
    output[index] = sample / (sample < 0 ? 0x8000 : 0x7fff);
  }
  return output;
}

export function useCallMedia(deviceId: string, callId: string, enabled: boolean, muted: boolean) {
  const [status, setStatus] = useState<MediaStatus>("idle");
  const [error, setError] = useState("");
  const mutedRef = useRef(muted);
  mutedRef.current = muted;

  useEffect(() => {
    if (!enabled || !deviceId || !callId) {
      setStatus("idle");
      setError("");
      return;
    }
    if (!window.isSecureContext && location.hostname !== "localhost" && location.hostname !== "127.0.0.1") {
      setStatus("blocked");
      setError("secure_context");
      return;
    }

    let closed = false;
    let socket: WebSocket | null = null;
    let context: AudioContext | null = null;
    let processor: ScriptProcessorNode | null = null;
    let source: MediaStreamAudioSourceNode | null = null;
    let stream: MediaStream | null = null;
    const playback: number[] = [];
    const capture: number[] = [];
    setStatus("connecting");
    setError("");

    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${protocol}//${location.host}/api/devices/${encodeURIComponent(deviceId)}/calls/media?call_id=${encodeURIComponent(callId)}`;

    const start = async () => {
      try {
        stream = await navigator.mediaDevices.getUserMedia({
          audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true, channelCount: 1 },
          video: false,
        });
      } catch {
        if (!closed) setError("microphone");
      }
      if (closed) {
        stream?.getTracks().forEach((track) => track.stop());
        return;
      }

      const AudioCtx = window.AudioContext || (window as typeof window & { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
      if (!AudioCtx) {
        setStatus("error");
        setError("audio_context");
        stream?.getTracks().forEach((track) => track.stop());
        return;
      }
      context = new AudioCtx();
      if (context.state === "suspended") {
        try {
          await context.resume();
        } catch {
          /* continue; playback may still work after a later gesture */
        }
      }
      const nativeRate = context.sampleRate || 48000;
      processor = context.createScriptProcessor(2048, 1, 1);
      if (stream) source = context.createMediaStreamSource(stream);
      processor.onaudioprocess = (event) => {
        const input = event.inputBuffer.getChannelData(0);
        const output = event.outputBuffer.getChannelData(0);
        if (!mutedRef.current) {
          const down = resample(input, nativeRate, PCM_RATE);
          for (let index = 0; index < down.length; index += 1) capture.push(down[index]);
        }
        if (capture.length > PCM_RATE) capture.splice(0, capture.length - PCM_RATE);
        while (capture.length >= FRAME_SAMPLES && socket?.readyState === WebSocket.OPEN) {
          const frame = Float32Array.from(capture.splice(0, FRAME_SAMPLES));
          socket.send(floatToPcm16(frame));
        }
        const needed = output.length;
        const take = Math.max(1, Math.ceil((needed * PCM_RATE) / nativeRate));
        if (playback.length >= take) {
          const chunk = resample(Float32Array.from(playback.splice(0, take)), PCM_RATE, nativeRate);
          output.set(chunk.subarray(0, needed));
        } else {
          output.fill(0);
        }
        if (playback.length > PCM_RATE) playback.splice(0, playback.length - PCM_RATE);
      };
      if (source) source.connect(processor);
      processor.connect(context.destination);

      socket = new WebSocket(url);
      socket.binaryType = "arraybuffer";
      socket.onopen = () => {
        if (!closed) setStatus("live");
      };
      socket.onmessage = (event) => {
        if (typeof event.data === "string" || closed) return;
        const samples = pcm16ToFloat(event.data as ArrayBuffer);
        for (let index = 0; index < samples.length; index += 1) playback.push(samples[index]);
      };
      socket.onerror = () => {
        if (!closed) {
          setStatus("error");
          setError("socket");
        }
      };
      socket.onclose = () => {
        if (!closed) setStatus((current) => (current === "error" || current === "blocked" ? current : "idle"));
      };
    };

    void start();
    return () => {
      closed = true;
      socket?.close();
      try {
        processor?.disconnect();
        source?.disconnect();
      } catch {
        /* already torn down */
      }
      void context?.close();
      stream?.getTracks().forEach((track) => track.stop());
    };
  }, [deviceId, callId, enabled]);

  return { status, error };
}
