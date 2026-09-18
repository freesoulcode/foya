import type { ProjectInfo, Session } from "@/lib/api";

export interface ProjectGroup {
  project: ProjectInfo;
  sessions: Session[];
  updatedAt: string;
}

export interface ChatSidebarGroups {
  pinnedSessions: Session[];
  ungroupedSessions: Session[];
  projectGroups: ProjectGroup[];
}

function sortSessionsByUpdatedAt(items: Session[]): Session[] {
  return [...items].sort((left, right) =>
    right.updated_at.localeCompare(left.updated_at),
  );
}

function sortPinnedSessions(items: Session[]): Session[] {
  return [...items].sort((left, right) =>
    (right.pinned_at ?? right.updated_at).localeCompare(
      left.pinned_at ?? left.updated_at,
    ),
  );
}

export function buildChatSidebarGroups(
  sessions: Session[],
  projects: ProjectInfo[],
): ChatSidebarGroups {
  const persistentSessions = sessions.filter((session) => !session.temporary);
  const projectIDs = new Set(projects.map((project) => project.id));
  const sessionsByProject = new Map<string, Session[]>(
    projects.map((project) => [project.id, []]),
  );

  for (const session of persistentSessions) {
    if (session.project_id && projectIDs.has(session.project_id)) {
      sessionsByProject.get(session.project_id)?.push(session);
    }
  }

  const pinnedSessions = sortPinnedSessions(
    persistentSessions.filter((session) => session.pinned),
  );
  const ungroupedSessions = sortSessionsByUpdatedAt(
    persistentSessions.filter(
      (session) =>
        !session.pinned &&
        (!session.project_id || !projectIDs.has(session.project_id)),
    ),
  );
  const projectGroups = projects
    .map((project) => {
      const allSessions = sessionsByProject.get(project.id) ?? [];
      return {
        project,
        sessions: sortSessionsByUpdatedAt(
          allSessions.filter((session) => !session.pinned),
        ),
        updatedAt: allSessions.reduce(
          (latest, session) =>
            session.updated_at > latest ? session.updated_at : latest,
          project.updated_at,
        ),
      };
    })
    .sort((left, right) => {
      if (Boolean(left.project.pinned) !== Boolean(right.project.pinned)) {
        return left.project.pinned ? -1 : 1;
      }
      const leftTime = left.project.pinned
        ? left.project.pinned_at ?? left.updatedAt
        : left.updatedAt;
      const rightTime = right.project.pinned
        ? right.project.pinned_at ?? right.updatedAt
        : right.updatedAt;
      return rightTime.localeCompare(leftTime);
    });

  return { pinnedSessions, ungroupedSessions, projectGroups };
}
