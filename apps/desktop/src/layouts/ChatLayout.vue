<script setup lang="ts">
import { ref, computed, watch } from "vue";
import { storeToRefs } from "pinia";
import { useI18n } from "vue-i18n";
import { RouterView, useRoute, useRouter } from "vue-router";
import { openUrl } from "@tauri-apps/plugin-opener";
import { useConversationStore } from "@/stores/conversation";
import { useLinkPreference } from "@/composables/useLinkPreference";
import { usePlatform } from "@/composables/usePlatform";
import { useRoutedSelection } from "@/composables/useRoutedSelection";
import { useWorkbarStore } from "@/stores/workbar";
import { SidebarProvider, SidebarInset } from "@/components/ui/sidebar";
import {
  api,
  type ApprovalMode,
  type AttachmentRef,
  type BackgroundCommand,
  type BrowserActionRequest,
  type BrowserActionResult,
  type BrowserElementSelection,
  type ReasoningEffort,
  type SessionWorkspace,
  type UpdateSessionPatch,
} from "@/lib/api";
import { diffFilePath } from "@/lib/diff";
import ChatTitleBar from "@/layouts/chat/ChatTitleBar.vue";
import ChatSidebar from "@/layouts/chat/ChatSidebar.vue";
import MessageList from "@/components/chat/MessageList.vue";
import ProjectCreateDialog from "@/components/projects/ProjectCreateDialog.vue";
import WorkbarPanel from "@/components/workbar/WorkbarPanel.vue";
import {
  provideChatWorkspace,
  type ChatWorkspaceContext,
} from "@/layouts/chatWorkspace";
import {
  RouteName,
  chatLocation,
  newChatLocation,
  studioLocation,
} from "@/router/navigation";

const router = useRouter();
const route = useRoute();
const { t } = useI18n();
const { isMac } = usePlatform();
const workbarStore = useWorkbarStore();
const { open: workbarOpen } = storeToRefs(workbarStore);
const {
  openFile: openWorkbarFile,
  openArtifact: openWorkbarArtifact,
  openBrowser: openWorkbarBrowser,
  openAgentBrowser,
  openSideChat,
  openBackgroundCommand: openWorkbarBackgroundCommand,
  setActiveSession: setActiveWorkbarSession,
  removeSession: removeWorkbarSession,
  ownerSessionForContentSession,
} = workbarStore;
const { linkOpenMode } = useLinkPreference();
const conversationStore = useConversationStore();
const { draft: _draft, ...conversationRefs } = storeToRefs(conversationStore);
const draft = conversationStore.draft;

const {
  ready,
  streaming,
  sessions,
  projects,
  runningSessions,
  unreadSessions,
  compactingSessions,
  agentRunsBySession,
  workflowsBySession,
  activeId,
  activeSession,
  isDraft,
  connectionModels,
  modelsLoading,
  modelsError,
  messages,
  queuedMessages,
  backgroundCommands,
  fileReview,
  contextUsage,
  pendingApprovals,
  pendingQuestions,
  pendingBrowserActions,
  pendingHistoryRewind,
  composerRestore,
} = conversationRefs;
const {
  newSession,
  select,
  send,
  ensureSession,
  rewindSentMessage,
  consumeComposerRestore,
  keepAllFileChanges,
  undoAllFileChanges,
  toggleFileReviewForceFile,
  refreshActiveFileReview,
  cancelTurn,
  cancelTool,
  backgroundTool,
  revealToolCommand,
  stopBackgroundCommand,
  updateSession,
  renameSession,
  pinSession,
  forkSession,
  sideChatSession,
  deleteSession,
  editQueuedMessage,
  reorderQueuedMessage,
  deleteQueuedMessage,
  dispatchQueuedMessage,
  approveWorkflow,
  closeWorkflow,
  refreshConnections,
  refreshProjects,
  registerProject,
} = conversationStore;

const projectCreateOpen = ref(false);
const projectCreateBusy = ref(false);
const projectCreateError = ref("");
const projectCreateTargetSessionId = ref<string | null>(null);
const messageListRef = ref<InstanceType<typeof MessageList> | null>(null);
const activeTurn = ref(0);
const questionPanelExpanded = ref(false);
const pendingBrowserElements = ref<BrowserElementSelection[]>([]);
const sessionDeleteDialogOpen = ref(false);
const automationsActive = computed(() => route.name === RouteName.automations);
const pluginsActive = computed(
  () =>
    route.name === RouteName.plugins ||
    route.name === RouteName.pluginDetail
);
const chatPageActive = computed(
  () =>
    route.name === RouteName.chat ||
    route.name === RouteName.chatDraft
);
const workspacePageActive = computed(() => !chatPageActive.value);

useRoutedSelection({
  ready,
  routeNames: [RouteName.chat, RouteName.chatDraft],
  paramName: "sessionId",
  activeId,
  emptyRoute: () =>
    route.name === RouteName.chatDraft ? "clear" : "active",
  exists: (sessionId) =>
    sessions.value.some((session) => session.id === sessionId),
  select,
  clear: newSession,
  location: chatLocation,
  onError: (error) => {
    console.error("Failed to select routed chat:", error);
  },
});

// Each user message starts a turn; its leading text becomes the summary.
const TURN_LABEL_MAX = 40;
const turnPoints = computed(() =>
  messages.value
    .filter((m) => m.role === "user")
    .map((m) => {
      const text = m.content.replace(/\s+/g, " ").trim();
      return {
        label:
          text.length > TURN_LABEL_MAX
            ? `${text.slice(0, TURN_LABEL_MAX)}…`
            : text || t("New chat"),
      };
    })
);

// Select the latest turn by default; MessageList updates it while scrolling.
watch(
  () => turnPoints.value.length,
  (n) => {
    activeTurn.value = Math.max(0, n - 1);
  }
);

watch(
  activeId,
  (id) => {
    setActiveWorkbarSession(id);
    pendingBrowserElements.value = [];
  },
  { immediate: true }
);

watch(
  () => sessions.value.map((session) => session.id),
  (ids, previous = []) => {
    const current = new Set(ids);
    for (const id of previous) {
      if (!current.has(id)) removeWorkbarSession(id);
    }
  }
);

function onTurnSelect(i: number) {
  messageListRef.value?.scrollToTurn(i);
}

async function executeComposerCommand(
  name: string,
  args: string,
  workspaceFiles: string[] = []
) {
  const sessionID = activeId.value || await ensureSession();
  await api.executeCommand(sessionID, name, args, workspaceFiles);
}

// Draft settings come from draft state; existing chats use active session state.
const composerModel = computed(() =>
  isDraft.value ? draft.model : activeSession.value?.model ?? ""
);
const composerConnectionID = computed(() =>
  isDraft.value ? draft.connectionID : activeSession.value?.connection_id ?? ""
);
const currentProjectID = computed(() =>
  isDraft.value ? draft.projectID : activeSession.value?.project_id ?? ""
);
const currentProject = computed(
  () => projects.value.find((project) => project.id === currentProjectID.value) ?? null
);
const activeProjects = computed(() =>
  projects.value
);
const projectPath = computed(() => currentProject.value?.path ?? "");
const activeWorkspace = ref<SessionWorkspace | null>(null);
const activeWorkspaceSessionID = ref("");
let workspaceRequest = 0;

function draftProjectWorkspace(): SessionWorkspace | null {
  const project = currentProject.value;
  if (!project) return null;
  return {
    kind: "project",
    name: project.name,
    path: project.path,
    project_id: project.id,
  };
}

async function loadSessionWorkspace(sessionID: string): Promise<SessionWorkspace | null> {
  const request = ++workspaceRequest;
  if (!sessionID) {
    activeWorkspace.value = draftProjectWorkspace();
    activeWorkspaceSessionID.value = "";
    return activeWorkspace.value;
  }
  try {
    const workspace = await api.getSessionWorkspace(sessionID);
    if (request === workspaceRequest && activeId.value === sessionID) {
      activeWorkspace.value = workspace;
      activeWorkspaceSessionID.value = sessionID;
    }
    return workspace;
  } catch (error) {
    if (request === workspaceRequest && activeId.value === sessionID) {
      activeWorkspace.value = null;
      activeWorkspaceSessionID.value = "";
    }
    throw error;
  }
}

async function ensureActiveWorkspace(): Promise<SessionWorkspace> {
  const sessionID = activeId.value || await ensureSession();
  if (activeWorkspace.value && activeWorkspaceSessionID.value === sessionID) {
    return activeWorkspace.value;
  }
  const workspace = await loadSessionWorkspace(sessionID);
  if (!workspace) throw new Error(t("Unable to resolve workspace"));
  return workspace;
}

watch(
  () => [activeId.value, activeSession.value?.project_id, draft.projectID] as const,
  ([sessionID]) => {
    if (!sessionID) {
      workspaceRequest += 1;
      activeWorkspace.value = draftProjectWorkspace();
      activeWorkspaceSessionID.value = "";
      return;
    }
    activeWorkspace.value = null;
    activeWorkspaceSessionID.value = "";
    void loadSessionWorkspace(sessionID).catch((error) => {
      console.error("Failed to resolve session workspace:", error);
    });
  },
  { immediate: true }
);
const composerReasoningEffort = computed<ReasoningEffort>(
  () => (isDraft.value ? draft.reasoningEffort : activeSession.value?.reasoning_effort) ?? ""
);
const projectLocked = computed(
  () => !isDraft.value && Boolean(activeSession.value?.project_id)
);
const composerApproval = computed<ApprovalMode>(
  () =>
    (isDraft.value
      ? draft.approvalMode
      : activeSession.value?.approval_mode) || "manual"
);
const composerContextWindow = computed(
  () =>
    connectionModels.value.find((connection) => connection.id === composerConnectionID.value)
      ?.model_settings?.[composerModel.value]?.context_window ?? 0
);
const composerSupportsImage = computed(() => {
  const connection = connectionModels.value.find(
    (item) => item.id === composerConnectionID.value
  );
  if (!connection || !composerModel.value) return false;
  return connection.model_settings?.[composerModel.value]?.image_input ?? true;
});
const composerContextUsage = computed(() =>
  contextUsage.value?.model === composerModel.value ? contextUsage.value : undefined
);
const activeCompacting = computed(
  () => Boolean(activeId.value && compactingSessions.value[activeId.value])
);
const activeWorkflow = computed(() =>
  activeId.value ? workflowsBySession.value[activeId.value] ?? null : null
);
const activeQuestionBatch = computed(
  () =>
    Object.values(pendingQuestions.value).find(
      (batch) => batch.session_id === activeId.value
    ) ?? null
);
const sessionsRequiringAttention = computed<Record<string, boolean>>(() => {
  const pending: Record<string, boolean> = {};
  const markPending = (sessionID: string) => {
    if (sessionID && sessionID !== activeId.value) pending[sessionID] = true;
  };
  for (const batch of Object.values(pendingQuestions.value)) {
    markPending(batch.session_id);
  }
  for (const approval of Object.values(pendingApprovals.value)) {
    markPending(approval.session);
  }
  for (const workflow of Object.values(workflowsBySession.value)) {
    if (workflow?.status === "ready") markPending(workflow.session_id);
  }
  return pending;
});
const workbarObscured = computed(
  () =>
    sessionDeleteDialogOpen.value ||
    Object.keys(pendingApprovals.value).length > 0 ||
    pendingHistoryRewind.value !== null
);

function onOpenDiff(diff: string) {
  if (!projectPath.value) return;
  const path = diffFilePath(diff, projectPath.value);
  if (path) openWorkbarFile(projectPath.value, path, "diff", diff);
}

function onOpenReviewFile(path: string, diff: string) {
  if (!projectPath.value || path.startsWith("/")) return;
  openWorkbarFile(projectPath.value, path, "diff", diff);
}

function onOpenWorkflowFile(path: string) {
  const root = projectPath.value.replace(/\\/g, "/").replace(/\/+$/, "");
  const target = path.replace(/\\/g, "/");
  if (!root || !target.startsWith(`${root}/`)) return;
  openWorkbarFile(projectPath.value, target.slice(root.length + 1));
}

async function listActiveWorkspaceFiles() {
  const sessionID = activeId.value || await ensureSession();
  await ensureActiveWorkspace();
  return api.listWorkspaceFiles(sessionID);
}

function onOpenArtifact(attachment: AttachmentRef) {
  if (!activeId.value) return;
  openWorkbarArtifact(activeId.value, attachment);
}

function onOpenBackgroundCommand(command: BackgroundCommand) {
  openWorkbarBackgroundCommand(
    command.session_id,
    command.command_id,
    command.command
  );
}

async function onViewToolInWorkbar(toolCallId: string) {
  const command = await revealToolCommand(toolCallId);
  if (command) onOpenBackgroundCommand(command);
}

async function onOpenLink(url: string) {
  if (linkOpenMode.value === "system") {
    try {
      await openUrl(url);
    } catch (error) {
      console.error("Failed to open link in system browser:", error);
    }
    return;
  }
  openWorkbarBrowser(url);
}

function onBrowserElementSelected(element: BrowserElementSelection) {
  const duplicate = pendingBrowserElements.value.some(
    (item) =>
      item.page_url === element.page_url &&
      item.selector === element.selector
  );
  if (duplicate || pendingBrowserElements.value.length >= 8) return;
  pendingBrowserElements.value = [...pendingBrowserElements.value, element];
}

function removeBrowserElement(index: number) {
  pendingBrowserElements.value = pendingBrowserElements.value.filter(
    (_, itemIndex) => itemIndex !== index
  );
}

function clearBrowserElements() {
  pendingBrowserElements.value = [];
}

function restoreBrowserElements(elements: BrowserElementSelection[]) {
  pendingBrowserElements.value = elements;
}

const openedBrowserRequests = new Set<string>();

function workbarSessionFor(executionSessionId: string) {
  const directOwner = ownerSessionForContentSession(executionSessionId);
  if (directOwner !== undefined) return directOwner;
  for (const runs of Object.values(agentRunsBySession.value)) {
    const run = runs.find((item) => item.child_session_id === executionSessionId);
    if (run?.root_session_id) {
      return ownerSessionForContentSession(run.root_session_id) ?? run.root_session_id;
    }
  }
  return executionSessionId;
}

function workbarContentSessionFor(executionSessionId: string) {
  if (ownerSessionForContentSession(executionSessionId) !== undefined) {
    return executionSessionId;
  }
  for (const runs of Object.values(agentRunsBySession.value)) {
    const run = runs.find((item) => item.child_session_id === executionSessionId);
    if (run?.root_session_id) return run.root_session_id;
  }
  return executionSessionId;
}

watch(
  () => Object.values(pendingBrowserActions.value),
  (actions) => {
    for (const action of actions) {
      if (openedBrowserRequests.has(action.id)) continue;
      openedBrowserRequests.add(action.id);
      openAgentBrowser(
        workbarSessionFor(action.session_id),
        action.browser_id,
        workbarContentSessionFor(action.session_id)
      );
    }
  },
  { immediate: true }
);

async function onBrowserActionResult(
  request: BrowserActionRequest,
  result: BrowserActionResult
) {
  try {
    await api.resolveBrowserAction(request.session_id, request.id, result);
  } catch (cause) {
    console.error("Failed to resolve browser action:", cause);
  }
}

// Apply composer settings locally for drafts and persist them for existing chats.
function onModelConfigChange(value: {
  connectionID: string;
  model: string;
  reasoningEffort: ReasoningEffort;
}) {
  if (isDraft.value) {
    draft.connectionID = value.connectionID;
    draft.model = value.model;
    draft.reasoningEffort = value.reasoningEffort;
  } else if (activeId.value) {
    void updateSession(activeId.value, {
      connection_id: value.connectionID,
      model: value.model,
      reasoning_effort: value.reasoningEffort,
    });
  }
}

function onProjectChange(value: string) {
  if (projectLocked.value) return;
  if (isDraft.value) draft.projectID = value;
  else if (activeId.value)
    void updateSession(activeId.value, { project_id: value });
}

function onAddProject(sessionId?: string) {
  projectCreateTargetSessionId.value = sessionId ?? null;
  projectCreateError.value = "";
  projectCreateOpen.value = true;
}

async function onCreateProject(input: { name: string; path: string }) {
  projectCreateBusy.value = true;
  projectCreateError.value = "";
  try {
    const project = await registerProject(input.path, input.name);
    projectCreateOpen.value = false;
    if (projectCreateTargetSessionId.value) {
      await updateSession(projectCreateTargetSessionId.value, {
        project_id: project.id,
      });
    } else {
      onProjectChange(project.id);
    }
    projectCreateTargetSessionId.value = null;
  } catch (error) {
    projectCreateError.value = String(error);
  } finally {
    projectCreateBusy.value = false;
  }
}

async function onNewSession(projectID?: string) {
  await router.push(newChatLocation());
  newSession(projectID);
}

function onSelectSession(id: string) {
  void router.push(chatLocation(id));
}

function onApprovalChange(value: ApprovalMode) {
  if (isDraft.value) draft.approvalMode = value;
  else if (activeId.value) {
    const patch: UpdateSessionPatch = { approval_mode: value };
    void updateSession(activeId.value, patch);
  }
}

// Manual sidebar renames are persisted and disable generated title replacement.
function onRename(id: string, title: string) {
  void renameSession(id, title);
}

// Pin changes are persisted and reflected through the kernel event stream.
function onPin(id: string, pinned: boolean) {
  void pinSession(id, pinned);
}

function onFork(id: string) {
  void forkSession(id).catch((error) => {
    console.error("Failed to fork chat:", error);
  });
}

function onCreateSideChat(sourceId = activeId.value, throughSeq?: number) {
  if (!sourceId) return;
  const ownerSessionId =
    ownerSessionForContentSession(sourceId) ?? activeId.value ?? sourceId;
  void sideChatSession(sourceId, throughSeq).then((session) => {
    openSideChat(ownerSessionId, session.id);
  }).catch((error) => {
    console.error("Failed to create side chat:", error);
  });
}

function onCloseSideChat(sessionId: string) {
  void deleteSession(sessionId).catch((error) => {
    console.error("Failed to delete side chat:", error);
  });
}

// Deleting a chat cancels its turn, removes history, and updates all clients.
function onDelete(id: string) {
  void deleteSession(id).then(() => removeWorkbarSession(id));
}

async function onDeleteProject(id: string) {
  try {
    await api.deleteProject(id);
    sessions.value = await api.listSessions();
    await refreshProjects();
  } catch (error) {
    console.error("Failed to delete project:", error);
  }
}

async function onRenameProject(id: string, name: string) {
  try {
    await api.updateProject(id, { name });
    await refreshProjects();
  } catch (error) {
    console.error("Failed to rename project:", error);
  }
}

async function onPinProject(id: string, pinned: boolean) {
  try {
    await api.updateProject(id, { pinned });
    await refreshProjects();
  } catch (error) {
    console.error("Failed to update project pin state:", error);
  }
}

function openSettings() {
  void router.push({ name: RouteName.settingsConnections });
}

function openStudio() {
  void router.push(studioLocation());
}

function openAutomations() {
  void router.push({ name: RouteName.automations });
}

function openPlugins() {
  void router.push({ name: RouteName.plugins });
}

const viewContext: ChatWorkspaceContext = {
  ready,
  streaming,
  messages,
  queuedMessages,
  backgroundCommands,
  fileReview,
  connectionModels,
  modelsLoading,
  modelsError,
  activeId,
  activeSession,
  isDraft,
  runningSessions,
  activeTurn,
  turnPoints,
  messageListRef,
  projectPath,
  activeCompacting,
  activeWorkflow,
  activeQuestionBatch,
  questionPanelExpanded,
  composerModel,
  composerConnectionID,
  composerReasoningEffort,
  currentProjectID,
  activeProjects,
  projectLocked,
  composerApproval,
  composerContextUsage,
  composerContextWindow,
  composerSupportsImage,
  composerRestore,
  pendingBrowserElements,
  onTurnSelect,
  rewindSentMessage,
  consumeComposerRestore,
  keepAllFileChanges,
  undoAllFileChanges,
  toggleFileReviewForceFile,
  forkSession,
  createSideChat: onCreateSideChat,
  onOpenDiff,
  onOpenReviewFile,
  onOpenWorkflowFile,
  listActiveWorkspaceFiles,
  onOpenArtifact,
  cancelTool,
  backgroundTool,
  onViewToolInWorkbar,
  onOpenLink,
  stopBackgroundCommand,
  onOpenBackgroundCommand,
  approveWorkflow,
  closeWorkflow,
  send,
  executeComposerCommand,
  openPlugins,
  cancelTurn,
  editQueuedMessage,
  reorderQueuedMessage,
  dispatchQueuedMessage,
  deleteQueuedMessage,
  onModelConfigChange,
  onProjectChange,
  onAddProject,
  onApprovalChange,
  refreshConnections,
  removeBrowserElement,
  clearBrowserElements,
  restoreBrowserElements,
};

provideChatWorkspace(viewContext);
</script>

<template>
  <SidebarProvider class="h-svh">
    <ChatSidebar
      :is-mac="isMac"
      :sessions="sessions"
      :projects="activeProjects"
      :running="runningSessions"
      :unread="unreadSessions"
      :needs-attention="sessionsRequiringAttention"
      :active-id="activeId"
      :is-draft="isDraft"
      :automations-active="automationsActive"
      :plugins-active="pluginsActive"
      @new="onNewSession"
      @select="onSelectSession"
      @rename="onRename"
      @pin="onPin"
      @fork="onFork"
      @delete="onDelete"
      @delete-dialog-change="sessionDeleteDialogOpen = $event"
      @delete-project="onDeleteProject"
      @rename-project="onRenameProject"
      @pin-project="onPinProject"
      @open-settings="openSettings"
      @open-studio="openStudio"
      @open-automations="openAutomations"
      @open-plugins="openPlugins"
    />

    <SidebarInset class="relative min-w-0 flex-row overflow-hidden">
      <div class="flex min-h-0 min-w-[350px] flex-1 flex-col">
        <ChatTitleBar
          v-if="!workspacePageActive"
          :session="activeSession"
          :project-path="projectPath"
          @rename="onRename"
        />
        <RouterView />
      </div>

      <WorkbarPanel
        v-show="!workspacePageActive && workbarOpen"
        :session-id="activeId || undefined"
        :workspace="activeWorkspace"
        :messages="messages"
        :background-commands="backgroundCommands"
        :browser-actions="Object.values(pendingBrowserActions)"
        :obscured="workbarObscured"
        :visible="!workspacePageActive && workbarOpen"
        :ensure-session="ensureSession"
        :ensure-workspace="ensureActiveWorkspace"
        @project-files-changed="refreshActiveFileReview"
        @browser-element-selected="onBrowserElementSelected"
        @browser-action-result="onBrowserActionResult"
        @create-side-chat="onCreateSideChat"
        @close-side-chat="onCloseSideChat"
        @add-project="onAddProject"
        @open-plugins="openPlugins"
      />
    </SidebarInset>
  </SidebarProvider>

  <ProjectCreateDialog
    v-model:open="projectCreateOpen"
    :busy="projectCreateBusy"
    :error="projectCreateError"
    @confirm="onCreateProject"
  />
</template>
