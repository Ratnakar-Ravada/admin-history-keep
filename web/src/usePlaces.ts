import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import type { Event, Place } from "./types";
export function usePlaces(events: Event[]) {
  const ids = [
    ...new Set(
      events
        .filter((e) => e.latitude !== null && !e.place)
        .map((e) => e.event_id),
    ),
  ]
    .sort()
    .slice(0, 200);
  return useQuery({
    queryKey: ["places", ids],
    enabled: ids.length > 0,
    queryFn: async ({ signal }) => {
      const items: Record<string, { status: string; place?: Place }> = {};
      for (let i = 0; i < ids.length; i += 100) {
        const result = await api<{ items: typeof items }>(
          "/api/places/resolve",
          {
            method: "POST",
            signal,
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ event_ids: ids.slice(i, i + 100) }),
          },
        );
        Object.assign(items, result.items);
      }
      return { items };
    },
    staleTime: 60000,
    gcTime: 60000,
    retry: 1,
    refetchInterval: (query) => {
      if (query.state.dataUpdateCount >= 10) return false;
      return Object.values(query.state.data?.items ?? {}).some(
        (v) => v.status === "pending" || v.status === "busy",
      )
        ? 1500
        : false;
    },
    refetchOnWindowFocus: false,
  });
}
