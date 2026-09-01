import { useEffect, useState } from "react";

export function useDelayedBusy(active: boolean, delay = 3000): boolean {
  const [visible, setVisible] = useState(false);
  useEffect(() => {
    if (!active) {
      // Reset immediately so a completed job can never leave a stale overlay.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setVisible(false);
      return;
    }
    const timer = window.setTimeout(() => setVisible(true), delay);
    return () => window.clearTimeout(timer);
  }, [active, delay]);
  return visible;
}
