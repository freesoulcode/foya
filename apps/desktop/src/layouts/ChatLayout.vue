<script setup lang="ts">
import { ref, computed, watch } from "vue";
import { RouterView, useRouter } from "vue-router";
import { openUrl } from "@tauri-apps/plugin-opener";
import { useKernel } from "@/composables/useKernel";
import { useLinkPreference } from "@/composables/useLinkPreference";
import { usePlatform } from "@/composables/usePlatform";
import { useWorkbar } from "@/composables/useWorkbar";
import { SidebarProvider, SidebarInset } from "@/components/ui/sidebar";
import {
  api,
  type ApprovalMode,
  type BackgroundCommand,
  type BrowserActionRequest,
  type BrowserActionResult,
  type BrowserElementSelection,
  type ReasoningEffort,
  type UpdateSessionPatch,
} from "@/lib/api";
import { diffFilePath } from "@/lib/diff";
import ChatTitleBar from "@/layouts/chat/ChatTitleBar.vue";
import ChatSidebar from "@/layouts/chat/ChatSidebar.vue";
import MessageList from "@/components/chat/MessageList.vue";
import ProjectCreateDialog from "@/components/projects/ProjectCreateDialog.vue";
import WorkbarPanel from "@/components/workbar/WorkbarPanel.vue";
import type { ChatWorkspaceContext } from "@/layouts/chatWorkspace";

const router = useRouter();
const { isMac } = usePlatform();
const {
  open: workbarOpen,
  openFile: openWorkbarFile,
  openBrowser: openWorkbarBrowser,
  openAgentBrowser,
  openBackgroundCommand: openWorkbarBackgroundCommand,
} = useWorkbar();
const { linkOpenMode } = useLinkPreference();

const {
  ready,
  streaming,
  sessions,
  projects,
  runningSessions,
  compactingSessions,
  workflowsBySession,
  activeId,
  activeSession,
  isDraft,
  draft,
  connectionModels,
  modelsLoading,
  modelsError,
  messages,
  queuedMessages,
  backgroundCommands,
  contextUsage,
  pendingApprovals,
  pendingQuestions,
  pendingBrowserActions,
  pendingHistoryEdit,
  newSession,
  select,
  send,
  ensureSession,
  editSentMessage,
  cancelTurn,
  cancelTool,
  backgroundTool,
  revealToolCommand,
  stopBackgroundCommand,
  updateSession,
  renameSession,
  pinSession,
  deleteSession,
  editQueuedMessage,
  reorderQueuedMessage,
  deleteQueuedMessage,
  dispatchQueuedMessage,
  approveWorkflow,
  refreshConnections,
  refreshProjects,
  registerProject,
} = useKernel();

const projectCreateOpen = ref(false);
const projectCreateBusy = ref(false);
const projectCreateError = ref("");
const messageListRef = ref<InstanceType<typeof MessageList> | null>(null);
const activeTurn = ref(0);
const questionPanelExpanded = ref(false);
const pendingBrowserElements = ref<BrowserElementSelection[]>([]);
const sessionDeleteDialogOpen = ref(false);

// 每个 user 消息对应一个回合;摘要取该条用户消息的前若干字。
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
            : text || "新对话",
      };
    })
);

// 新回合产生(用户发消息)或切会话时,默认高亮最新回合;滚动时由 MessageList 覆盖。
watch(
  () => turnPoints.value.length,
  (n) => {
    activeTurn.value = Math.max(0, n - 1);
  }
);

watch(activeId, () => {
  pendingBrowserElements.value = [];
});

function onTurnSelect(i: number) {
  messageListRef.value?.scrollToTurn(i);
}

async function executeComposerCommand(name: string, args: string) {
  const sessionID = activeId.value || await ensureSession();
  await api.executeCommand(sessionID, name, args);
}

// 输入框当前展示的模型/工作目录/审批档位:草稿态读 draft,已建会话读 activeSession。
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
      ?.model_settings?.[composerModel.value]?.context_window ??
    connectionModels.value.find((connection) => connection.id === composerConnectionID.value)
      ?.context_windows[composerModel.value] ??
    0
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
const sessionsWaitingForAnswer = computed<Record<string, boolean>>(() => {
  const waiting: Record<string, boolean> = {};
  for (const batch of Object.values(pendingQuestions.value)) {
    waiting[batch.session_id] = true;
  }
  return waiting;
});
const workbarObscured = computed(
  () =>
    sessionDeleteDialogOpen.value ||
    Object.keys(pendingApprovals.value).length > 0 ||
    pendingHistoryEdit.value !== null
);

function onOpenDiff(diff: string) {
  if (!projectPath.value) return;
  const path = diffFilePath(diff, projectPath.value);
  if (path) openWorkbarFile(projectPath.value, path, "diff", diff);
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
      console.error("使用系统浏览器打开链接失败:", error);
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
watch(
  () => Object.values(pendingBrowserActions.value),
  (actions) => {
    for (const action of actions) {
      if (openedBrowserRequests.has(action.id)) continue;
      openedBrowserRequests.add(action.id);
      openAgentBrowser(action.session_id, action.browser_id);
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
    console.error("浏览器动作回执失败:", cause);
  }
}

// 统一处理输入框里的配置变更:草稿态直接改本地 draft;已建会话调用 PATCH 实时落库。
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

function onAddProject() {
  projectCreateError.value = "";
  projectCreateOpen.value = true;
}

async function onCreateProject(input: { name: string; path: string }) {
  projectCreateBusy.value = true;
  projectCreateError.value = "";
  try {
    const project = await registerProject(input.path, input.name);
    projectCreateOpen.value = false;
    onProjectChange(project.id);
  } catch (error) {
    projectCreateError.value = String(error);
  } finally {
    projectCreateBusy.value = false;
  }
}

function onNewSession(projectID?: string) {
  newSession(projectID);
}

function onApprovalChange(value: ApprovalMode) {
  if (isDraft.value) draft.approvalMode = value;
  else if (activeId.value) {
    const patch: UpdateSessionPatch = { approval_mode: value };
    void updateSession(activeId.value, patch);
  }
}

// 侧边栏手动改名:调用内核 PATCH,置 title_is_manual。
function onRename(id: string, title: string) {
  void renameSession(id, title);
}

// 置顶/取消置顶:走 PATCH pinned 字段,内核广播后本地项更新。
function onPin(id: string, pinned: boolean) {
  void pinSession(id, pinned);
}

// 删除会话:内核中断回合、清历史并广播,前端移除并按需切换会话。
function onDelete(id: string) {
  void deleteSession(id);
}

async function onDeleteProject(id: string) {
  try {
    await api.deleteProject(id);
    sessions.value = await api.listSessions();
    await refreshProjects();
  } catch (error) {
    console.error("删除项目失败:", error);
  }
}

async function onRenameProject(id: string, name: string) {
  try {
    await api.updateProject(id, { name });
    await refreshProjects();
  } catch (error) {
    console.error("重命名项目失败:", error);
  }
}

async function onPinProject(id: string, pinned: boolean) {
  try {
    await api.updateProject(id, { pinned });
    await refreshProjects();
  } catch (error) {
    console.error("更新项目置顶状态失败:", error);
  }
}

function openSettings() {
  void router.push("/settings/connections");
}

function openStudio() {
  void router.push("/studio");
}

const viewContext: ChatWorkspaceContext = {
  ready,
  streaming,
  messages,
  queuedMessages,
  backgroundCommands,
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
  pendingBrowserElements,
  onTurnSelect,
  editSentMessage,
  onOpenDiff,
  cancelTool,
  backgroundTool,
  onViewToolInWorkbar,
  onOpenLink,
  stopBackgroundCommand,
  onOpenBackgroundCommand,
  approveWorkflow,
  send,
  executeComposerCommand,
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
</script>

<template>
  <SidebarProvider class="h-svh">
    <ChatSidebar
      :is-mac="isMac"
      :sessions="sessions"
      :projects="activeProjects"
      :running="runningSessions"
      :waiting-for-answer="sessionsWaitingForAnswer"
      :active-id="activeId"
      :is-draft="isDraft"
      @new="onNewSession"
      @select="select"
      @rename="onRename"
      @pin="onPin"
      @delete="onDelete"
      @delete-dialog-change="sessionDeleteDialogOpen = $event"
      @delete-project="onDeleteProject"
      @rename-project="onRenameProject"
      @pin-project="onPinProject"
      @open-settings="openSettings"
      @open-studio="openStudio"
    />

    <SidebarInset class="relative min-w-0 flex-row overflow-hidden">
      <div class="flex min-h-0 min-w-[350px] flex-1 flex-col">
        <ChatTitleBar :session="activeSession" @rename="onRename" />
        <RouterView v-slot="{ Component }">
          <component :is="Component" :workspace="viewContext" />
        </RouterView>
      </div>

      <WorkbarPanel
        v-show="workbarOpen"
        :session-id="activeId || undefined"
        :project-path="projectPath"
        :messages="messages"
        :background-commands="backgroundCommands"
        :browser-actions="Object.values(pendingBrowserActions)"
        :obscured="workbarObscured"
        :ensure-session="ensureSession"
        @browser-element-selected="onBrowserElementSelected"
        @browser-action-result="onBrowserActionResult"
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
