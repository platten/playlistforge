/**
 * Tests for useDelayedBusy: no visible state for sub-threshold work, a visible
 * state once the delay elapses, and an immediate reset the moment work stops.
 */
import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useDelayedBusy } from "./hooks";

describe("useDelayedBusy", () => {
  afterEach(() => vi.useRealTimers());
  it("waits three seconds and resets when work finishes", () => {
    vi.useFakeTimers();
    const { result, rerender } = renderHook(
      ({ active }) => useDelayedBusy(active),
      { initialProps: { active: true } },
    );
    expect(result.current).toBe(false);
    act(() => vi.advanceTimersByTime(2999));
    expect(result.current).toBe(false);
    act(() => vi.advanceTimersByTime(1));
    expect(result.current).toBe(true);
    rerender({ active: false });
    expect(result.current).toBe(false);
  });
});
