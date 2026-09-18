<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { storeToRefs } from "pinia";
import { useI18n } from "vue-i18n";
import MessageList from "@/components/chat/MessageList.vue";
import Timeline from "@/components/chat/Timeline.vue";
import Composer from "@/components/chat/Composer.vue";
import AskUserPanel from "@/components/chat/AskUserPanel.vue";
import ActivityBar from "@/components/chat/ActivityBar.vue";
import { api } from "@/lib/api";
import type {
  ApprovalMode,
  AttachmentRef,
  BackgroundCommand,
  BrowserElementSelection,
  ReasoningEffort,
  WorkspaceEntry,
} from "@/lib/api";
import { useConversationStore } from "@/stores/conversation";

const TURN_LABEL_MAX = 40;

const props = withDefaults(
  defineProps<{
    sessionId: string;
    ownerSessionId: string;
    active?: boolean;
    browserElements?: BrowserElementSelection[];
  }>(),
  {
    active: false,
    browserElements: () => [],
  },
);

const emit = defineEmits<{
  (event: "open-diff", projectPath: string, diff: string): void;
  (
    event: "open-review-file",
    projectPath: string,
    path: string,
    view: "file" | "diff",
    diff?: string
  ): void;
  (event: "open-workflow-file", projectPath: string, path: string): void;
  (event: "open-artifact", attachment: AttachmentRef): void;
  (event: "open-link", url: string): void;
  (event: "open-background-command", command: BackgroundCommand): void;
  (event: "add-project"): void;
  (event: "open-plugins"): void;
  (event: "remove-browser-element", index: number): void;
  (event: "clear-browser-elements"): void;
  (event: "restore-browser-elements", elements: BrowserElementSelection[]): void;
  (event: "create-side-chat", sessionId: string, throughSeq?: number): void;
}>();

const conversationStore = useConversationStore();
const { t } = useI18n();
const {
  ready,
  projects,
  runningSessions,
  compactingSessions,
  connectionModels,
  modelsLoading,
  modelsError,
  pendingQuestions,
  composerRestore,
  workflowsBySession,
} = storeToRefs(conversationStore);
const {
  sessionById,
  messagesForSession,
  queuedMessagesForSession,
  contextUsageForSession,
  backgroundCommandsForSession,
  fileReviewForSession,
  sendToSession,
  rewindSentMessageForSession,
  keepAllFileChangesForSession,
  undoAllFileChangesForSession,
  toggleFileReviewForceFileForSession,
  cancelTurnForSession,
  cancelToolForSession,
  backgroundToolForSession,
  revealToolCommandForSession,
  stopBackgroundCommandForSession,
  updateSession,
  forkSession,
  editQueuedMessageForSession,
  reorderQueuedMessageForSession,
  deleteQueuedMessageForSession,
  dispatchQueuedMessageForSession,
  approveWorkflow,
  closeWorkflow,
  refreshConnections,
  consumeComposerRestore,
} = conversationStore;

const messageListRef = ref<InstanceType<typeof MessageList> | null>(null);
const activeTurn = ref(0);
const questionPanelExpanded = ref(false);

const session = computed(() => sessionById(props.sessionId));
const messages = computed(() => messagesForSession(props.sessionId));
const queuedMessages = computed(() => queuedMessagesForSession(props.sessionId));
const backgroundCommands = computed(() =>
  backgroundCommandsForSession(props.sessionId),
);
const fileReview = computed(() => fileReviewForSession(props.sessionId));
const contextUsage = computed(() => contextUsageForSession(props.sessionId));
const streaming = computed(() => Boolean(runningSessions.value[props.sessionId]));
const compacting = computed(() =>
  Boolean(compactingSessions.value[props.sessionId]),
);
const busy = computed(() => streaming.value || compacting.value);
const workflow = computed(
  () => workflowsBySession.value[props.sessionId] ?? null,
);
const questionBatch = computed(
  () =>
    Object.values(pendingQuestions.value).find(
      (batch) => batch.session_id === props.sessionId,
    ) ?? null,
);
const currentProjectID = computed(() => session.value?.project_id ?? "");
const currentProject = computed(
  () =>
    projects.value.find((project) => project.id === currentProjectID.value) ??
    null,
);
const projectPath = computed(() => currentProject.value?.path ?? "");
const projectLocked = computed(() => Boolean(session.value?.project_id));
const composerModel = computed(() => session.value?.model ?? "");
const composerConnectionID = computed(() => session.value?.connection_id ?? "");
const composerReasoningEffort = computed<ReasoningEffort>(
  () => session.value?.reasoning_effort ?? "",
);
const composerApproval = computed<ApprovalMode>(
  () => (session.value?.approval_mode as ApprovalMode | undefined) || "manual",
);
const composerContextWindow = computed(
  () =>
    connectionModels.value.find(
      (connection) => connection.id === composerConnectionID.value,
    )?.model_settings?.[composerModel.value]?.context_window ?? 0,
);
const composerSupportsImage = computed(() => {
  const connection = connectionModels.value.find(
    (item) => item.id === composerConnectionID.value,
  );
  if (!connection || !composerModel.value) return false;
  return connection.model_settings?.[composerModel.value]?.image_input ?? true;
});
const composerContextUsage = computed(() =>
  contextUsage.value?.model === composerModel.value
    ? contextUsage.value
    : undefined,
);
const turnPoints = computed(() =>
  messages.value
    .filter((message) => message.role === "user")
    .map((message) => {
      const text = message.content.replace(/\s+/g, " ").trim();
      return {
        label:
          text.length > TURN_LABEL_MAX
            ? `${text.slice(0, TURN_LABEL_MAX)}...`
            : text || t("New chat"),
      };
    }),
);

watch(
  () => turnPoints.value.length,
  (count) => {
    activeTurn.value = Math.max(0, count - 1);
  },
);

function onTurnSelect(index: number) {
  messageListRef.value?.scrollToTurn(index);
}

function updateQuestionPanelExpanded(value: boolean) {
  questionPanelExpanded.value = value;
}

function createSideChatFromMessage(messageSeq: number) {
  emit("create-side-chat", props.sessionId, messageSeq);
}

function forkAtMessage(messageSeq: number) {
  void forkSession(props.sessionId, messageSeq).catch((error) => {
    console.error("Failed to fork chat from this message:", error);
  });
}

function listWorkspaceFiles(): Promise<WorkspaceEntry[]> {
  return api.listWorkspaceFiles(props.sessionId);
}

async function executeComposerCommand(
  name: string,
  args: string,
  workspaceFiles: string[] = [],
) {
  await api.executeCommand(props.sessionId, name, args, workspaceFiles);
}

function send(
  text: string,
  files: File[] = [],
  browserElements: BrowserElementSelection[] = [],
  workspaceFiles: string[] = [],
  skillRef = "",
  restore?: () => void,
) {
  return sendToSession(
    props.sessionId,
    text,
    files,
    browserElements,
    workspaceFiles,
    skillRef,
    restore,
  );
}

function onModelConfigChange(value: {
  connectionID: string;
  model: string;
  reasoningEffort: ReasoningEffort;
}) {
  void updateSession(props.sessionId, {
    connection_id: value.connectionID,
    model: value.model,
    reasoning_effort: value.reasoningEffort,
  });
}

function onProjectChange(projectID: string) {
  if (projectLocked.value) return;
  void updateSession(props.sessionId, { project_id: projectID });
}

function onApprovalChange(value: ApprovalMode) {
  void updateSession(props.sessionId, { approval_mode: value });
}

async function onViewToolInWorkbar(toolCallId: string) {
  const command = await revealToolCommandForSession(props.sessionId, toolCallId);
  if (command) emit("open-background-command", command);
}

async function onBackgroundTool(toolCallId: string) {
  const command = await backgroundToolForSession(props.sessionId, toolCallId);
  if (command) emit("open-background-command", command);
}

function onOpenDiff(diff: string) {
  emit("open-diff", projectPath.value, diff);
}

function onOpenReviewFile(path: string, diff: string) {
  emit("open-review-file", projectPath.value, path, "diff", diff);
}

function onOpenWorkflowFile(path: string) {
  emit("open-workflow-file", projectPath.value, path);
}
</script>

<template>
  <main class="flex min-h-0 min-w-0 flex-1 flex-col bg-background">
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
        :active-turn="activeTurn"
        :session-id="sessionId"
        :project-path="projectPath"
        :messages="messages"
        :streaming="streaming"
        :compacting="compacting"
        :editable="queuedMessages.length === 0"
        :empty-hint="$t('Side chats disappear when closed.')"
        @rewind-message="rewindSentMessageForSession(sessionId, $event)"
        @fork-message="forkAtMessage"
        @side-chat-message="createSideChatFromMessage"
        @open-diff="onOpenDiff"
        @open-artifact="emit('open-artifact', $event)"
        @cancel-tool="cancelToolForSession(sessionId, $event)"
        @background-tool="onBackgroundTool"
        @terminal-tool="onViewToolInWorkbar"
        @open-link="emit('open-link', $event)"
        @update:active-turn="activeTurn = $event"
      />
    </div>
    <ActivityBar
      :tasks="session?.tasks"
      :task-running="Boolean(runningSessions[sessionId])"
      :commands="backgroundCommands"
      :review="fileReview"
      :review-disabled="busy || queuedMessages.length > 0"
      :queued-messages="queuedMessages"
      :workflow="workflow"
      :can-open-workflow-files="Boolean(projectPath)"
      :streaming="streaming"
      @stop-command="stopBackgroundCommandForSession(sessionId, $event)"
      @open-command="emit('open-background-command', $event)"
      @keep-files="keepAllFileChangesForSession(sessionId)"
      @undo-files="undoAllFileChangesForSession(sessionId)"
      @toggle-force-file="toggleFileReviewForceFileForSession(sessionId, $event)"
      @open-file="onOpenReviewFile"
      @edit-queued="(id, text) => editQueuedMessageForSession(sessionId, id, text)"
      @reorder-queued="
        (id, position) => reorderQueuedMessageForSession(sessionId, id, position)
      "
      @dispatch-queued="dispatchQueuedMessageForSession(sessionId, $event)"
      @delete-queued="deleteQueuedMessageForSession(sessionId, $event)"
      @open-workflow-file="onOpenWorkflowFile"
      @approve-workflow="approveWorkflow(sessionId)"
      @close-workflow="closeWorkflow(sessionId)"
    />
    <AskUserPanel
      :batch="questionBatch"
      @expanded-change="updateQuestionPanelExpanded"
    />
    <Composer
      v-if="!questionBatch || !questionPanelExpanded"
      :disabled="!ready"
      :streaming="streaming"
      :model="composerModel"
      :connection-id="composerConnectionID"
      :reasoning-effort="composerReasoningEffort"
      :project-id="currentProjectID"
      :projects="projects"
      :project-locked="projectLocked"
      :approval="composerApproval"
      :connections="connectionModels"
      :models-loading="modelsLoading"
      :models-error="modelsError"
      :context-usage="composerContextUsage"
      :context-window="composerContextWindow"
      :supports-image="composerSupportsImage"
      :load-workspace-files="listWorkspaceFiles"
      :has-session="true"
      :session-id="sessionId"
      :browser-elements="browserElements"
      :restore-text="composerRestore"
      @send="send"
      @command="executeComposerCommand"
      @stop="cancelTurnForSession(sessionId)"
      @update:model-config="onModelConfigChange"
      @update:project-id="onProjectChange"
      @add-project="emit('add-project')"
      @open-plugins="emit('open-plugins')"
      @update:approval="onApprovalChange"
      @refresh-models="refreshConnections"
      @remove-browser-element="emit('remove-browser-element', $event)"
      @clear-browser-elements="emit('clear-browser-elements')"
      @restore-browser-elements="emit('restore-browser-elements', $event)"
      @restore-consumed="consumeComposerRestore"
    />
  </main>
</template>
