import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import { useWorkbarStore } from "@/stores/workbar";

function memoryStorage(): Storage {
  const values = new Map<string, string>();
  return {
    get length() {
      return values.size;
    },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => [...values.keys()][index] ?? null,
    removeItem: (key) => {
      values.delete(key);
    },
    setItem: (key, value) => values.set(key, value),
  };
}

describe("workbar store", () => {
  beforeEach(() => {
    vi.stubGlobal("localStorage", memoryStorage());
    setActivePinia(createPinia());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("moves draft tabs into the first persisted session", () => {
    const store = useWorkbarStore();
    store.addTab("terminal");
    expect(store.tabs).toHaveLength(1);
    expect(store.tabs[0].sessionId).toBeUndefined();

    store.setActiveSession("session-1");

    expect(store.tabs).toHaveLength(1);
    expect(store.tabs[0].sessionId).toBe("session-1");
  });

  it("keeps tabs isolated between sessions", () => {
    const store = useWorkbarStore();
    store.setActiveSession("session-1");
    store.openBrowser("https://example.com/docs");

    store.setActiveSession("session-2");
    expect(store.tabs).toEqual([]);

    store.setActiveSession("session-1");
    expect(store.tabs[0]).toMatchObject({
      kind: "browser",
      title: "example.com",
      url: "https://example.com/docs",
    });
  });

  it("opens generated artifacts in a reusable tab", () => {
    const store = useWorkbarStore();
    store.setActiveSession("session-1");

    store.openArtifact("session-1", {
      id: "artifact-1",
      name: "report.html",
      kind: "file",
      media_type: "text/html",
      bytes: 42,
    });
    store.openArtifact("session-1", {
      id: "artifact-1",
      name: "report.html",
      kind: "file",
      media_type: "text/html",
      bytes: 42,
    });

    expect(store.tabs).toHaveLength(1);
    expect(store.tabs[0]).toMatchObject({
      kind: "artifact",
      title: "report.html",
      sessionId: "session-1",
    });
    expect(store.open).toBe(true);
    expect(store.activeTabId).toBe("artifact:artifact-1");
  });

  it("reopens an existing workspace tab and selects files in it", () => {
    const store = useWorkbarStore();
    store.setActiveSession("session-1");
    store.openFiles("/workspace");
    store.setOpen(false);

    store.openFiles("/workspace");
    expect(store.open).toBe(true);

    store.openFile("/workspace", "src/main.ts");
    expect(store.activeTab).toMatchObject({
      kind: "file",
      title: "main.ts",
      path: "src/main.ts",
      workspacePath: "/workspace",
      sessionId: "session-1",
    });
    expect(store.activeTab?.titleKey).toBeUndefined();
  });

  it("clamps and persists panel width", () => {
    const store = useWorkbarStore();

    store.setWidth(100);
    expect(store.width).toBe(store.minWidth);
    expect(localStorage.getItem("foya-workbar-width-v1")).toBe(
      String(store.minWidth),
    );

    store.setWidth(2000);
    expect(store.width).toBe(store.maxWidth);
  });
});
