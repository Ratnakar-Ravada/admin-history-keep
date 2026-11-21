export type HistoryKind = "calls" | "notifications" | "app-usage";
export type HistoryRecord = {
  event_id: string;
  device_id: string;
  recorded_at: string;
  observed_at: string;
  received_at: string;
  event_type: string;
  source_package?: string;
  call_id?: string;
  source_id?: string;
  source_key?: string;
  fields: Record<string, string | null>;
};
export type HistoryPageData = {
  items: HistoryRecord[];
  page_cursor?: string | null;
  expires_at?: string;
  next_cursor: string | null;
  has_more: boolean;
  as_of: string;
};
export const callTypes: Record<string, string> = {
  "1": "Incoming",
  "2": "Outgoing",
  "3": "Missed",
  "4": "Voicemail",
  "5": "Rejected",
  "6": "Blocked",
  "7": "Answered elsewhere",
};
export function duration(
  value: string | null | undefined,
  milliseconds = true,
) {
  if (value == null || value.trim() === "") return "Not recorded";
  const number = Number(value);
  if (!Number.isFinite(number) || number < 0) return "Not recorded";
  const total = Math.floor(number / (milliseconds ? 1000 : 1));
  if (!total) return number > 0 ? "<1s" : "0s";
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const seconds = total % 60;
  return [
    hours && `${hours}h`,
    minutes && `${minutes}m`,
    seconds && `${seconds}s`,
  ]
    .filter(Boolean)
    .join(" ");
}
export function callNumber(fields: HistoryRecord["fields"]) {
  const presentation: Record<string, string> = {
    "2": "Private number",
    "3": "Unknown number",
    "4": "Payphone",
    "5": "Number unavailable",
  };
  return (
    fields.formatted_number ||
    fields.number ||
    fields.normalized_number ||
    presentation[fields.presentation ?? ""] ||
    "Number not provided"
  );
}
export type NotificationMessage = {
  text: string | null;
  sender: string | null;
  time: number | null;
};
export function notificationMessages(
  value: string | null | undefined,
): NotificationMessage[] {
  try {
    const parsed: unknown = JSON.parse(value || "[]");
    if (!Array.isArray(parsed)) return [];
    return parsed.slice(-10).flatMap((item: unknown) => {
      if (!item || typeof item !== "object") return [];
      const row = item as Record<string, unknown>;
      return [
        {
          text: typeof row.text === "string" ? row.text : null,
          sender: typeof row.sender === "string" ? row.sender : null,
          time:
            typeof row.time === "number" &&
            Number.isFinite(row.time) &&
            !Number.isNaN(new Date(row.time).valueOf())
              ? row.time
              : null,
        },
      ];
    });
  } catch {
    return [];
  }
}
