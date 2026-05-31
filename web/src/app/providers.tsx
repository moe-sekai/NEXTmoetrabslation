"use client";

import { ThemeProvider } from "next-themes";
import React, { createContext, useCallback, useContext, useRef, useState } from "react";

// ---- Theme + toast providers ----

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <ThemeProvider attribute="data-theme" defaultTheme="system" enableSystem>
      <ToastProvider>{children}</ToastProvider>
    </ThemeProvider>
  );
}

// ---- Toast ----

type Toast = { id: number; msg: string; type: "ok" | "err" };
type ToastCtx = { show: (msg: string, type?: "ok" | "err") => void };

const ToastContext = createContext<ToastCtx>({ show: () => {} });

export function useToast() {
  return useContext(ToastContext);
}

function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const nextId = useRef(0);
  const show = useCallback((msg: string, type: "ok" | "err" = "ok") => {
    const id = ++nextId.current;
    setToasts((p) => [...p, { id, msg, type }]);
    setTimeout(() => setToasts((p) => p.filter((t) => t.id !== id)), 3200);
  }, []);
  return (
    <ToastContext.Provider value={{ show }}>
      {children}
      <div className="toast-stack">
        {toasts.map((t) => (
          <div key={t.id} className={`toast ${t.type}`}>
            {t.msg}
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}
