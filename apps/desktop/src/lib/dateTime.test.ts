import { describe, expect, it } from "vitest";
import {
  formatFullTime,
  formatMessageTime,
  formatSessionTime,
} from "./dateTime";

const now = new Date(2026, 8, 18, 12, 0);

describe("date time formatting", () => {
  it("uses a compact time for today's sessions", () => {
    const value = new Date(2026, 8, 18, 9, 5).toISOString();
    expect(formatSessionTime(value, "en-US", now)).toMatch(/9:05\s?AM/i);
  });

  it("includes the date for older messages", () => {
    const value = new Date(2026, 8, 17, 9, 5).toISOString();
    const formatted = formatMessageTime(value, "en-US", now);
    expect(formatted).toContain("Sep");
    expect(formatted).toContain("17");
    expect(formatted).toMatch(/9:05\s?AM/i);
  });

  it("returns an empty label for missing or invalid values", () => {
    expect(formatMessageTime(undefined, "en-US", now)).toBe("");
    expect(formatSessionTime("not-a-date", "en-US", now)).toBe("");
    expect(formatFullTime(undefined, "en-US")).toBe("");
  });
});
