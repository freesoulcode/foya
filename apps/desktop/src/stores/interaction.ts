import { ref } from "vue";
import { defineStore } from "pinia";
import {
  api,
  type ApprovalDecision,
  type BrowserActionRequest,
  type PendingQuestionBatch,
  type QuestionAnswer,
  type RewindFile,
} from "@/lib/api";

export interface PendingApproval {
  id: string;
  session: string;
  execution_session?: string;
  tool_name: string;
  action: string;
  detail: string;
  resource?: string;
  scope?: string;
}

export interface PendingHistoryRewind {
  sessionId: string;
  messageSeq: number;
  message: string;
  files: RewindFile[];
  headSeq: number;
  fileStateToken: string;
  forceFileKeys: string[];
  submitting?: boolean;
  error?: string;
}

export interface ComposerRestore {
  sessionId: string;
  text: string;
  nonce: number;
}

export const useInteractionStore = defineStore("interaction", () => {
  const pendingApprovals = ref<Record<string, PendingApproval>>({});
  const pendingQuestions = ref<Record<string, PendingQuestionBatch>>({});
  const pendingBrowserActions = ref<Record<string, BrowserActionRequest>>({});
  const pendingHistoryRewind = ref<PendingHistoryRewind | null>(null);
  const composerRestore = ref<ComposerRestore | null>(null);
  let composerRestoreNonce = 0;

  function requestApproval(approval: PendingApproval) {
    if (approval?.id) pendingApprovals.value[approval.id] = approval;
  }

  function resolveApprovalEvent(id: string) {
    if (id) delete pendingApprovals.value[id];
  }

  function requestQuestions(batch: PendingQuestionBatch) {
    if (batch?.id && batch.questions?.length) {
      pendingQuestions.value[batch.id] = batch;
    }
  }

  function resolveQuestionsEvent(id: string) {
    if (id) delete pendingQuestions.value[id];
  }

  function requestBrowserAction(action: BrowserActionRequest) {
    if (action?.id) pendingBrowserActions.value[action.id] = action;
  }

  function resolveBrowserActionEvent(id: string) {
    if (id) delete pendingBrowserActions.value[id];
  }

  function setPendingHistoryRewind(value: PendingHistoryRewind | null) {
    pendingHistoryRewind.value = value;
  }

  function restoreComposer(sessionId: string, text: string) {
    composerRestore.value = {
      sessionId,
      text,
      nonce: ++composerRestoreNonce,
    };
  }

  function consumeComposerRestore(nonce: number) {
    if (composerRestore.value?.nonce === nonce) {
      composerRestore.value = null;
    }
  }

  function cancelHistoryRewind() {
    pendingHistoryRewind.value = null;
  }

  function toggleHistoryRewindForceFile(key: string) {
    const pending = pendingHistoryRewind.value;
    if (!pending || pending.submitting) return;
    const selected = new Set(pending.forceFileKeys);
    if (selected.has(key)) selected.delete(key);
    else selected.add(key);
    pending.forceFileKeys = [...selected];
  }

  function clearSession(id: string) {
    for (const [approvalId, approval] of Object.entries(
      pendingApprovals.value,
    )) {
      if (approval.session === id) delete pendingApprovals.value[approvalId];
    }
    for (const [batchId, batch] of Object.entries(pendingQuestions.value)) {
      if (batch.session_id === id) delete pendingQuestions.value[batchId];
    }
    for (const [actionId, action] of Object.entries(
      pendingBrowserActions.value,
    )) {
      if (action.session_id === id) {
        delete pendingBrowserActions.value[actionId];
      }
    }
    if (pendingHistoryRewind.value?.sessionId === id) {
      pendingHistoryRewind.value = null;
    }
    if (composerRestore.value?.sessionId === id) {
      composerRestore.value = null;
    }
  }

  async function resolveApproval(
    sessionId: string,
    requestId: string,
    decision: ApprovalDecision,
  ) {
    await api.resolveApproval(sessionId, requestId, decision);
    resolveApprovalEvent(requestId);
  }

  async function answerQuestions(
    sessionId: string,
    batchId: string,
    answers: QuestionAnswer[],
  ) {
    await api.answerQuestions(sessionId, batchId, answers);
    resolveQuestionsEvent(batchId);
  }

  async function cancelQuestions(sessionId: string, batchId: string) {
    await api.cancelQuestions(sessionId, batchId);
    resolveQuestionsEvent(batchId);
  }

  return {
    pendingApprovals,
    pendingQuestions,
    pendingBrowserActions,
    pendingHistoryRewind,
    composerRestore,
    requestApproval,
    resolveApprovalEvent,
    requestQuestions,
    resolveQuestionsEvent,
    requestBrowserAction,
    resolveBrowserActionEvent,
    setPendingHistoryRewind,
    restoreComposer,
    consumeComposerRestore,
    cancelHistoryRewind,
    toggleHistoryRewindForceFile,
    clearSession,
    resolveApproval,
    answerQuestions,
    cancelQuestions,
  };
});
