import { KeyboardEvent, MouseEvent, useEffect, useRef } from "react";

interface ApiKeyHelpDialogProps {
  open: boolean;
  onClose: () => void;
}

// Keep credential-onboarding guidance in a focused component so keyboard
// behavior, official links, and security wording evolve together.
export function ApiKeyHelpDialog({ open, onClose }: ApiKeyHelpDialogProps) {
  const panelRef = useRef<HTMLElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  const previousFocus = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!open) return;
    previousFocus.current = document.activeElement as HTMLElement | null;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    closeRef.current?.focus();
    return () => {
      document.body.style.overflow = previousOverflow;
      previousFocus.current?.focus();
    };
  }, [open]);

  if (!open) return null;

  function handleKeyDown(event: KeyboardEvent) {
    if (event.key === "Escape") {
      event.preventDefault();
      onClose();
      return;
    }
    if (event.key !== "Tab") return;
    // The handler is attached to the mounted panel, whose markup always has
    // the close and confirmation buttons.
    const controls = Array.from(
      panelRef.current!.querySelectorAll<HTMLElement>(
        "a[href], button:not([disabled])",
      ),
    );
    const first = controls[0];
    const last = controls[controls.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  function closeFromBackdrop(event: MouseEvent<HTMLDivElement>) {
    if (event.target === event.currentTarget) onClose();
  }

  return (
    <div className="help-overlay" onMouseDown={closeFromBackdrop}>
      <section
        ref={panelRef}
        className="help-dialog card"
        role="dialog"
        aria-modal="true"
        aria-labelledby="api-key-help-title"
        aria-describedby="api-key-help-summary"
        onKeyDown={handleKeyDown}
      >
        <button
          ref={closeRef}
          className="dialog-close"
          type="button"
          onClick={onClose}
          aria-label="Close API key help"
        >
          ×
        </button>
        <p className="eyebrow">OpenAI Platform setup</p>
        <h2 id="api-key-help-title">Get an OpenAI API key</h2>
        <p id="api-key-help-summary">
          API access and billing are managed in the OpenAI Platform. Complete
          these steps, then return here with the new secret key.
        </p>
        <ol>
          <li>
            <strong>Create or sign in to your account.</strong>
            <span>
              Open the official{" "}
              <a
                href="https://platform.openai.com/signup"
                target="_blank"
                rel="noopener noreferrer"
              >
                OpenAI Platform signup page
              </a>
              .
            </span>
          </li>
          <li>
            <strong>Connect API billing.</strong>
            <span>
              Visit{" "}
              <a
                href="https://platform.openai.com/settings/organization/billing/overview"
                target="_blank"
                rel="noopener noreferrer"
              >
                Billing settings
              </a>{" "}
              and add a payment method or credits when prompted. API calls are
              charged according to usage.
            </span>
          </li>
          <li>
            <strong>Generate a secret key.</strong>
            <span>
              Open the{" "}
              <a
                href="https://platform.openai.com/api-keys"
                target="_blank"
                rel="noopener noreferrer"
              >
                API keys page
              </a>
              , choose <em>Create new secret key</em>, name it “Playlist Forge,”
              and copy the generated value.
            </span>
          </li>
          <li>
            <strong>Paste and save it below.</strong>
            <span>
              Playlist Forge validates model access, then stores the key in your
              operating system’s credential store when available.
            </span>
          </li>
        </ol>
        <p className="security-note">
          Treat the API key like a password. Never share it, commit it to source
          control, or paste it anywhere except a trusted credential field. You
          can revoke it from the API keys page at any time.
        </p>
        <button className="button primary" type="button" onClick={onClose}>
          Got it
        </button>
      </section>
    </div>
  );
}
