import { computed, reactive, ref } from "vue";
import { defineStore } from "pinia";
import {
  api,
  type ApprovalMode,
  type ConnectionModelGroup,
  type DefaultModels,
  type ProjectInfo,
  type ReasoningEffort,
  type Session,
  type UpdateSessionPatch,
} from "@/lib/api";
import { translate } from "@/i18n";

export interface DraftConfig {
  connectionID: string;
  model: string;
  reasoningEffort: ReasoningEffort;
  projectID: string;
  approvalMode: ApprovalMode;
}

const DEFAULT_APPROVAL: ApprovalMode = "manual";

function emptyDefaultModels(): DefaultModels {
  return {
    language: { connection_id: "", model: "" },
    fast: { connection_id: "", model: "" },
    image: { connection_id: "", model: "" },
    video: { connection_id: "", model: "" },
  };
}

export const useSessionStore = defineStore("session", () => {
  const sessions = ref<Session[]>([]);
  const projects = ref<ProjectInfo[]>([]);
  const activeId = ref("");
  const draft = reactive<DraftConfig>({
    connectionID: "",
    model: "",
    reasoningEffort: "",
    projectID: "",
    approvalMode: DEFAULT_APPROVAL,
  });
  const connectionModels = ref<ConnectionModelGroup[]>([]);
  const modelsLoading = ref(false);
  const modelsError = ref("");
  const defaultModels = ref<DefaultModels>(emptyDefaultModels());

  const activeSession = computed(() =>
    sessions.value.find((session) => session.id === activeId.value),
  );
  const isDraft = computed(() => activeId.value === "");

  function setActive(id: string) {
    activeId.value = id;
  }

  function resetDraft(projectID = "") {
    activeId.value = "";
    const preferred = defaultModels.value.language;
    const first =
      connectionModels.value.find(
        (connection) =>
          connection.id === preferred.connection_id &&
          connection.models.includes(preferred.model),
      ) ??
      connectionModels.value.find(
        (connection) =>
          connection.type === "language" && connection.models.length > 0,
      );
    draft.connectionID = first?.id ?? "";
    draft.model =
      first?.id === preferred.connection_id
        ? preferred.model
        : first?.models[0] || "";
    draft.reasoningEffort = "";
    draft.projectID = projectID;
    draft.approvalMode = DEFAULT_APPROVAL;
  }

  function replaceSessions(items: Session[]) {
    sessions.value = [...items].sort((left, right) =>
      right.updated_at.localeCompare(left.updated_at),
    );
  }

  function upsertSession(session: Session, prepend = false) {
    const index = sessions.value.findIndex((item) => item.id === session.id);
    if (index >= 0) {
      sessions.value[index] = session;
    } else if (prepend) {
      sessions.value.unshift(session);
    } else {
      sessions.value.push(session);
    }
  }

  function mergeSession(session: Session) {
    const index = sessions.value.findIndex((item) => item.id === session.id);
    if (index >= 0) {
      sessions.value[index] = { ...sessions.value[index], ...session };
    }
  }

  function sessionTreeIds(id: string): string[] {
    const remove = new Set([id]);
    let changed = true;
    while (changed) {
      changed = false;
      for (const session of sessions.value) {
        if (
          session.parent_id &&
          remove.has(session.parent_id) &&
          !remove.has(session.id)
        ) {
          remove.add(session.id);
          changed = true;
        }
      }
    }
    return Array.from(remove);
  }

  function removeSessionRecord(id: string) {
    const remove = new Set(sessionTreeIds(id));
    sessions.value = sessions.value.filter((session) => !remove.has(session.id));
  }

  async function loadInitialData() {
    for (let attempt = 1; ; attempt++) {
      try {
        const [loadedSessions, loadedProjects] = await Promise.all([
          api.listSessions(),
          api.listProjects(),
        ]);
        replaceSessions(loadedSessions);
        projects.value = loadedProjects;
        return;
      } catch (error) {
        const message = String(error);
        const connectionError =
          message.includes("Failed to connect to kernel") ||
          message.includes("client error (Connect)") ||
          message === translate("Unable to connect to the kernel");
        if (!connectionError) throw error;
        await new Promise((resolve) =>
          window.setTimeout(
            resolve,
            Math.min(1000, 250 + Math.floor(attempt / 20) * 250),
          ),
        );
      }
    }
  }

  async function refreshConnections() {
    modelsLoading.value = true;
    modelsError.value = "";
    try {
      const [allConnections, defaults] = await Promise.all([
        api.listConnections(),
        api.getDefaultModels(),
      ]);
      const connections = allConnections.filter(
        (connection) => connection.type === "language",
      );
      defaultModels.value = defaults;
      connectionModels.value = await Promise.all(
        connections.map(async (connection) => {
          try {
            const catalog = await api.listConnectionModels(connection.id ?? "");
            return { ...connection, ...catalog };
          } catch (cause) {
            return {
              ...connection,
              models: [],
              context_windows: {},
              models_error: String(cause),
            };
          }
        }),
      );
      if (isDraft.value && !draft.connectionID) {
        const preferred = defaultModels.value.language;
        const first =
          connectionModels.value.find(
            (connection) =>
              connection.id === preferred.connection_id &&
              connection.models.includes(preferred.model),
          ) ??
          connectionModels.value.find(
            (connection) =>
              connection.type === "language" && connection.models.length > 0,
          );
        if (first) {
          draft.connectionID = first.id ?? "";
          draft.model =
            first.id === preferred.connection_id
              ? preferred.model
              : first.models[0] || "";
        }
      }
    } catch (error) {
      connectionModels.value = [];
      modelsError.value = String(error);
    } finally {
      modelsLoading.value = false;
    }
  }

  async function registerProject(
    path: string,
    name = "",
  ): Promise<ProjectInfo> {
    const project = await api.registerProject(path, name);
    const index = projects.value.findIndex((item) => item.id === project.id);
    if (index >= 0) projects.value[index] = project;
    else projects.value.unshift(project);
    return project;
  }

  async function refreshProjects() {
    projects.value = await api.listProjects();
    if (
      isDraft.value &&
      draft.projectID &&
      !projects.value.some((project) => project.id === draft.projectID)
    ) {
      draft.projectID = "";
    }
  }

  async function updateSession(id: string, patch: UpdateSessionPatch) {
    const updated = await api.updateSession(id, patch);
    upsertSession(updated);
    return updated;
  }

  function renameSession(id: string, title: string) {
    return updateSession(id, { title });
  }

  function pinSession(id: string, pinned: boolean) {
    return updateSession(id, { pinned });
  }

  return {
    sessions,
    projects,
    activeId,
    activeSession,
    isDraft,
    draft,
    connectionModels,
    modelsLoading,
    modelsError,
    defaultModels,
    setActive,
    resetDraft,
    replaceSessions,
    upsertSession,
    mergeSession,
    sessionTreeIds,
    removeSessionRecord,
    loadInitialData,
    refreshConnections,
    refreshProjects,
    registerProject,
    updateSession,
    renameSession,
    pinSession,
  };
});
