"use client";

import { useEffect, useRef } from "react";
import { getToken } from "./api";

// SSE event names (mirror the Go sse package constants).
export type SSEEvent =
  | "entry.updated"
  | "eventstory.updated"
  | "sync.progress"
  | "translate.progress"
  | "backup.status"
  | "upstream.status"
  | "ping";

export type SSEHandler = (event: SSEEvent, data: unknown) => void;

const SSE_BASE = process.env.NEXT_PUBLIC_API_BASE
  ? process.env.NEXT_PUBLIC_API_BASE.replace(/\/api$/, "")
  : "";

/**
 * useSSE subscribes to the backend event stream while mounted. EventSource
 * cannot set Authorization headers, so the JWT is passed as a query param
 * (the backend accepts ?token= for /sse). Reconnection is handled by the
 * browser; we recreate the source if the token changes.
 */
export function useSSE(handler: SSEHandler, enabled: boolean) {
  const handlerRef = useRef(handler);
  handlerRef.current = handler;

  useEffect(() => {
    if (!enabled) return;
    const token = getToken();
    if (!token) return;

    const url = `${SSE_BASE}/sse?token=${encodeURIComponent(token)}`;
    const es = new EventSource(url);

    const events: SSEEvent[] = [
      "entry.updated", "eventstory.updated", "sync.progress",
      "translate.progress", "backup.status", "upstream.status", "ping",
    ];
    const listeners: Array<[string, (e: MessageEvent) => void]> = [];
    for (const name of events) {
      const fn = (e: MessageEvent) => {
        let data: unknown = e.data;
        try {
          data = JSON.parse(e.data);
        } catch {
          /* leave as string */
        }
        handlerRef.current(name, data);
      };
      es.addEventListener(name, fn);
      listeners.push([name, fn]);
    }

    return () => {
      for (const [name, fn] of listeners) es.removeEventListener(name, fn);
      es.close();
    };
  }, [enabled]);
}
