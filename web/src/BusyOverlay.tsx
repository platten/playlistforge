import { KeyboardEvent, useEffect, useRef } from "react";
import type { Job } from "./types";
import { useDelayedBusy } from "./hooks";

export function BusyOverlay({
  job,
  onCancel,
  immediate = false,
}: {
  job: Job | null;
  onCancel: () => void;
  immediate?: boolean;
}) {
  const active = Boolean(
    job && (job.status === "queued" || job.status === "running"),
  );
  const visible = useDelayedBusy(active, immediate ? 0 : 3000);
  const cancelRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!visible) return;
    // The dialog has one interactive control. Moving focus there and trapping
    // Tab makes the modal behavior explicit for keyboard and screen-reader users.
    cancelRef.current?.focus();
    // Freeze the page behind the overlay so its scrollbar cannot appear beside
    // the centered card while a long operation runs.
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = previousOverflow;
    };
  }, [visible]);
  if (!visible || !job) return null;
  const total = job.total ?? 0;
  const done = Math.min(job.completed ?? 0, total);
  const percent = total > 0 ? Math.round((done / total) * 100) : 0;
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
        <div className="eq" aria-hidden="true">
          <span />
          <span />
          <span />
          <span />
          <span />
        </div>
        <p className="eyebrow">
          {total > 0 ? "Syncing" : "Working on your playlist"}
        </p>
        <h2 id="busy-title">{job.phase}</h2>
        {total > 0 ? (
          <>
            <div
              className="busy-progress"
              role="progressbar"
              aria-valuemin={0}
              aria-valuemax={total}
              aria-valuenow={done}
            >
              <div
                className="busy-progress-fill"
                style={{ width: `${percent}%` }}
              />
            </div>
            <p>
              {done} of {total} playlists · you can leave this window open
            </p>
          </>
        ) : (
          <p>
            Research-heavy playlists can take a minute or two. You can leave
            this window open.
          </p>
        )}
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
