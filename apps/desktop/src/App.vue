<script setup lang="ts">
import { ref, computed, watch, onMounted } from "vue";
import { useKernel } from "@/composables/useKernel";
import { usePlatform } from "@/composables/usePlatform";
import { SidebarProvider, SidebarInset } from "@/components/ui/sidebar";
import type { ApprovalMode, UpdateSessionPatch } from "@/lib/api";
import AppTitleBar from "@/components/AppTitleBar.vue";
import SessionSidebar from "@/components/chat/SessionSidebar.vue";
import MessageList from "@/components/chat/MessageList.vue";
import Timeline from "@/components/chat/Timeline.vue";
import Composer from "@/components/chat/Composer.vue";
import SettingsDialog from "@/components/chat/SettingsDialog.vue";
import ApprovalDialog from "@/components/chat/ApprovalDialog.vue";

const { isMac } = usePlatform();

const {
  ready,
  streaming,
  sessions,
  runningSessions,
  activeId,
  activeSession,
  isDraft,
  draft,
  availableModels,
  modelContextWindows,
  modelsLoading,
  modelsError,
  messages,
  queuedMessages,
  contextUsage,
  connect,
  newSession,
  select,
  send,
  cancelTurn,
  updateSession,
  renameSession,
  pinSession,
  deleteSession,
  editQueuedMessage,
  reorderQueuedMessage,
  deleteQueuedMessage,
  dispatchQueuedMessage,
  refreshModels,
} = useKernel();

const settingsOpen = ref(false);
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

// 设置弹窗关闭后刷新模型列表(provider 配置可能已变更)。
watch(settingsOpen, (open) => {
  if (!open) void refreshModels();
});

// 输入框当前展示的模型/工作目录/审批档位:草稿态读 draft,已建会话读 activeSession。
const composerModel = computed(() =>
  isDraft.value ? draft.model : activeSession.value?.model ?? ""
);
const composerWorkspace = computed(() =>
  isDraft.value ? draft.workspace : activeSession.value?.workspace ?? ""
);
const composerApproval = computed<ApprovalMode>(
  () =>
    (isDraft.value
      ? draft.approvalMode
      : (activeSession.value?.approval_mode as ApprovalMode)) || "ask"
);
const composerContextWindow = computed(
  () => modelContextWindows.value[composerModel.value] ?? 0
);
const composerContextUsage = computed(() =>
  contextUsage.value?.model === composerModel.value ? contextUsage.value : undefined
);

// 统一处理输入框里的配置变更:草稿态直接改本地 draft;已建会话调用 PATCH 实时落库。
function onModelChange(value: string) {
  if (isDraft.value) draft.model = value;
  else if (activeId.value) void updateSession(activeId.value, { model: value });
}

function onWorkspaceChange(value: string) {
  if (isDraft.value) draft.workspace = value;
  else if (activeId.value)
    void updateSession(activeId.value, { workspace: value });
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
  <SidebarProvider class="h-svh">
    <SessionSidebar
      :is-mac="isMac"
      :sessions="sessions"
      :running="runningSessions"
      :active-id="activeId"
      :is-draft="isDraft"
      @new="newSession"
      @select="select"
      @rename="onRename"
      @pin="onPin"
      @delete="onDelete"
      @open-settings="settingsOpen = true"
    />

    <SidebarInset class="min-w-0">
      <AppTitleBar :session="activeSession" @rename="onRename" />

      <main class="flex min-h-0 flex-1 flex-col">
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
          />
        </div>
        <Composer
          :disabled="!ready"
          :streaming="streaming"
          :model="composerModel"
          :workspace="composerWorkspace"
          :approval="composerApproval"
          :available-models="availableModels"
          :models-loading="modelsLoading"
          :models-error="modelsError"
          :queued-messages="queuedMessages"
          :context-usage="composerContextUsage"
          :context-window="composerContextWindow"
          @send="send"
          @stop="cancelTurn"
          @edit-queued="editQueuedMessage"
          @reorder-queued="reorderQueuedMessage"
          @dispatch-queued="dispatchQueuedMessage"
          @delete-queued="deleteQueuedMessage"
          @update:model="onModelChange"
          @update:workspace="onWorkspaceChange"
          @update:approval="onApprovalChange"
          @refresh-models="refreshModels"
        />
      </main>
    </SidebarInset>

    <SettingsDialog v-model:open="settingsOpen" />
    <ApprovalDialog />
  </SidebarProvider>
</template>
