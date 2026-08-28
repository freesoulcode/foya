<script setup lang="ts">
import { ref, computed, watch, onMounted } from "vue";
import { useKernel } from "@/composables/useKernel";
import { usePlatform } from "@/composables/usePlatform";
import { SidebarProvider, SidebarInset } from "@/components/ui/sidebar";
import type { ApprovalMode, UpdateSessionPatch } from "@/lib/api";
import AppTitleBar from "@/components/AppTitleBar.vue";
import SessionSidebar from "@/components/chat/SessionSidebar.vue";
import MessageList from "@/components/chat/MessageList.vue";
import Composer from "@/components/chat/Composer.vue";
import SettingsDialog from "@/components/chat/SettingsDialog.vue";

const { isMac } = usePlatform();

const {
  ready,
  streaming,
  sessions,
  activeId,
  activeSession,
  isDraft,
  draft,
  availableModels,
  modelsLoading,
  modelsError,
  messages,
  connect,
  newSession,
  select,
  send,
  updateSession,
  renameSession,
  refreshModels,
} = useKernel();

const settingsOpen = ref(false);

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

onMounted(connect);
</script>

<template>
  <SidebarProvider class="h-svh">
    <SessionSidebar
      :is-mac="isMac"
      :sessions="sessions"
      :active-id="activeId"
      :is-draft="isDraft"
      @new="newSession"
      @select="select"
      @rename="onRename"
      @open-settings="settingsOpen = true"
    />

    <SidebarInset class="min-w-0">
      <AppTitleBar />

      <main class="flex min-h-0 flex-1 flex-col">
        <MessageList :messages="messages" :streaming="streaming" />
        <Composer
          :disabled="!ready"
          :streaming="streaming"
          :model="composerModel"
          :workspace="composerWorkspace"
          :approval="composerApproval"
          :available-models="availableModels"
          :models-loading="modelsLoading"
          :models-error="modelsError"
          @send="send"
          @update:model="onModelChange"
          @update:workspace="onWorkspaceChange"
          @update:approval="onApprovalChange"
          @refresh-models="refreshModels"
        />
      </main>
    </SidebarInset>

    <SettingsDialog v-model:open="settingsOpen" />
  </SidebarProvider>
</template>
