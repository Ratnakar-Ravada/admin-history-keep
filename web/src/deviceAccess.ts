import type { QueryClient } from "@tanstack/react-query";
import type { Device } from "./types";

export const accessChangedEvent = "location-keeper:access-changed";

export function deviceUnlocked(device: Device) {
  return device.locked === false && device.pin_setup_required === false;
}

export function unlockedSelection(ids: string[], devices: Device[]) {
  const allowed = new Set(
    devices.filter(deviceUnlocked).map((device) => device.device_id),
  );
  return [...new Set(ids)].filter((id) => allowed.has(id));
}

export function clearPrivateQueries(client: QueryClient, keepDevices = false) {
  const predicate = (query: { queryKey: readonly unknown[] }) =>
    query.queryKey[0] !== "session" &&
    (!keepDevices || query.queryKey[0] !== "devices");
  void client.cancelQueries({ predicate });
  client.removeQueries({ predicate });
}

export function validDevicePIN(pin: string) {
  return pin.length === 4 && /^[0-9]{4}$/.test(pin);
}
