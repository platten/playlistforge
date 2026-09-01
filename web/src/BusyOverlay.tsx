import { KeyboardEvent, useEffect, useRef } from "react";
import type { Job } from "./types";
import { useDelayedBusy } from "./hooks";

export function BusyOverlay({
  job,
  onCancel,
}: {
  job: Job | null;
  onCancel: () => void;
}) {
  const active = Boolean(
    job && (job.status === "queued" || job.status === "running"),
  );
  const visible = useDelayedBusy(active);
  const cancelRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    // The dialog has one interactive control. Moving focus there and trapping
    // Tab makes the modal behavior explicit for keyboard and screen-reader users.
    if (visible) cancelRef.current?.focus();
  }, [visible]);
  if (!visible || !job) return null;
  function keepFocus(event: KeyboardEvent) {
    if (event.key === "Tab") {
      event.preventDefault();
      cancelRef.current?.focus();
    } else if (event.key === "Escape") {
      onCancel();
    }
  }
  return (
    <div
      className="overlay"
      role="dialog"
      aria-modal="true"
      aria-labelledby="busy-title"
      onKeyDown={keepFocus}
    >
      <div className="busy-card">
        <div className="spinner" aria-hidden="true" />
        <p className="eyebrow">Working on your playlist</p>
        <h2 id="busy-title">{job.phase}</h2>
        <p>
          Research-heavy playlists can take a minute or two. You can leave this
          window open.
        </p>
        <button
          ref={cancelRef}
          className="button secondary"
          type="button"
          onClick={onCancel}
        >
          Cancel operation
        </button>
      </div>
    </div>
  );
}
