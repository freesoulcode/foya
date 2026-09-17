import { beforeEach, describe, expect, it } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import { useInteractionStore } from "@/stores/interaction";

describe("interaction store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it("tracks approvals and clears session-owned state", () => {
    const store = useInteractionStore();
    store.requestApproval({
      id: "approval-1",
      session: "session-1",
      tool_name: "bash",
      action: "execute",
      detail: "pnpm test",
    });
    store.requestApproval({
      id: "approval-2",
      session: "session-2",
      tool_name: "read",
      action: "read",
      detail: "README.md",
    });

    store.clearSession("session-1");

    expect(store.pendingApprovals["approval-1"]).toBeUndefined();
    expect(store.pendingApprovals["approval-2"]?.session).toBe("session-2");
  });

  it("uses a nonce so composer restores are consumed exactly once", () => {
    const store = useInteractionStore();
    store.restoreComposer("session-1", "draft");
    const nonce = store.composerRestore?.nonce;

    expect(nonce).toBeTypeOf("number");
    store.consumeComposerRestore((nonce ?? 0) + 1);
    expect(store.composerRestore?.text).toBe("draft");

    store.consumeComposerRestore(nonce ?? 0);
    expect(store.composerRestore).toBeNull();
  });

  it("toggles forced files while a rewind is editable", () => {
    const store = useInteractionStore();
    store.setPendingHistoryRewind({
      sessionId: "session-1",
      messageSeq: 10,
      message: "retry",
      files: [],
      headSeq: 12,
      fileStateToken: "token",
      forceFileKeys: [],
    });

    store.toggleHistoryRewindForceFile("src/main.ts");
    expect(store.pendingHistoryRewind?.forceFileKeys).toEqual(["src/main.ts"]);

    store.toggleHistoryRewindForceFile("src/main.ts");
    expect(store.pendingHistoryRewind?.forceFileKeys).toEqual([]);
  });
});
