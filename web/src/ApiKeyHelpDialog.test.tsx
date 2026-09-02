/**
 * Tests for the API-key help modal: it renders nothing while closed; when open
 * it keeps focus inside the panel, and on close it restores both the previously
 * focused element and the page scroll lock. Escape, the close button, and a
 * backdrop click all dismiss it.
 */
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ApiKeyHelpDialog } from "./ApiKeyHelpDialog";

describe("ApiKeyHelpDialog", () => {
  it("renders nothing while closed", () => {
    render(<ApiKeyHelpDialog open={false} onClose={vi.fn()} />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("traps focus, handles dismissal methods, and restores page state", () => {
    const trigger = document.createElement("button");
    document.body.appendChild(trigger);
    trigger.focus();
    const onClose = vi.fn();
    const view = render(<ApiKeyHelpDialog open onClose={onClose} />);
    const dialog = screen.getByRole("dialog");
    const close = screen.getByRole("button", { name: "Close API key help" });
    const done = screen.getByRole("button", { name: "Got it" });

    expect(close).toHaveFocus();
    expect(document.body.style.overflow).toBe("hidden");
    fireEvent.keyDown(dialog, { key: "Tab", shiftKey: true });
    expect(done).toHaveFocus();
    fireEvent.keyDown(dialog, { key: "Tab" });
    expect(close).toHaveFocus();
    const billing = screen.getByRole("link", { name: "Billing settings" });
    billing.focus();
    fireEvent.keyDown(dialog, { key: "Tab", shiftKey: true });
    expect(billing).toHaveFocus();
    fireEvent.keyDown(dialog, { key: "ArrowDown" });
    fireEvent.mouseDown(dialog);
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.mouseDown(dialog.parentElement as HTMLElement);
    fireEvent.click(close);
    fireEvent.click(done);
    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(4);

    view.unmount();
    expect(document.body.style.overflow).toBe("");
    expect(trigger).toHaveFocus();
    trigger.remove();
  });
});
