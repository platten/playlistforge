import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { BusyOverlay } from "./BusyOverlay";

describe("BusyOverlay", () => {
  afterEach(() => vi.useRealTimers());
  it("appears for slow operations and supports cancellation", () => {
    vi.useFakeTimers();
    const cancel = vi.fn();
    render(
      <BusyOverlay
        job={{ id: "1", status: "running", phase: "Researching" }}
        onCancel={cancel}
      />,
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    act(() => vi.advanceTimersByTime(3000));
    expect(screen.getByRole("dialog")).toHaveTextContent("Researching");
    const button = screen.getByRole("button", { name: /cancel/i });
    expect(button).toHaveFocus();
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Tab" });
    expect(button).toHaveFocus();
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    fireEvent.click(button);
    expect(cancel).toHaveBeenCalledTimes(2);
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
