<script setup lang="ts">
import type { ComponentPublicInstance } from "vue";
import MessageList from "@/components/chat/MessageList.vue";
import TaskProgress from "@/components/chat/TaskProgress.vue";
import Timeline from "@/components/chat/Timeline.vue";
import Composer from "@/components/chat/Composer.vue";
import AskUserPanel from "@/components/chat/AskUserPanel.vue";
import BackgroundCommandsPanel from "@/components/chat/BackgroundCommandsPanel.vue";
import { Button } from "@/components/ui/button";
import type { ChatWorkspaceContext } from "@/layouts/chatWorkspace";

const props = defineProps<{ workspace: ChatWorkspaceContext }>();
const {
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
} = props.workspace;

function setMessageListRef(instance: Element | ComponentPublicInstance | null) {
  messageListRef.value = instance as InstanceType<typeof MessageList> | null;
}

function updateActiveTurn(value: number) {
  activeTurn.value = value;
}

function updateQuestionPanelExpanded(value: boolean) {
  questionPanelExpanded.value = value;
}
</script>

<template>
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
        :ref="setMessageListRef"
        :active-turn="activeTurn"
        :session-id="activeId"
        :project-path="projectPath"
        :messages="messages"
        :streaming="streaming"
        :compacting="activeCompacting"
        :editable="queuedMessages.length === 0"
        @edit-message="editSentMessage"
        @open-diff="onOpenDiff"
        @cancel-tool="cancelTool"
        @background-tool="backgroundTool"
        @terminal-tool="onViewToolInWorkbar"
        @open-link="onOpenLink"
        @update:active-turn="updateActiveTurn"
      />
    </div>
    <TaskProgress
      :tasks="activeSession?.tasks"
      :running="Boolean(activeId && runningSessions[activeId])"
    />
    <BackgroundCommandsPanel
      :commands="backgroundCommands"
      @stop="stopBackgroundCommand"
      @open="onOpenBackgroundCommand"
    />
    <div
      v-if="activeWorkflow?.status === 'ready'"
      class="mx-auto flex w-full max-w-3xl items-center justify-between gap-4 border-x border-t border-border px-4 py-3"
    >
      <div class="min-w-0">
        <p class="text-sm font-medium">{{ activeWorkflow.kind }} 已就绪</p>
        <p class="truncate text-xs text-muted-foreground">{{ activeWorkflow.goal }}</p>
      </div>
      <Button size="sm" @click="approveWorkflow(activeId)">
        批准并执行
      </Button>
    </div>
    <AskUserPanel
      :batch="activeQuestionBatch"
      @expanded-change="updateQuestionPanelExpanded"
    />
    <Composer
      v-if="!activeQuestionBatch || !questionPanelExpanded"
      :disabled="!ready"
      :streaming="streaming"
      :model="composerModel"
      :connection-id="composerConnectionID"
      :reasoning-effort="composerReasoningEffort"
      :project-id="currentProjectID"
      :projects="activeProjects"
      :project-locked="projectLocked"
      :approval="composerApproval"
      :connections="connectionModels"
      :models-loading="modelsLoading"
      :models-error="modelsError"
      :queued-messages="queuedMessages"
      :context-usage="composerContextUsage"
      :context-window="composerContextWindow"
      :supports-image="composerSupportsImage"
      :has-session="!isDraft"
      :session-id="activeId"
      :browser-elements="pendingBrowserElements"
      @send="send"
      @command="executeComposerCommand"
      @stop="cancelTurn"
      @edit-queued="editQueuedMessage"
      @reorder-queued="reorderQueuedMessage"
      @dispatch-queued="dispatchQueuedMessage"
      @delete-queued="deleteQueuedMessage"
      @update:model-config="onModelConfigChange"
      @update:project-id="onProjectChange"
      @add-project="onAddProject"
      @update:approval="onApprovalChange"
      @refresh-models="refreshConnections"
      @remove-browser-element="removeBrowserElement"
      @clear-browser-elements="clearBrowserElements"
      @restore-browser-elements="restoreBrowserElements"
    />
  </main>
</template>
