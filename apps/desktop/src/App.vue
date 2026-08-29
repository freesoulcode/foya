<script setup lang="ts">
import { ref, computed, watch, onMounted } from "vue";
import { useKernel } from "@/composables/useKernel";
import { usePlatform } from "@/composables/usePlatform";
import { useWorkbar } from "@/composables/useWorkbar";
import { SidebarProvider, SidebarInset } from "@/components/ui/sidebar";
import type { ApprovalMode, ReasoningEffort, UpdateSessionPatch } from "@/lib/api";
import AppTitleBar from "@/components/AppTitleBar.vue";
import SessionSidebar from "@/components/chat/SessionSidebar.vue";
import MessageList from "@/components/chat/MessageList.vue";
import Timeline from "@/components/chat/Timeline.vue";
import Composer from "@/components/chat/Composer.vue";
import SettingsWorkspace from "@/components/settings/SettingsWorkspace.vue";
import ApprovalDialog from "@/components/chat/ApprovalDialog.vue";
import HistoryEditDialog from "@/components/chat/HistoryEditDialog.vue";
import WorkbarPanel from "@/components/workbar/WorkbarPanel.vue";

const { isMac } = usePlatform();
const { open: workbarOpen } = useWorkbar();

const {
  ready,
  streaming,
  sessions,
  runningSessions,
  compactingSessions,
  activeId,
  activeSession,
  isDraft,
  draft,
  connectionModels,
  modelsLoading,
  modelsError,
  messages,
  queuedMessages,
  contextUsage,
  pendingApprovals,
  pendingHistoryEdit,
  connect,
  newSession,
  select,
  send,
  ensureSession,
  editSentMessage,
  cancelTurn,
  updateSession,
  renameSession,
  pinSession,
  deleteSession,
  editQueuedMessage,
  reorderQueuedMessage,
  deleteQueuedMessage,
  dispatchQueuedMessage,
  refreshConnections,
} = useKernel();

const settingsActive = ref(false);
const messageListRef = ref<InstanceType<typeof MessageList> | null>(null);
const activeTurn = ref(0);

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

function onTurnSelect(i: number) {
  messageListRef.value?.scrollToTurn(i);
}

// 退出设置工作区后刷新 Connection 目录，使新建连接立即出现在模型选择器。
watch(settingsActive, (active) => {
  if (!active) void refreshConnections();
});

// 输入框当前展示的模型/工作目录/审批档位:草稿态读 draft,已建会话读 activeSession。
const composerModel = computed(() =>
  isDraft.value ? draft.model : activeSession.value?.model ?? ""
);
const composerConnectionID = computed(() =>
  isDraft.value ? draft.connectionID : activeSession.value?.connection_id ?? ""
);
const composerWorkspace = computed(() =>
  isDraft.value ? draft.workspace : activeSession.value?.workspace ?? ""
);
const composerReasoningEffort = computed<ReasoningEffort>(
  () => (isDraft.value ? draft.reasoningEffort : activeSession.value?.reasoning_effort) ?? ""
);
const workspaceLocked = computed(
  () => !isDraft.value && Boolean(activeSession.value?.workspace)
);
const composerApproval = computed<ApprovalMode>(
  () =>
    (isDraft.value
      ? draft.approvalMode
      : (activeSession.value?.approval_mode as ApprovalMode)) || "ask"
);
const composerContextWindow = computed(
  () =>
    connectionModels.value.find((connection) => connection.id === composerConnectionID.value)
      ?.context_windows[composerModel.value] ?? 0
);
const composerContextUsage = computed(() =>
  contextUsage.value?.model === composerModel.value ? contextUsage.value : undefined
);
const activeCompacting = computed(
  () => Boolean(activeId.value && compactingSessions.value[activeId.value])
);
const workbarObscured = computed(
  () =>
    settingsActive.value ||
    Object.keys(pendingApprovals.value).length > 0 ||
    pendingHistoryEdit.value !== null
);

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

function onWorkspaceChange(value: string) {
  if (workspaceLocked.value) return;
  if (isDraft.value) draft.workspace = value;
  else if (activeId.value)
    void updateSession(activeId.value, { workspace: value });
}

function onNewSession(workspace?: string) {
  newSession(workspace);
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

onMounted(connect);
</script>

<template>
  <SettingsWorkspace
    v-if="settingsActive"
    :active="settingsActive"
    @close="settingsActive = false"
  />

  <SidebarProvider v-else class="h-svh">
    <SessionSidebar
      :is-mac="isMac"
      :sessions="sessions"
      :running="runningSessions"
      :active-id="activeId"
      :is-draft="isDraft"
      @new="onNewSession"
      @select="select"
      @rename="onRename"
      @pin="onPin"
      @delete="onDelete"
      @open-settings="settingsActive = true"
    />

    <SidebarInset class="min-w-0 flex-row overflow-hidden">
      <div class="flex min-h-0 min-w-[350px] flex-1 flex-col">
        <AppTitleBar :session="activeSession" @rename="onRename" />

        <main class="flex min-h-0 min-w-0 flex-1 flex-col">
          <div class="flex min-h-0 flex-1">
            <aside
              v-if="turnPoints.length > 3"
              class="hidden w-8 shrink-0 items-center justify-center pl-0.5 md:flex"
            >
              <Timeline
                :turns="turnPoints"
                :active="activeTurn"
                @select="onTurnSelect"
              />
            </aside>
            <MessageList
              ref="messageListRef"
              v-model:active-turn="activeTurn"
              :messages="messages"
              :streaming="streaming"
              :compacting="activeCompacting"
              :editable="queuedMessages.length === 0"
              @edit-message="editSentMessage"
            />
          </div>
          <Composer
            :disabled="!ready"
            :streaming="streaming"
            :model="composerModel"
            :connection-id="composerConnectionID"
            :reasoning-effort="composerReasoningEffort"
            :workspace="composerWorkspace"
            :workspace-locked="workspaceLocked"
            :approval="composerApproval"
            :connections="connectionModels"
            :models-loading="modelsLoading"
            :models-error="modelsError"
            :queued-messages="queuedMessages"
            :context-usage="composerContextUsage"
            :context-window="composerContextWindow"
            :has-session="!isDraft"
            @send="send"
            @stop="cancelTurn"
            @edit-queued="editQueuedMessage"
            @reorder-queued="reorderQueuedMessage"
            @dispatch-queued="dispatchQueuedMessage"
            @delete-queued="deleteQueuedMessage"
            @update:model-config="onModelConfigChange"
            @update:workspace="onWorkspaceChange"
            @update:approval="onApprovalChange"
            @refresh-models="refreshConnections"
          />
        </main>
      </div>

      <WorkbarPanel
        v-show="workbarOpen"
        :session-id="activeId || undefined"
        :workspace="composerWorkspace"
        :messages="messages"
        :obscured="workbarObscured"
        :ensure-session="ensureSession"
      />
    </SidebarInset>
  </SidebarProvider>

  <ApprovalDialog />
  <HistoryEditDialog />
</template>
