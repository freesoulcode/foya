import { describe, expect, it } from "vitest";
import type { ProjectInfo, Session } from "@/lib/api";
import { buildChatSidebarGroups } from "./chatSidebarGroups";

function session(
  id: string,
  updatedAt: string,
  options: Partial<Session> = {},
): Session {
  return {
    id,
    phase: "idle",
    connection_id: "",
    model: "",
    created_at: updatedAt,
    updated_at: updatedAt,
    ...options,
  };
}

function project(id: string, updatedAt: string): ProjectInfo {
  return {
    id,
    name: id,
    path: `/tmp/${id}`,
    available: true,
    created_at: updatedAt,
    updated_at: updatedAt,
  };
}

describe("buildChatSidebarGroups", () => {
  it("moves every pinned chat into one top-level group", () => {
    const groups = buildChatSidebarGroups(
      [
        session("project-pinned", "2026-09-18T10:00:00Z", {
          project_id: "project-1",
          pinned: true,
          pinned_at: "2026-09-18T11:00:00Z",
        }),
        session("loose-pinned", "2026-09-18T12:00:00Z", {
          pinned: true,
          pinned_at: "2026-09-18T13:00:00Z",
        }),
        session("project-chat", "2026-09-18T09:00:00Z", {
          project_id: "project-1",
        }),
        session("loose-chat", "2026-09-18T08:00:00Z"),
      ],
      [project("project-1", "2026-09-01T00:00:00Z")],
    );

    expect(groups.pinnedSessions.map((item) => item.id)).toEqual([
      "loose-pinned",
      "project-pinned",
    ]);
    expect(groups.ungroupedSessions.map((item) => item.id)).toEqual([
      "loose-chat",
    ]);
    expect(groups.projectGroups[0].sessions.map((item) => item.id)).toEqual([
      "project-chat",
    ]);
  });

  it("keeps pinned chat activity in its project's recency", () => {
    const groups = buildChatSidebarGroups(
      [
        session("recent-pinned", "2026-09-18T10:00:00Z", {
          project_id: "older-project",
          pinned: true,
        }),
      ],
      [
        project("older-project", "2026-09-01T00:00:00Z"),
        project("newer-project", "2026-09-10T00:00:00Z"),
      ],
    );

    expect(groups.projectGroups.map((item) => item.project.id)).toEqual([
      "older-project",
      "newer-project",
    ]);
    expect(groups.projectGroups[0].sessions).toEqual([]);
  });
});
