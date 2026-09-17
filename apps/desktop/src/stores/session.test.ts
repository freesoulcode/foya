import { beforeEach, describe, expect, it } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import type { ConnectionModelGroup, Session } from "@/lib/api";
import { useSessionStore } from "@/stores/session";

function session(id: string, updatedAt: string): Session {
  return {
    id,
    phase: "idle",
    connection_id: "",
    model: "",
    created_at: updatedAt,
    updated_at: updatedAt,
  };
}

function connection(id: string, models: string[]): ConnectionModelGroup {
  return {
    id,
    name: id,
    type: "language",
    kind: "openai",
    auth_kind: "api_key",
    base_url: "",
    model_settings: {},
    sort_order: 0,
    models,
    context_windows: {},
  };
}

describe("session store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it("sorts sessions and maintains records by id", () => {
    const store = useSessionStore();
    store.replaceSessions([
      session("older", "2026-01-01T00:00:00Z"),
      session("newer", "2026-02-01T00:00:00Z"),
    ]);
    expect(store.sessions.map((item) => item.id)).toEqual(["newer", "older"]);

    store.upsertSession({
      ...session("older", "2026-03-01T00:00:00Z"),
      title: "Updated",
    });
    expect(store.sessions.find((item) => item.id === "older")?.title).toBe(
      "Updated",
    );

    store.removeSessionRecord("newer");
    expect(store.sessions.map((item) => item.id)).toEqual(["older"]);
  });

  it("builds a draft from the configured default model", () => {
    const store = useSessionStore();
    store.connectionModels = [
      connection("fallback", ["fast-model"]),
      connection("preferred", ["chat-model"]),
    ];
    store.defaultModels = {
      language: { connection_id: "preferred", model: "chat-model" },
      fast: { connection_id: "", model: "" },
      image: { connection_id: "", model: "" },
      video: { connection_id: "", model: "" },
    };
    store.setActive("session-1");

    store.resetDraft("project-1");

    expect(store.activeId).toBe("");
    expect(store.draft).toMatchObject({
      connectionID: "preferred",
      model: "chat-model",
      projectID: "project-1",
      approvalMode: "manual",
    });
  });

  it("falls back to the first language model when the default is unavailable", () => {
    const store = useSessionStore();
    store.connectionModels = [connection("fallback", ["fast-model"])];
    store.defaultModels = {
      language: { connection_id: "missing", model: "missing-model" },
      fast: { connection_id: "", model: "" },
      image: { connection_id: "", model: "" },
      video: { connection_id: "", model: "" },
    };

    store.resetDraft();

    expect(store.draft.connectionID).toBe("fallback");
    expect(store.draft.model).toBe("fast-model");
  });
});
