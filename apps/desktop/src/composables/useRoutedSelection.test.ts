import { describe, expect, it } from "vitest";
import { resolveRoutedSelection } from "@/composables/useRoutedSelection";

const existingIds = new Set(["session-1", "session-2"]);

function decide(
  overrides: Partial<Parameters<typeof resolveRoutedSelection>[0]> = {},
) {
  return resolveRoutedSelection({
    ready: true,
    matchesRoute: true,
    routeId: "",
    activeId: "",
    emptyRoute: "active",
    exists: (id) => existingIds.has(id),
    ...overrides,
  });
}

describe("resolveRoutedSelection", () => {
  it("waits until the store and target route are active", () => {
    expect(decide({ ready: false })).toEqual({ type: "none" });
    expect(decide({ matchesRoute: false })).toEqual({ type: "none" });
  });

  it("restores the active entity on an empty canonical route", () => {
    expect(decide({ activeId: "session-1" })).toEqual({
      type: "restore-active",
      id: "session-1",
    });
  });

  it("clears selection for an explicit draft route", () => {
    expect(decide({ activeId: "session-1", emptyRoute: "clear" })).toEqual({
      type: "clear",
    });
  });

  it("selects a valid routed entity", () => {
    expect(decide({ routeId: "session-2", activeId: "session-1" })).toEqual({
      type: "select",
      id: "session-2",
    });
  });

  it("rejects an unknown routed entity", () => {
    expect(decide({ routeId: "missing" })).toEqual({ type: "invalid" });
  });

  it("does nothing when route and store already agree", () => {
    expect(decide({ routeId: "session-1", activeId: "session-1" })).toEqual({
      type: "none",
    });
  });
});
