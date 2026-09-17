import { ref, computed } from "vue";
import { defineStore, storeToRefs } from "pinia";
import {
  api,
  type Session,
  type ChatMessage,
  type QueuedMessage,
  type ContextUsage,
  type FileReview,
  type AgentRunSnapshot,
  type AgentBudget,
  type ToolCallView,
  type AttachmentRef,
  type WorkflowRecord,
  type PendingQuestionBatch,
  type BackgroundCommand,
  type BrowserElementSelection,
  type BrowserActionRequest,
} from "@/lib/api";
import { translate } from "@/i18n";
import { useConnectionStore } from "@/stores/connection";
import {
  useInteractionStore,
  type PendingApproval,
} from "@/stores/interaction";
import { useSessionStore } from "@/stores/session";
import {
  deleteSessionRuntime,
  getStreamingIndex,
  isDeletedSession,
  restoreSessionRuntime,
  setStreamingIndex,
  subscribeSessionEvents,
} from "@/services/kernelEventRuntime";

export type {
  PendingApproval,
  PendingHistoryRewind,
} from "@/stores/interaction";

export interface PendingFileReview extends FileReview {
  forceFileKeys: string[];
  submitting: boolean;
  error: string;
}

// Public subset of Go event.Event.
interface KernelEvent {
  seq: number;
  kind: string;
  session: string;
  time: string;
  payload?: unknown;
}

export const useConversationStore = defineStore("conversation", () => {
  const connectionStore = useConnectionStore();
  const sessionStore = useSessionStore();
  const interactionStore = useInteractionStore();
  const { ready, connecting, connectError } = storeToRefs(connectionStore);
  const {
    sessions,
    projects,
    activeId,
    activeSession,
    isDraft,
    connectionModels,
    defaultModels,
    modelsLoading,
    modelsError,
  } = storeToRefs(sessionStore);
  const draft = sessionStore.draft;
  const {
    pendingApprovals,
    pendingQuestions,
    pendingBrowserActions,
    pendingHistoryRewind,
    composerRestore,
  } = storeToRefs(interactionStore);

const streaming = ref(false);
// Running state is tracked per chat, independent of the active chat.
const runningSessions = ref<Record<string, boolean>>({});
// Unread results produced by inactive chats are cleared when opened.
const unreadSessions = ref<Record<string, boolean>>({});
const compactingSessions = ref<Record<string, boolean>>({});

// Per-chat messages and subscriptions persist across navigation.
const messagesBySession = ref<Record<string, ChatMessage[]>>({});
// The kernel owns queues; this state stores their SSE and GET projections.
const queuedBySession = ref<Record<string, QueuedMessage[]>>({});
const usageBySession = ref<Record<string, ContextUsage>>({});
const agentRunsBySession = ref<Record<string, AgentRunSnapshot[]>>({});
const agentBudgetBySession = ref<Record<string, AgentBudget>>({});
const workflowsBySession = ref<Record<string, WorkflowRecord | null>>({});
  const backgroundCommandsBySession = ref<Record<string, BackgroundCommand[]>>(
    {},
  );
const fileReviewsBySession = ref<Record<string, PendingFileReview>>({});

  const activeMessages = computed<ChatMessage[]>(
    () => messagesBySession.value[activeId.value] ?? [],
  );
const activeQueuedMessages = computed<QueuedMessage[]>(
    () => queuedBySession.value[activeId.value] ?? [],
);
const activeUsage = computed<ContextUsage | undefined>(
    () => usageBySession.value[activeId.value],
);
const activeBackgroundCommands = computed<BackgroundCommand[]>(
    () => backgroundCommandsBySession.value[activeId.value] ?? [],
);
const activeFileReview = computed<PendingFileReview | undefined>(
    () => fileReviewsBySession.value[activeId.value],
);
function ensureBucket(id: string) {
  if (!messagesBySession.value[id]) messagesBySession.value[id] = [];
}

// Find the latest assistant message that can receive a tool call.
function findLastAssistantIdx(bucket: ChatMessage[]): number {
  for (let i = bucket.length - 1; i >= 0; i--) {
    if (bucket[i].role === "assistant") return i;
  }
  return -1;
}

  function matchingAgentTools(
    sessionId: string,
    toolCallId?: string,
  ): ToolCallView[] {
  if (!toolCallId) return [];
  const tools: ToolCallView[] = [];
  for (const msg of messagesBySession.value[sessionId] ?? []) {
    for (const item of msg.tool_calls ?? []) {
      if (item.id === toolCallId) tools.push(item);
    }
    for (const segment of msg.segments ?? []) {
      if (segment.kind === "tool" && segment.tool.id === toolCallId) {
        tools.push(segment.tool);
      }
    }
  }
  return tools;
}

  async function hydrateAgentRun(
    parentSessionId: string,
    run: AgentRunSnapshot,
  ) {
  const runs = agentRunsBySession.value[parentSessionId] ?? [];
  const index = runs.findIndex((item) => item.id === run.id);
  if (index >= 0) runs[index] = run;
  else runs.push(run);
  agentRunsBySession.value[parentSessionId] = runs;

  const tools = matchingAgentTools(parentSessionId, run.parent_tool_call_id);
  for (const item of tools) {
    item.agent_run = run;
    item.child_session_id = run.child_session_id;
    item.agent_ref = run.agent_ref;
    item.agent_name = run.agent_name;
  }
  if (!run.child_session_id) return;
  const childId = run.child_session_id;
  ensureBucket(childId);
  if (messagesBySession.value[childId].length === 0) {
    const history = await api.loadHistory(childId);
    messagesBySession.value[childId] = normalizeHistory(history);
  }
  for (const item of tools) {
    item.child_messages = messagesBySession.value[childId];
  }
  await subscribe(childId);
}

// Ensure a message has ordered segments and return them.
  function ensureSegments(
    msg: ChatMessage,
  ): NonNullable<ChatMessage["segments"]> {
  if (!msg.segments) msg.segments = [];
  return msg.segments;
}

// Append a streamed delta, merging adjacent segments of the same kind.
  function appendDelta(
    msg: ChatMessage,
    kind: "reasoning" | "text",
    delta: string,
  ) {
  const segs = ensureSegments(msg);
  const last = segs[segs.length - 1];
  if (last && last.kind === kind) {
    last.text += delta;
  } else {
    segs.push({ kind, text: delta });
  }
}

// Apply one SSE event to a chat projection.
function handleEvent(sessionId: string, data: string) {
  let ev: KernelEvent;
  try {
    ev = JSON.parse(data);
  } catch {
    return;
  }
  // Deletion can race with delivery, so discard late events for deleted chats.
    if (isDeletedSession(sessionId)) return;
  ensureBucket(sessionId);
  const bucket = messagesBySession.value[sessionId];

  switch (ev.kind) {
    case "message_delta": {
      const delta = ev.payload as string;
        let idx = getStreamingIndex(sessionId);
      if (idx < 0) {
        // Recreate the optimistic bubble when it is missing after a reload.
        bucket.push({ role: "assistant", content: "" });
        idx = bucket.length - 1;
          setStreamingIndex(sessionId, idx);
      }
      // The first delta clears pending or error state.
      bucket[idx].error = false;
      bucket[idx].content += delta;
      appendDelta(bucket[idx], "text", delta);
      if (sessionId === activeId.value) streaming.value = true;
      break;
    }
    case "reasoning_delta": {
      // Preserve repeated reasoning phases by appending ordered segments.
      const delta = ev.payload as string;
        let idx = getStreamingIndex(sessionId);
      if (idx < 0) {
        bucket.push({ role: "assistant", content: "" });
        idx = bucket.length - 1;
          setStreamingIndex(sessionId, idx);
      }
      bucket[idx].error = false;
      appendDelta(bucket[idx], "reasoning", delta);
      if (sessionId === activeId.value) streaming.value = true;
      break;
    }
    case "message_end":
    case "message_imported": {
      const m = { ...(ev.payload as ChatMessage), event_seq: ev.seq };
      // Kernel events project user messages to every connected client.
      if (m.role === "user") {
        bucket.push(m);
        break;
      }
        const idx = getStreamingIndex(sessionId);
      if (m.role === "assistant" && idx >= 0) {
        bucket[idx].event_seq = m.event_seq;
        // Prefer accumulated deltas and use the final payload as a fallback.
        if (!bucket[idx].content && m.content) {
          bucket[idx].content = m.content;
          appendDelta(bucket[idx], "text", m.content);
        }
        // Restore reasoning from the final payload if no delta was received.
          if (
            m.reasoning &&
            !bucket[idx].segments?.some((s) => s.kind === "reasoning")
          ) {
            ensureSegments(bucket[idx]).unshift({
              kind: "reasoning",
              text: m.reasoning,
            });
        }
          if (m.turn_started_at)
            bucket[idx].turn_started_at = m.turn_started_at;
          if (m.turn_completed_at)
            bucket[idx].turn_completed_at = m.turn_completed_at;
        if (m.turn_status) bucket[idx].turn_status = m.turn_status;
        if (m.turn_reason) bucket[idx].turn_reason = m.turn_reason;
        bucket[idx].error = false;
        // Tool-call messages do not end the turn. Keep the same streaming slot
        // so later deltas remain in one bubble; turn_complete or error resets it.
        if (!m.tool_calls) {
            setStreamingIndex(sessionId, -1);
        }
      }
      // Tool results are rendered from tool_end events.
      break;
    }
    case "history_rewound": {
      const p = ev.payload as { target_user_seq?: number };
      const target = Number(p?.target_user_seq ?? 0);
      const index = bucket.findIndex(
          (message) => message.role === "user" && message.event_seq === target,
      );
      if (index >= 0) bucket.splice(index);
        setStreamingIndex(sessionId, -1);
      delete usageBySession.value[sessionId];
      void refreshFileReview(sessionId);
      break;
    }
    case "file_review_resolved": {
      delete fileReviewsBySession.value[sessionId];
      break;
    }
    case "tool_begin": {
      const p = ev.payload as {
        id: string;
        name: string;
        input: string;
        status?: ToolCallView["status"];
      };
      // Attach the tool call to the current streaming assistant message.
      const asstIdx = findLastAssistantIdx(bucket);
      if (asstIdx >= 0) {
        const msg = bucket[asstIdx];
        if (!msg.tool_calls) msg.tool_calls = [];
        // Replay may already contain this tool call.
        if (!msg.tool_calls.find((tc) => tc.id === p.id)) {
          const tool = {
            id: p.id,
            name: p.name,
            input: p.input,
            status: p.status ?? "queued",
          };
          msg.tool_calls.push(tool);
          // Preserve ordering by appending a tool segment.
          ensureSegments(msg).push({ kind: "tool", tool });
        }
      }
      break;
    }
    case "tool_update": {
      // Fill complete arguments before execution. tool_begin may contain only
      // a name while arguments are still streaming.
      const p = ev.payload as {
        id: string;
        name: string;
        input: string;
        status?: ToolCallView["status"];
        output?: string;
        is_error?: boolean;
        diff?: string;
        attachments?: AttachmentRef[];
      };
      const asstIdx = findLastAssistantIdx(bucket);
      if (asstIdx < 0) break;
      const msg = bucket[asstIdx];
      let tc = msg.tool_calls?.find((t) => t.id === p.id);
      if (!tc) {
        if (!msg.tool_calls) msg.tool_calls = [];
        tc = {
          id: p.id,
          name: p.name,
          input: p.input,
          status: p.status ?? "queued",
        };
        msg.tool_calls.push(tc);
        ensureSegments(msg).push({ kind: "tool", tool: tc });
      } else {
        if (p.name) tc.name = p.name;
        if (p.input) tc.input = p.input;
      }
      if (p.status) tc.status = p.status;
      if (p.output) tc.output = p.output;
      if (p.is_error) tc.status = "error";
      if (p.diff) tc.diff = p.diff;
      if (p.attachments) tc.attachments = p.attachments;
      const seg = msg.segments?.find(
          (s) => s.kind === "tool" && s.tool.id === p.id,
      );
      if (seg && seg.kind === "tool") {
        if (p.name) seg.tool.name = p.name;
        if (p.input) seg.tool.input = p.input;
        if (p.status) seg.tool.status = p.status;
        if (p.output) seg.tool.output = p.output;
        if (p.is_error) seg.tool.status = "error";
        if (p.diff) seg.tool.diff = p.diff;
        if (p.attachments) seg.tool.attachments = p.attachments;
      }
      break;
    }
    case "tool_end": {
      const p = ev.payload as {
        id: string;
        name: string;
        output: string;
        is_error: boolean;
        diff?: string;
        attachments?: AttachmentRef[];
      };
      const asstIdx = findLastAssistantIdx(bucket);
      if (asstIdx >= 0) {
        const tc = bucket[asstIdx].tool_calls?.find((t) => t.id === p.id);
        if (tc) {
          tc.status = p.is_error ? "error" : "done";
          tc.output = p.output;
          if (p.diff) tc.diff = p.diff;
          if (p.attachments) tc.attachments = p.attachments;
        }
        // Segments and tool_calls hold different object references.
        const seg = bucket[asstIdx].segments?.find(
            (s) => s.kind === "tool" && s.tool.id === p.id,
        );
        if (seg && seg.kind === "tool") {
          seg.tool.status = p.is_error ? "error" : "done";
          seg.tool.output = p.output;
          if (p.diff) seg.tool.diff = p.diff;
          if (p.attachments) seg.tool.attachments = p.attachments;
        }
      }
      break;
    }
    case "subagent_queued":
    case "subagent_running":
    case "subagent_started":
    case "subagent_completed":
    case "subagent_failed":
    case "subagent_cancelled":
    case "subagent_interrupted": {
      const run = ev.payload as AgentRunSnapshot;
      if (run?.id) {
        void hydrateAgentRun(sessionId, run);
      }
      break;
    }
    case "agent_budget_updated":
    case "agent_budget_exceeded": {
      agentBudgetBySession.value[sessionId] = ev.payload as AgentBudget;
      break;
    }
    case "approval_request": {
      const p = ev.payload as PendingApproval;
        interactionStore.requestApproval(p);
      break;
    }
    case "approval_resolved": {
      const p = ev.payload as { id: string };
        interactionStore.resolveApprovalEvent(p.id);
      break;
    }
    case "question_requested": {
      const p = ev.payload as PendingQuestionBatch;
        interactionStore.requestQuestions(p);
      break;
    }
    case "question_resolved": {
      const p = ev.payload as { id?: string };
        if (p?.id) interactionStore.resolveQuestionsEvent(p.id);
      break;
    }
    case "browser_action_requested": {
      const action = ev.payload as BrowserActionRequest;
        interactionStore.requestBrowserAction(action);
      break;
    }
    case "browser_action_resolved": {
      const result = ev.payload as { id?: string };
        if (result?.id) interactionStore.resolveBrowserActionEvent(result.id);
      break;
    }
    case "turn_started": {
      const p = ev.payload as { started_at?: string } | null;
      runningSessions.value[sessionId] = true;
        let idx = getStreamingIndex(sessionId);
      if (idx < 0) {
        bucket.push({ role: "assistant", content: "" });
        idx = bucket.length - 1;
          setStreamingIndex(sessionId, idx);
      }
      bucket[idx].turn_started_at = p?.started_at ?? ev.time;
      if (sessionId === activeId.value) streaming.value = true;
      break;
    }
    case "turn_complete": {
      const p = ev.payload as {
        started_at?: string;
        completed_at?: string;
        status?: ChatMessage["turn_status"];
        reason?: string;
      } | null;
        const streamingMessageIdx = getStreamingIndex(sessionId);
        const idx =
          streamingMessageIdx >= 0
            ? streamingMessageIdx
            : findLastAssistantIdx(bucket);
      if (idx >= 0) {
        bucket[idx].turn_started_at ??= p?.started_at ?? ev.time;
        bucket[idx].turn_completed_at = p?.completed_at ?? ev.time;
        if (p?.status) bucket[idx].turn_status = p.status;
        if (p?.reason) bucket[idx].turn_reason = p.reason;
      } else if (p?.status === "cancelled") {
        bucket.push({
          role: "assistant",
          content: "",
          turn_started_at: p.started_at ?? ev.time,
          turn_completed_at: p.completed_at ?? ev.time,
          turn_status: p.status,
          turn_reason: p.reason,
        });
      }
        setStreamingIndex(sessionId, -1);
      delete runningSessions.value[sessionId];
      if (sessionId === activeId.value) {
        streaming.value = false;
      } else if (p?.status !== "cancelled") {
        unreadSessions.value[sessionId] = true;
      }
      void refreshFileReview(sessionId);
      break;
    }
    case "queue_updated": {
      const p = ev.payload as { items?: QueuedMessage[] };
      queuedBySession.value[sessionId] = p?.items ?? [];
      break;
    }
    case "usage_updated": {
      const usage = ev.payload as ContextUsage;
      if (usage && usage.total_tokens > 0) {
        usageBySession.value[sessionId] = usage;
      }
      break;
    }
    case "compaction_started":
      compactingSessions.value[sessionId] = true;
      if (sessionId === activeId.value) streaming.value = true;
      break;
    case "compaction_completed":
      delete compactingSessions.value[sessionId];
      if (sessionId === activeId.value) {
        streaming.value = Boolean(runningSessions.value[sessionId]);
      }
      break;
    case "compaction_failed":
      delete compactingSessions.value[sessionId];
      if (sessionId === activeId.value) {
        streaming.value = Boolean(runningSessions.value[sessionId]);
      }
      break;
    case "session_updated": {
      // Replace updated chat metadata by ID.
      const updated = ev.payload as Session;
      if (updated && updated.id) {
          sessionStore.mergeSession(updated);
      }
      break;
    }
    case "session_deleted": {
      // A remote deletion removes local state and selects another chat or a draft.
      const p = ev.payload as { id?: string };
      const id = p?.id ?? sessionId;
      removeSession(id);
      break;
    }
    case "workflow_updated":
      workflowsBySession.value[sessionId] = ev.payload as WorkflowRecord;
      break;
    case "background_command_updated": {
      const command = ev.payload as BackgroundCommand;
      if (!command?.command_id) break;
      const current = backgroundCommandsBySession.value[sessionId] ?? [];
      const index = current.findIndex(
          (item) => item.command_id === command.command_id,
      );
      if (index >= 0) {
        const next = [...current];
        next[index] = command;
        backgroundCommandsBySession.value[sessionId] = next;
      } else {
        backgroundCommandsBySession.value[sessionId] = [command, ...current];
      }
      break;
    }
    case "error": {
      // Reuse the current empty assistant bubble for errors.
      delete runningSessions.value[sessionId];
      const text = `⚠️ ${String(ev.payload)}`;
        const idx = getStreamingIndex(sessionId);
      if (idx >= 0) {
        bucket[idx].content = text;
        bucket[idx].error = true;
          setStreamingIndex(sessionId, -1);
      } else {
        bucket.push({ role: "assistant", content: text, error: true });
      }
      if (sessionId === activeId.value) streaming.value = false;
      break;
    }
  }
}

// Subscribe to a chat event stream idempotently.
async function subscribe(sessionId: string) {
    await subscribeSessionEvents(sessionId, (data) =>
      handleEvent(sessionId, data),
    );
  }

async function refreshFileReview(sessionId: string) {
  try {
    const review = await api.loadFileReview(sessionId);
      if (isDeletedSession(sessionId)) return;
    const existing = fileReviewsBySession.value[sessionId];
    const availableKeys = new Set(
      review.files
        .filter((file) => file.status === "modified")
          .map((file) => file.key),
    );
    fileReviewsBySession.value[sessionId] = {
      ...review,
      forceFileKeys: (existing?.forceFileKeys ?? []).filter((key) =>
          availableKeys.has(key),
      ),
      submitting: false,
      error: "",
    };
  } catch (error) {
    console.error("Failed to load files awaiting review:", error);
  }
}

function refreshActiveFileReview() {
  const sessionId = activeId.value;
  if (!sessionId || !fileReviewsBySession.value[sessionId]?.files.length) {
    return Promise.resolve();
  }
  return refreshFileReview(sessionId);
      }

// Wait for the sidecar, load chats, and select an existing chat or draft.
async function connect() {
  if (ready.value || connecting.value) return;
    connectionStore.beginConnecting();
  try {
      await sessionStore.loadInitialData();
    if (sessions.value.length > 0) {
      await select(sessions.value[0].id);
    } else {
        sessionStore.setActive("");
      ensureBucket("");
    }
      connectionStore.markReady();
    // Load model catalogs after the kernel connection is ready.
      void sessionStore.refreshConnections();
  } catch (e) {
    // Surface startup failures so missing commands and kernel issues are visible.
      connectionStore.fail(e);
    console.error("Failed to connect to kernel:", e);
  } finally {
      connectionStore.finishConnecting();
  }
}

// Enter draft state without creating a chat until the first message is sent.
function newSession(projectID = "") {
    sessionStore.resetDraft(projectID);
  ensureBucket("");
  streaming.value = false;
}

// Fold flat backend history into assistant bubbles with ordered segments.
// User messages delimit turns; consecutive assistant and tool messages merge.
function normalizeHistory(history: ChatMessage[]): ChatMessage[] {
  const out: ChatMessage[] = [];
  let cur: ChatMessage | null = null;

  for (const m of history) {
    if (m.role === "user") {
      out.push(m);
      cur = null;
      continue;
    }
    if (m.role === "assistant") {
      if (!cur) {
        cur = {
          role: "assistant",
          content: "",
          event_seq: m.event_seq,
          segments: [],
          tool_calls: [],
        };
        out.push(cur);
      }
      cur.event_seq = m.event_seq;
      const segs = cur.segments!;
      if (m.reasoning) segs.push({ kind: "reasoning", text: m.reasoning });
      if (m.content) {
        segs.push({ kind: "text", text: m.content });
        cur.content += m.content;
      }
      if (m.turn_started_at) cur.turn_started_at = m.turn_started_at;
      if (m.turn_completed_at) cur.turn_completed_at = m.turn_completed_at;
      if (m.turn_status) cur.turn_status = m.turn_status;
      if (m.turn_reason) cur.turn_reason = m.turn_reason;
      if (m.tool_calls) {
        for (const tc of m.tool_calls) {
          const tool = { ...tc, status: "queued" as const };
          cur.tool_calls!.push(tool);
          segs.push({ kind: "tool", tool });
        }
      }
      continue;
    }
    if (m.role === "tool") {
      // Fill the matching tool segment using tool_call_id.
      if (cur) {
        if (m.event_seq) cur.event_seq = m.event_seq;
        const seg = cur.segments!.find(
            (s) => s.kind === "tool" && s.tool.id === m.tool_call_id,
        );
        if (seg && seg.kind === "tool") {
          seg.tool.output = m.content;
          seg.tool.status = m.error ? "error" : "done";
          if (m.diff) seg.tool.diff = m.diff;
          if (m.attachments) seg.tool.attachments = m.attachments;
        }
        const tc = cur.tool_calls!.find((t) => t.id === m.tool_call_id);
        if (tc) {
          tc.output = m.content;
          tc.status = m.error ? "error" : "done";
          if (m.diff) tc.diff = m.diff;
          if (m.attachments) tc.attachments = m.attachments;
        }
      }
      continue;
    }
    // Preserve other roles unchanged.
    out.push(m);
    cur = null;
  }
  return out;
}

// Load history and subscribe the first time a chat is opened.
async function select(id: string) {
    sessionStore.setActive(id);
  delete unreadSessions.value[id];
  streaming.value = Boolean(runningSessions.value[id]);
    if (
      !messagesBySession.value[id] ||
      messagesBySession.value[id].length === 0
    ) {
    const history = await api.loadHistory(id);
    messagesBySession.value[id] = normalizeHistory(history);
  }
  await subscribe(id);
  const [
    queued,
    usage,
    agentRuns,
    agentBudget,
    workflow,
    backgroundCommands,
    fileReview,
  ] = await Promise.all([
    api.listQueuedMessages(id),
    api.loadUsage(id),
    api.listAgentRuns(id),
    api.loadAgentBudget(id),
    api.getWorkflow(id),
    api.listBackgroundCommands(id),
    api.loadFileReview(id).catch((error) => {
      console.error("Failed to load files awaiting review:", error);
      return {
        files: [],
        file_state_token: "",
        through_seq: 0,
      } satisfies FileReview;
    }),
  ]);
  queuedBySession.value[id] = queued;
  if (usage) usageBySession.value[id] = usage;
  else delete usageBySession.value[id];
  agentBudgetBySession.value[id] = agentBudget;
  workflowsBySession.value[id] = workflow;
  backgroundCommandsBySession.value[id] = backgroundCommands;
  fileReviewsBySession.value[id] = {
    ...fileReview,
    forceFileKeys: [],
    submitting: false,
    error: "",
  };
  await Promise.all(agentRuns.map((run) => hydrateAgentRun(id, run)));
}

async function forkSession(id: string, throughSeq?: number) {
  const forked = await api.forkSession(
    id,
      throughSeq ? { through_seq: throughSeq } : undefined,
  );
    restoreSessionRuntime(forked.id);
    sessionStore.upsertSession(forked, true);
  const history = await api.loadHistory(forked.id);
  messagesBySession.value[forked.id] = normalizeHistory(history);
  await select(forked.id);
  return forked;
}

// Remove local chat state and select another chat or enter draft state.
function removeSession(id: string) {
    deleteSessionRuntime(id);
    sessionStore.removeSessionRecord(id);
  delete messagesBySession.value[id];
  delete queuedBySession.value[id];
  delete usageBySession.value[id];
  delete runningSessions.value[id];
  delete unreadSessions.value[id];
  delete compactingSessions.value[id];
  delete backgroundCommandsBySession.value[id];
  delete fileReviewsBySession.value[id];
    interactionStore.clearSession(id);
  if (activeId.value === id) {
    const next = sessions.value[0];
    if (next) {
      void select(next.id);
    } else {
        sessionStore.setActive("");
      streaming.value = false;
      ensureBucket("");
    }
  }
}

// Delete the chat in the kernel, then remove local state idempotently.
async function deleteSession(id: string) {
  await api.deleteSession(id);
  removeSession(id);
}

// Stop the active turn while preserving and pausing its queued messages.
async function cancelTurn() {
  const id = activeId.value;
  if (!id) return;
  try {
    await api.cancelTurn(id);
  } catch (e) {
    console.error("Failed to stop turn:", e);
  }
}

async function cancelTool(toolCallId: string) {
  const id = activeId.value;
  if (!id) return;
  try {
    await api.cancelTool(id, toolCallId);
  } catch (e) {
    console.error("Failed to stop command:", e);
  }
}

async function backgroundTool(
    toolCallId: string,
): Promise<BackgroundCommand | undefined> {
  const id = activeId.value;
  if (!id) return undefined;
  const pendingId = `promoting:${toolCallId}`;
  const toolCall = matchingAgentTools(id, toolCallId)[0];
  let command = toolCall?.input ?? "";
  try {
    command = String(JSON.parse(command)?.command ?? command);
  } catch {
    // Keep the raw tool input when it is not valid JSON.
  }
  const items = backgroundCommandsBySession.value[id] ?? [];
  backgroundCommandsBySession.value[id] = [
    {
      command_id: pendingId,
      session_id: id,
      command,
      running: true,
      started_at: new Date().toISOString(),
      backgrounded_by: "user",
    },
    ...items.filter((item) => item.command_id !== pendingId),
  ];
  try {
    let backgroundCommand: BackgroundCommand | undefined;
    let lastError: unknown;
    for (let attempt = 0; attempt < 20; attempt++) {
      try {
        backgroundCommand = await api.backgroundTool(id, toolCallId);
        break;
      } catch (error) {
        lastError = error;
        const message = String(error);
          if (
            !message.includes("409") &&
            !message.includes("tool_not_running")
          ) {
          throw error;
        }
        await new Promise((resolve) => window.setTimeout(resolve, 50));
      }
    }
    if (!backgroundCommand) throw lastError;
    const current = backgroundCommandsBySession.value[id] ?? [];
    backgroundCommandsBySession.value[id] = [
      backgroundCommand,
      ...current.filter(
        (item) =>
          item.command_id !== pendingId &&
            item.command_id !== backgroundCommand.command_id,
      ),
    ];
    return backgroundCommand;
  } catch (e) {
    backgroundCommandsBySession.value[id] = (
      backgroundCommandsBySession.value[id] ?? []
    ).filter((item) => item.command_id !== pendingId);
    console.error("Failed to move command to background:", e);
    return undefined;
  }
}

async function revealToolCommand(
    toolCallId: string,
): Promise<BackgroundCommand | undefined> {
  const id = activeId.value;
  if (!id) return undefined;
  let lastError: unknown;
  for (let attempt = 0; attempt < 20; attempt++) {
    try {
      return await api.revealToolCommand(id, toolCallId);
    } catch (error) {
      lastError = error;
      const message = String(error);
      if (!message.includes("409") && !message.includes("tool_not_running")) {
        break;
      }
      await new Promise((resolve) => window.setTimeout(resolve, 50));
    }
  }
  console.error("Failed to open command terminal:", lastError);
  return undefined;
}

async function stopBackgroundCommand(commandId: string) {
  const id = activeId.value;
  if (!id) return;
  try {
    await api.stopBackgroundCommand(id, commandId);
    backgroundCommandsBySession.value[id] = (
      backgroundCommandsBySession.value[id] ?? []
      ).filter((item) => item.command_id !== commandId);
  } catch (e) {
    console.error("Failed to stop background command:", e);
  }
}

// Persist a draft chat through one shared path for the workspace and composer.
async function ensureSession(): Promise<string> {
  if (activeId.value) return activeId.value;
  const s = await api.createSession({
    connection_id: draft.connectionID || undefined,
    model: draft.model || undefined,
    reasoning_effort: draft.reasoningEffort || undefined,
    project_id: draft.projectID || undefined,
    approval_mode: draft.approvalMode,
  });
    sessionStore.upsertSession(s, true);
  messagesBySession.value[s.id] = messagesBySession.value[""] ?? [];
  delete messagesBySession.value[""];
    sessionStore.setActive(s.id);
  await subscribe(s.id);
  queuedBySession.value[s.id] = [];
  return s.id;
}

// Submit a message. The kernel atomically starts or queues it and projects state
// through SSE so all clients remain consistent.
async function send(
  text: string,
  files: File[] = [],
  browserElements: BrowserElementSelection[] = [],
  skillRef = "",
    restore?: () => void,
) {
    if (!text.trim() && files.length === 0 && browserElements.length === 0)
      return;

  let id = activeId.value;
    if (
      text.trim() === "/compact" &&
      files.length === 0 &&
      browserElements.length === 0
    ) {
    if (!id) return;
    try {
      await api.compactSession(id);
    } catch (e) {
      ensureBucket(id);
      messagesBySession.value[id].push({
        role: "assistant",
          content: translate("Compaction failed: {error}", {
            error: String(e),
          }),
        error: true,
      });
    }
    return;
  }

  const uploaded: AttachmentRef[] = [];
  try {
    if (!id) id = await ensureSession();
    ensureBucket(id);
    for (const file of files) {
      uploaded.push(await api.uploadImage(id, file));
    }
    const result = await api.submitTurn(
      id,
      text,
      uploaded,
      browserElements,
        skillRef,
    );
    if (result.status === "queued" && result.queued) {
      const current = queuedBySession.value[id] ?? [];
      if (!current.some((item) => item.id === result.queued!.id)) {
        queuedBySession.value[id] = [...current, result.queued];
      }
    }
  } catch (e) {
    restore?.();
    if (id) {
        await Promise.allSettled(
          uploaded.map((item) => api.deleteArtifact(id, item.id)),
        );
      ensureBucket(id);
      messagesBySession.value[id].push({
        role: "assistant",
        content: translate("Send failed: {error}", { error: String(e) }),
        error: true,
      });
    } else {
      connectError.value = String(e);
    }
  }
}

async function rewindSentMessage(messageSeq: number) {
  const id = activeId.value;
  if (!id || streaming.value) return;
  try {
    const result = await api.rewindTurn(id, messageSeq);
      interactionStore.setPendingHistoryRewind({
      sessionId: id,
      messageSeq,
      message: result.message,
      files: result.files ?? [],
      headSeq: result.head_seq,
      fileStateToken: result.file_state_token,
      forceFileKeys: [],
      });
  } catch (error) {
      interactionStore.setPendingHistoryRewind({
      sessionId: id,
      messageSeq,
      message: "",
      files: [],
      headSeq: 0,
      fileStateToken: "",
      forceFileKeys: [],
      error: translate("Rewind failed: {error}", { error: String(error) }),
      });
  }
}

async function confirmHistoryRewind() {
  const pending = pendingHistoryRewind.value;
  if (!pending || pending.submitting) return;
  pending.submitting = true;
  pending.error = "";
  try {
    const result = await api.rewindTurn(
      pending.sessionId,
      pending.messageSeq,
      true,
      pending.headSeq,
      pending.fileStateToken,
        pending.forceFileKeys,
    );
      interactionStore.setPendingHistoryRewind(null);
    if (activeId.value !== pending.sessionId) {
      await select(pending.sessionId);
    }
      interactionStore.restoreComposer(
        pending.sessionId,
        result.message || pending.message,
      );
  } catch (error) {
    pending.submitting = false;
      pending.error = translate("Rewind failed: {error}", {
        error: String(error),
      });
  }
}

function cancelHistoryRewind() {
    interactionStore.cancelHistoryRewind();
}

function toggleHistoryRewindForceFile(key: string) {
    interactionStore.toggleHistoryRewindForceFile(key);
}

function consumeComposerRestore(nonce: number) {
    interactionStore.consumeComposerRestore(nonce);
  }

function toggleFileReviewForceFile(key: string) {
  const review = activeFileReview.value;
  if (!review || review.submitting) return;
  const selected = new Set(review.forceFileKeys);
  if (selected.has(key)) selected.delete(key);
  else selected.add(key);
  review.forceFileKeys = [...selected];
}

async function resolveActiveFileReview(action: "keep" | "undo") {
  const sessionId = activeId.value;
  const review = activeFileReview.value;
    if (!sessionId || !review || review.submitting || review.files.length === 0)
      return;

  review.submitting = true;
  review.error = "";
  try {
    await api.resolveFileReview(
      sessionId,
      action,
      review.through_seq,
      review.file_state_token,
        action === "undo" ? review.forceFileKeys : [],
    );
    delete fileReviewsBySession.value[sessionId];
  } catch (error) {
    await refreshFileReview(sessionId);
    const current = fileReviewsBySession.value[sessionId];
    if (current) {
      current.submitting = false;
        current.error = translate("Operation failed: {error}", {
          error: String(error),
        });
    }
  }
}

function keepAllFileChanges() {
  return resolveActiveFileReview("keep");
}

function undoAllFileChanges() {
  return resolveActiveFileReview("undo");
}

async function editQueuedMessage(messageId: string, text: string) {
  const id = activeId.value;
  if (!id || !text.trim()) return;
  try {
    await api.updateQueuedMessage(id, messageId, { message: text });
    queuedBySession.value[id] = await api.listQueuedMessages(id);
  } catch (e) {
    console.error("Failed to edit queued message:", e);
  }
}

async function reorderQueuedMessage(messageId: string, position: number) {
  const id = activeId.value;
  if (!id) return;
  const items = queuedBySession.value[id] ?? [];
  const index = items.findIndex((item) => item.id === messageId);
  if (index < 0 || position < 0 || position >= items.length) return;
  try {
    await api.updateQueuedMessage(id, messageId, { position });
    queuedBySession.value[id] = await api.listQueuedMessages(id);
  } catch (e) {
    console.error("Failed to reorder queued message:", e);
  }
}

async function deleteQueuedMessage(messageId: string) {
  const id = activeId.value;
  if (!id) return;
  try {
    await api.deleteQueuedMessage(id, messageId);
    queuedBySession.value[id] = (queuedBySession.value[id] ?? []).filter(
        (item) => item.id !== messageId,
    );
  } catch (e) {
    console.error("Failed to delete queued message:", e);
  }
}

async function dispatchQueuedMessage(messageId: string) {
  const id = activeId.value;
  if (!id) return;
  try {
    await api.dispatchQueuedMessage(id, messageId);
    queuedBySession.value[id] = await api.listQueuedMessages(id);
  } catch (e) {
    console.error("Failed to send queued message immediately:", e);
  }
}

async function approveWorkflow(id: string) {
  const workflow = workflowsBySession.value[id];
  if (!workflow || workflow.status !== "ready") return;
  const result = await api.approveWorkflow(id, workflow.id);
  workflowsBySession.value[id] = result.workflow;
}

async function closeWorkflow(id: string) {
  const workflow = workflowsBySession.value[id];
    if (
      !workflow ||
      (workflow.status !== "active" && workflow.status !== "ready")
    )
      return;
  try {
    workflowsBySession.value[id] = await api.closeWorkflow(id, workflow.id);
  } catch (e) {
    console.error("Failed to close workflow:", e);
  }
}

  return {
    ready,
    connecting,
    connectError,
    streaming,
    sessions,
    projects,
    runningSessions,
    unreadSessions,
    compactingSessions,
    activeId,
    activeSession,
    isDraft,
    draft,
    connectionModels,
    defaultModels,
    modelsLoading,
    modelsError,
    messages: activeMessages,
    queuedMessages: activeQueuedMessages,
    backgroundCommands: activeBackgroundCommands,
    fileReview: activeFileReview,
    contextUsage: activeUsage,
    agentRunsBySession,
    agentBudgetBySession,
    workflowsBySession,
    pendingApprovals,
    pendingQuestions,
    pendingBrowserActions,
    pendingHistoryRewind,
    composerRestore,
    connect,
    newSession,
    select,
    send,
    rewindSentMessage,
    confirmHistoryRewind,
    cancelHistoryRewind,
    toggleHistoryRewindForceFile,
    consumeComposerRestore,
    keepAllFileChanges,
    undoAllFileChanges,
    toggleFileReviewForceFile,
    refreshActiveFileReview,
    cancelTurn,
    cancelTool,
    backgroundTool,
    revealToolCommand,
    stopBackgroundCommand,
    ensureSession,
    updateSession: sessionStore.updateSession,
    renameSession: sessionStore.renameSession,
    pinSession: sessionStore.pinSession,
    forkSession,
    deleteSession,
    editQueuedMessage,
    reorderQueuedMessage,
    deleteQueuedMessage,
    dispatchQueuedMessage,
    approveWorkflow,
    closeWorkflow,
    resolveApproval: interactionStore.resolveApproval,
    answerQuestions: interactionStore.answerQuestions,
    cancelQuestions: interactionStore.cancelQuestions,
    refreshConnections: sessionStore.refreshConnections,
    refreshProjects: sessionStore.refreshProjects,
    registerProject: sessionStore.registerProject,
  };
});
