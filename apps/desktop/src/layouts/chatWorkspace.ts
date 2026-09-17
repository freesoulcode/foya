import {
  inject,
  provide,
  type ComputedRef,
  type InjectionKey,
  type Ref,
} from "vue";
import type {
  ApprovalMode,
  AttachmentRef,
  BackgroundCommand,
  BrowserElementSelection,
  ChatMessage,
  ConnectionModelGroup,
  ContextUsage,
  PendingQuestionBatch,
  ProjectInfo,
  QueuedMessage,
  ReasoningEffort,
  Session,
  WorkflowRecord,
} from "@/lib/api";
import type { useConversationStore } from "@/stores/conversation";
import type { PendingFileReview } from "@/stores/conversation";
import type { ComposerRestore } from "@/stores/interaction";

type ConversationStore = ReturnType<typeof useConversationStore>;

type ConversationBindings = Pick<
  ConversationStore,
  | "rewindSentMessage"
  | "consumeComposerRestore"
  | "keepAllFileChanges"
  | "undoAllFileChanges"
  | "toggleFileReviewForceFile"
  | "forkSession"
  | "cancelTool"
  | "backgroundTool"
  | "stopBackgroundCommand"
  | "approveWorkflow"
  | "closeWorkflow"
  | "send"
  | "cancelTurn"
  | "editQueuedMessage"
  | "reorderQueuedMessage"
  | "dispatchQueuedMessage"
  | "deleteQueuedMessage"
  | "refreshConnections"
> & {
  ready: Ref<boolean>;
  streaming: Ref<boolean>;
  messages: ComputedRef<ChatMessage[]>;
  queuedMessages: ComputedRef<QueuedMessage[]>;
  backgroundCommands: ComputedRef<BackgroundCommand[]>;
  fileReview: ComputedRef<PendingFileReview | undefined>;
  connectionModels: Ref<ConnectionModelGroup[]>;
  modelsLoading: Ref<boolean>;
  modelsError: Ref<string>;
  activeId: Ref<string>;
  activeSession: ComputedRef<Session | undefined>;
  isDraft: ComputedRef<boolean>;
  runningSessions: Ref<Record<string, boolean>>;
  composerRestore: Ref<ComposerRestore | null>;
};

export interface ChatWorkspaceContext extends ConversationBindings {
  activeTurn: Ref<number>;
  turnPoints: ComputedRef<Array<{ label: string }>>;
  messageListRef: Ref<{ scrollToTurn: (turnIndex: number) => void } | null>;
  projectPath: ComputedRef<string>;
  activeCompacting: ComputedRef<boolean>;
  activeWorkflow: ComputedRef<WorkflowRecord | null>;
  activeQuestionBatch: ComputedRef<PendingQuestionBatch | null>;
  questionPanelExpanded: Ref<boolean>;
  composerModel: ComputedRef<string>;
  composerConnectionID: ComputedRef<string>;
  composerReasoningEffort: ComputedRef<ReasoningEffort>;
  currentProjectID: ComputedRef<string>;
  activeProjects: ComputedRef<ProjectInfo[]>;
  projectLocked: ComputedRef<boolean>;
  composerApproval: ComputedRef<ApprovalMode>;
  composerContextUsage: ComputedRef<ContextUsage | undefined>;
  composerContextWindow: ComputedRef<number>;
  composerSupportsImage: ComputedRef<boolean>;
  pendingBrowserElements: Ref<BrowserElementSelection[]>;
  onTurnSelect: (index: number) => void;
  onOpenDiff: (diff: string) => void;
  onOpenReviewFile: (path: string, diff: string) => void;
  onOpenWorkflowFile: (path: string) => void;
  onOpenArtifact: (attachment: AttachmentRef) => void;
  onViewToolInWorkbar: (toolCallId: string) => Promise<void>;
  onOpenLink: (url: string) => Promise<void>;
  onOpenBackgroundCommand: (command: BackgroundCommand) => void;
  executeComposerCommand: (name: string, args: string) => Promise<void>;
  onModelConfigChange: (value: {
    connectionID: string;
    model: string;
    reasoningEffort: ReasoningEffort;
  }) => void;
  onProjectChange: (value: string) => void;
  onAddProject: () => void;
  onApprovalChange: (value: ApprovalMode) => void;
  removeBrowserElement: (index: number) => void;
  clearBrowserElements: () => void;
  restoreBrowserElements: (elements: BrowserElementSelection[]) => void;
}

const chatWorkspaceKey: InjectionKey<ChatWorkspaceContext> =
  Symbol("chat-workspace");

export function provideChatWorkspace(workspace: ChatWorkspaceContext) {
  provide(chatWorkspaceKey, workspace);
}

export function useChatWorkspace(): ChatWorkspaceContext {
  const workspace = inject(chatWorkspaceKey);
  if (!workspace) {
    throw new Error("useChatWorkspace must be used inside ChatLayout");
  }
  return workspace;
}
