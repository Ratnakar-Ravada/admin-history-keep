import { useId, useState, type FormEvent } from "react";
import { LockKeyhole } from "lucide-react";
import { api, APIError } from "./api";
import { validDevicePIN } from "./deviceAccess";
import type { Device } from "./types";

export function DeviceUnlock({
  device,
  onUnlocked,
}: {
  device: Device;
  onUnlocked: (id: string) => Promise<void>;
}) {
  const id = useId();
  const [pin, setPIN] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (pending) return;
    if (!validDevicePIN(pin)) {
      setError("Enter the four-digit PIN set in the mobile app.");
      return;
    }
    setPending(true);
    setError("");
    try {
      await api("/api/devices/unlock", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_id: device.device_id, pin }),
      });
      setPIN("");
      await onUnlocked(device.device_id);
    } catch (cause) {
      setPIN("");
      setError(
        cause instanceof APIError
          ? cause.code === "incorrect_pin"
            ? "Incorrect PIN. Try again."
            : cause.code === "pin_rate_limited"
              ? "Too many attempts. Wait a few minutes before trying again."
              : cause.code === "pin_verification_unavailable"
                ? "PIN verification is unavailable. Please try again later."
                : cause.code === "invalid_pin"
                  ? "Enter the four-digit PIN set in the mobile app."
                  : "Device access could not be verified. Refresh your devices and try again."
          : "Unable to unlock this device. Check your connection and try again.",
      );
    } finally {
      setPending(false);
    }
  };

  return (
    <section className="device-unlock" aria-labelledby={`${id}-name`}>
      <div className="device-unlock-heading">
        <LockKeyhole size={17} aria-hidden="true" />
        <div>
          <h3 id={`${id}-name`}>{device.device_name}</h3>
          <p>{device.detected_device_name}</p>
          <span>
            {device.pin_setup_required ? "PIN setup needed" : "Locked"}
          </span>
        </div>
      </div>
      {device.pin_setup_required ? (
        <p className="pin-setup-note">
          Open the mobile app on this device to set up a four-digit PIN, then
          refresh your devices here.
        </p>
      ) : (
        <form onSubmit={submit} aria-label={`Unlock ${device.device_name}`}>
          <label htmlFor={`${id}-pin`}>Device PIN</label>
          <div className="pin-input-row">
            <input
              id={`${id}-pin`}
              type="password"
              inputMode="numeric"
              autoComplete="off"
              pattern="[0-9]{4}"
              maxLength={4}
              required
              value={pin}
              onChange={(event) => {
                setPIN(event.target.value);
                setError("");
              }}
              disabled={pending}
              aria-describedby={error ? `${id}-error` : `${id}-help`}
              aria-invalid={!!error}
            />
            <button className="small-primary" disabled={pending} type="submit">
              {pending ? "Unlocking…" : "Unlock"}
            </button>
          </div>
          <p id={`${id}-help`} className="pin-help">
            Enter the four-digit PIN from this device’s mobile app.
          </p>
          {error && (
            <p id={`${id}-error`} className="pin-error" role="alert">
              {error}
            </p>
          )}
        </form>
      )}
      <p className="pin-recovery-note">
        Forgot PIN? Email recovery is not available yet. You can update your PIN
        in the mobile app.
      </p>
    </section>
  );
}
