import type { ComputedRef, Ref } from "vue";
import type {
  ApprovalMode,
  BackgroundCommand,
  BrowserElementSelection,
  ContextUsage,
  PendingQuestionBatch,
  ProjectInfo,
  ReasoningEffort,
  WorkflowRecord,
} from "@/lib/api";
import type { useKernel } from "@/composables/useKernel";

type Kernel = ReturnType<typeof useKernel>;

type KernelBindings = Pick<
  Kernel,
  | "ready"
  | "streaming"
  | "messages"
  | "queuedMessages"
  | "backgroundCommands"
  | "fileReview"
  | "connectionModels"
  | "modelsLoading"
  | "modelsError"
  | "activeId"
  | "activeSession"
  | "isDraft"
  | "runningSessions"
  | "composerRestore"
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
  | "send"
  | "cancelTurn"
  | "editQueuedMessage"
  | "reorderQueuedMessage"
  | "dispatchQueuedMessage"
  | "deleteQueuedMessage"
  | "refreshConnections"
>;

export interface ChatWorkspaceContext extends KernelBindings {
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
