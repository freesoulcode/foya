import { describe, expect, it } from "vitest";
import {
  normalizeTokenLimitInput,
  tokenLimitEditorValue,
} from "@/views/settings/modelTokenLimits";

describe("model token limit helpers", () => {
  it("normalizes input events to numeric token values", () => {
    expect(normalizeTokenLimitInput(8192)).toBe(8192);
    expect(normalizeTokenLimitInput("4096")).toBe(4096);
  });

  it("ignores empty and non-integer input values", () => {
    expect(normalizeTokenLimitInput("")).toBeUndefined();
    expect(normalizeTokenLimitInput(undefined)).toBeUndefined();
    expect(normalizeTokenLimitInput(0)).toBeUndefined();
    expect(normalizeTokenLimitInput("12.5")).toBeUndefined();
    expect(normalizeTokenLimitInput("abc")).toBeUndefined();
  });

  it("keeps editor state as numbers or unset", () => {
    expect(tokenLimitEditorValue(4096)).toBe(4096);
    expect(tokenLimitEditorValue(0)).toBeUndefined();
    expect(tokenLimitEditorValue(undefined)).toBeUndefined();
  });
});
