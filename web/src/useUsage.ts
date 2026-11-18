import { useEffect, useRef } from "react";
import { api } from "./api";
let lastInput = Date.now();
if (typeof window !== "undefined")
  for (const name of ["pointerdown", "keydown", "wheel", "touchstart"]) {
    window.addEventListener(
      name,
      () => {
        lastInput = Date.now();
      },
      { passive: true },
    );
  }
export function useUsage(enabled: boolean, view: string) {
  const previousView = useRef<string | null>(null);
  useEffect(() => {
    if (!enabled) {
      previousView.current = null;
      return;
    }
    if (previousView.current === view) return;
    previousView.current = view;
    void api("/api/usage/view", {
      method: "POST",
      source: "background",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ view }),
    }).catch(() => {});
  }, [enabled, view]);
  useEffect(() => {
    if (!enabled) return;
    let previousHeartbeat = 0;
    const tick = () => {
      if (document.visibilityState !== "visible") return;
      const now = Date.now();
      const active = lastInput > previousHeartbeat && now - lastInput < 300000;
      previousHeartbeat = now;
      void api("/api/usage/heartbeat", {
        method: "POST",
        source: "background",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ active }),
      }).catch(() => {});
    };
    const id = setInterval(tick, 30000);
    return () => clearInterval(id);
  }, [enabled]);
}
