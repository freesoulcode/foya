<script setup lang="ts">
import type { ComponentPublicInstance } from "vue";
import MessageList from "@/components/chat/MessageList.vue";
import Timeline from "@/components/chat/Timeline.vue";
import Composer from "@/components/chat/Composer.vue";
import AskUserPanel from "@/components/chat/AskUserPanel.vue";
import ActivityBar from "@/components/chat/ActivityBar.vue";
import type { ChatWorkspaceContext } from "@/layouts/chatWorkspace";

const props = defineProps<{ workspace: ChatWorkspaceContext }>();
const {
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
  keepAllFileChanges,
  undoAllFileChanges,
  toggleFileReviewForceFile,
  forkSession,
  onOpenDiff,
  onOpenReviewFile,
  onOpenWorkflowFile,
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
  consumeComposerRestore,
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

function forkAtMessage(messageSeq: number) {
  if (!activeId.value) return;
  void forkSession(activeId.value, messageSeq).catch((error) => {
    console.error("Failed to fork chat from this message:", error);
  });
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
        @rewind-message="rewindSentMessage"
        @fork-message="forkAtMessage"
        @open-diff="onOpenDiff"
        @cancel-tool="cancelTool"
        @background-tool="backgroundTool"
        @terminal-tool="onViewToolInWorkbar"
        @open-link="onOpenLink"
        @update:active-turn="updateActiveTurn"
      />
    </div>
    <ActivityBar
      :tasks="activeSession?.tasks"
      :task-running="Boolean(activeId && runningSessions[activeId])"
      :commands="backgroundCommands"
      :review="fileReview"
      :review-disabled="streaming || queuedMessages.length > 0"
      :queued-messages="queuedMessages"
      :workflow="activeWorkflow"
      :can-open-workflow-files="Boolean(projectPath)"
      :streaming="streaming"
      @stop-command="stopBackgroundCommand"
      @open-command="onOpenBackgroundCommand"
      @keep-files="keepAllFileChanges"
      @undo-files="undoAllFileChanges"
      @toggle-force-file="toggleFileReviewForceFile"
      @open-file="onOpenReviewFile"
      @edit-queued="editQueuedMessage"
      @reorder-queued="reorderQueuedMessage"
      @dispatch-queued="dispatchQueuedMessage"
      @delete-queued="deleteQueuedMessage"
      @open-workflow-file="onOpenWorkflowFile"
      @approve-workflow="approveWorkflow(activeId)"
      @close-workflow="closeWorkflow(activeId)"
    />
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
      :context-usage="composerContextUsage"
      :context-window="composerContextWindow"
      :supports-image="composerSupportsImage"
      :has-session="!isDraft"
      :session-id="activeId"
      :browser-elements="pendingBrowserElements"
      :restore-text="composerRestore"
      @send="send"
      @command="executeComposerCommand"
      @stop="cancelTurn"
      @update:model-config="onModelConfigChange"
      @update:project-id="onProjectChange"
      @add-project="onAddProject"
      @update:approval="onApprovalChange"
      @refresh-models="refreshConnections"
      @remove-browser-element="removeBrowserElement"
      @clear-browser-elements="clearBrowserElements"
      @restore-browser-elements="restoreBrowserElements"
      @restore-consumed="consumeComposerRestore"
    />
  </main>
</template>
