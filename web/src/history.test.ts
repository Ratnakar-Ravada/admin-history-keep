import { describe, expect, it } from "vitest";
import { callNumber, duration, notificationMessages } from "./history";

describe("history values from Android", () => {
  it("distinguishes call seconds from usage milliseconds and preserves zero", () => {
    expect(duration("3661", false)).toBe("1h 1m 1s");
    expect(duration("3661000")).toBe("1h 1m 1s");
    expect(duration("0", false)).toBe("0s");
    expect(duration("700")).toBe("<1s");
    for (const value of [null, undefined, "", " ", "-1", "NaN", "Infinity"]) {
      expect(duration(value)).toBe("Not recorded");
    }
  });
  it("falls back to available number metadata and restricted presentation", () => {
    expect(
      callNumber({ number: "+15550100", formatted_number: "+1 555 0100" }),
    ).toBe("+1 555 0100");
    expect(callNumber({ number: null, normalized_number: "+15550100" })).toBe(
      "+15550100",
    );
    expect(callNumber({ number: null, presentation: "2" })).toBe(
      "Private number",
    );
    expect(callNumber({})).toBe("Number not provided");
  });
  it("reads JSON-encoded notification messages without inventing redacted text", () => {
    expect(
      notificationMessages(
        '[{"text":null,"sender":null,"time":1760000000000}]',
      ),
    ).toEqual([{ text: null, sender: null, time: 1760000000000 }]);
    for (const value of [null, "", "broken", '{"text":"not an array"}']) {
      expect(notificationMessages(value)).toEqual([]);
    }
    expect(
      notificationMessages(
        '[null,4,{"text":{"html":"<script>"},"sender":7,"time":1e100}]',
      ),
    ).toEqual([{ text: null, sender: null, time: null }]);
    const messages = Array.from({ length: 13 }, (_, index) => ({
      text: String(index),
    }));
    expect(notificationMessages(JSON.stringify(messages))).toHaveLength(10);
    expect(notificationMessages(JSON.stringify(messages))[0].text).toBe("3");
  });
});
