import { useEffect, useState } from "react";

/**
 * Returns `true` only once `active` has held for `delay` ms, and `false`
 * immediately when it drops. This keeps the busy overlay from flashing for
 * operations that finish quickly while still covering the long ones. The
 * default 3s matches the point past which a blank wait feels unresponsive.
 */
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
