/**
 * Tests for the delayed busy dialog: it stays hidden for fast work, appears
 * only after the three-second threshold, moves focus to the Cancel button and
 * traps Tab there, freezes and restores page scroll, and cancels on both
 * Escape and a click.
 */
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { BusyOverlay } from "./BusyOverlay";

describe("BusyOverlay", () => {
  afterEach(() => vi.useRealTimers());
  it("appears for slow operations, locks page scroll, and supports cancellation", () => {
    vi.useFakeTimers();
    const cancel = vi.fn();
    const { unmount } = render(
      <BusyOverlay
        job={{ id: "1", status: "running", phase: "Researching" }}
        onCancel={cancel}
      />,
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(document.body.style.overflow).toBe("");
    act(() => vi.advanceTimersByTime(3000));
    expect(screen.getByRole("dialog")).toHaveTextContent("Researching");
    expect(document.body.style.overflow).toBe("hidden");
    const button = screen.getByRole("button", { name: /cancel/i });
    expect(button).toHaveFocus();
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Tab" });
    expect(button).toHaveFocus();
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    fireEvent.click(button);
    expect(cancel).toHaveBeenCalledTimes(2);
    unmount();
    expect(document.body.style.overflow).toBe("");
  });

  it("stays absent without an active job", () => {
    const { rerender } = render(
      <BusyOverlay job={null} onCancel={() => undefined} />,
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    rerender(
      <BusyOverlay
        job={{ id: "1", status: "succeeded", phase: "Complete" }}
        onCancel={() => undefined}
      />,
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
