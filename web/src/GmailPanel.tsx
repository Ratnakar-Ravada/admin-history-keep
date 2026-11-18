import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Mail, Paperclip, ShieldCheck } from "lucide-react";
import { api } from "./api";
type EmailStatus = {
  enabled: boolean;
  connected: boolean;
  can_connect: boolean;
  sender_email: string;
  reconnect_required?: boolean;
};
export function GmailPanel() {
  const [returnedFailure] = useState(
    () => new URLSearchParams(location.search).get("email") === "failed",
  );
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const status = useQuery({
    queryKey: ["email-status"],
    queryFn: ({ signal }) => api<EmailStatus>("/api/email/status", { signal }),
    refetchOnWindowFocus: false,
  });
  const action = async (disconnect = false) => {
    setBusy(true);
    setError("");
    try {
      if (disconnect) {
        await api("/api/email/disconnect", { method: "POST" });
        await status.refetch();
      } else {
        const result = await api<{ authorization_url: string }>(
          "/api/email/connect",
          { method: "POST" },
        );
        location.assign(result.authorization_url);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "Unable to connect Gmail.");
    } finally {
      setBusy(false);
    }
  };
  const state = status.data;
  return (
    <section className="gmail-panel" aria-label="Email sender">
      <div className="gmail-heading">
        <Mail size={23} />
        <div>
          <h3>Email delivery</h3>
          <p>Connect the Google account that will send your alerts.</p>
        </div>
      </div>
      {status.isPending ? (
        <p role="status">Checking sender…</p>
      ) : status.error ? (
        <p role="alert">
          Unable to check the email sender.{" "}
          <button onClick={() => status.refetch()}>Retry</button>
        </p>
      ) : (
        <>
          <p className="gmail-status">
            {state?.connected ? (
              <>
                Sender connected · <strong>{state.sender_email}</strong>
              </>
            ) : !state?.enabled ? (
              "Gmail setup is not configured yet."
            ) : state.reconnect_required ? (
              "Reconnect Gmail to renew sender access."
            ) : (
              <>
                Ready to connect · <strong>{state.sender_email}</strong>
              </>
            )}
          </p>
          <div className="gmail-capabilities">
            <span>
              <ShieldCheck size={14} /> Send permission only
            </span>
            <span>
              <Paperclip size={14} /> Attachments
            </span>
            <span>HTML cards with links</span>
          </div>
          {state?.can_connect && (
            <div className="gmail-buttons">
              <button
                className="primary"
                disabled={busy}
                onClick={() => action()}
              >
                {busy
                  ? "Please wait…"
                  : state.connected || state.reconnect_required
                    ? "Reconnect Gmail"
                    : "Connect Gmail"}
              </button>
              {state.connected && (
                <button
                  className="text-button"
                  disabled={busy}
                  onClick={() => action(true)}
                >
                  Disconnect sender
                </button>
              )}
            </div>
          )}
          {state?.enabled && !state.can_connect && (
            <p>
              Sign in with the configured sender account to connect or
              disconnect Gmail.
            </p>
          )}
          {!state?.enabled && (
            <p>
              Configure the Gmail sender in the server environment after Google
              sign-in is ready.
            </p>
          )}
        </>
      )}
      {returnedFailure && (
        <p role="alert">
          Gmail connection could not be completed. Choose the configured sender
          account and allow email sending.
        </p>
      )}
      {error && <p role="alert">{error}</p>}
      <p className="gmail-note">
        Connecting Gmail does not send a message. Automated alerts remain coming
        soon.
      </p>
    </section>
  );
}
