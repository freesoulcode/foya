import { ref, computed, reactive } from "vue";
import {
  api,
  type Session,
  type ChatMessage,
  type UpdateSessionPatch,
  type ApprovalMode,
  type ReasoningEffort,
  type ConnectionModelGroup,
  type QueuedMessage,
  type ContextUsage,
  type BranchEffect,
  type ProjectInfo,
} from "@/lib/api";

// 新建对话草稿态的配置:在真正创建会话前由用户选择模型、项目和审批档位。
export interface DraftConfig {
  connectionID: string;
  model: string;
  reasoningEffort: ReasoningEffort;
  projectID: string;
  approvalMode: ApprovalMode;
}

// 默认审批档位:危险操作前询问(与内核默认值一致)。
const DEFAULT_APPROVAL: ApprovalMode = "ask";

// 内核事件(与 Go event.Event 对齐的子集)。
interface KernelEvent {
  seq: number;
  kind: string;
  session: string;
  payload?: unknown;
}

// 单例状态:整个应用共享一个内核连接与会话集合。
const ready = ref(false);
const connecting = ref(false);
const connectError = ref("");
const sessions = ref<Session[]>([]);
const projects = ref<ProjectInfo[]>([]);
const activeId = ref<string>("");
const streaming = ref(false);
// 哪些会话正在运行 AI 回合(sessionId → true)。供侧边栏给运行中的会话加动画,
// 与当前激活会话无关:切到别的会话后,原会话仍显示运行态。
const runningSessions = ref<Record<string, boolean>>({});
const compactingSessions = ref<Record<string, boolean>>({});

// 新对话草稿态的配置(activeId === "" 时生效)。
const draft = reactive<DraftConfig>({
  connectionID: "",
  model: "",
  reasoningEffort: "",
  projectID: "",
  approvalMode: DEFAULT_APPROVAL,
});

// 连接目录及其模型清单。模型按 Connection 分组，避免同名模型歧义。
const connectionModels = ref<ConnectionModelGroup[]>([]);
const modelsLoading = ref(false);
const modelsError = ref("");

// 每个会话的消息与订阅状态(按会话缓存,切换时不丢)。
const messagesBySession = ref<Record<string, ChatMessage[]>>({});
const subscribed = new Set<string>();
const streamingIdx: Record<string, number> = {};
// 已删除会话:其迟到事件(如 turn_complete)一律丢弃,不重建消息桶。
const deletedSessions = new Set<string>();
// 待发送队列由内核持有;这里仅按会话保存 SSE/GET 投影。
const queuedBySession = ref<Record<string, QueuedMessage[]>>({});
const usageBySession = ref<Record<string, ContextUsage>>({});

// 待处理的审批请求(requestId → 请求详情),UI 据此弹确认框。
export interface PendingApproval {
  id: string;
  session: string;
  tool_name: string;
  action: string;
  detail: string;
}
const pendingApprovals = ref<Record<string, PendingApproval>>({});

export interface PendingHistoryEdit {
  sessionId: string;
  messageSeq: number;
  message: string;
  effects: BranchEffect[];
  headSeq: number;
  submitting?: boolean;
  error?: string;
}
const pendingHistoryEdit = ref<PendingHistoryEdit | null>(null);

const activeMessages = computed<ChatMessage[]>(() => messagesBySession.value[activeId.value] ?? []);
const activeQueuedMessages = computed<QueuedMessage[]>(
  () => queuedBySession.value[activeId.value] ?? []
);
const activeUsage = computed<ContextUsage | undefined>(
  () => usageBySession.value[activeId.value]
);
const activeSession = computed(() => sessions.value.find((s) => s.id === activeId.value));
const isDraft = computed(() => activeId.value === "");

function ensureBucket(id: string) {
  if (!messagesBySession.value[id]) messagesBySession.value[id] = [];
}

// 从后往前找最近一条 assistant 消息(用于挂载 tool_call 视图)。
function findLastAssistantIdx(bucket: ChatMessage[]): number {
  for (let i = bucket.length - 1; i >= 0; i--) {
    if (bucket[i].role === "assistant") return i;
  }
  return -1;
}

// 确保气泡有 segments 数组,并返回它(用于按序追加思考/文本/工具段)。
function ensureSegments(msg: ChatMessage): NonNullable<ChatMessage["segments"]> {
  if (!msg.segments) msg.segments = [];
  return msg.segments;
}

// 追加一段流式增量(思考或正文):若末段同类则并入,否则新开一段。
// 这样「思考→工具→思考→回复」的交错顺序被如实记录为多个段。
function appendDelta(msg: ChatMessage, kind: "reasoning" | "text", delta: string) {
  const segs = ensureSegments(msg);
  const last = segs[segs.length - 1];
  if (last && last.kind === kind) {
    last.text += delta;
  } else {
    segs.push({ kind, text: delta });
  }
}

// 处理某会话的一条 SSE 事件。
function handleEvent(sessionId: string, data: string) {
  let ev: KernelEvent;
  try {
    ev = JSON.parse(data);
  } catch {
    return;
  }
  // 已删除会话的迟到事件直接丢弃,不重建消息桶(删除可能晚于事件到达)。
  if (deletedSessions.has(sessionId)) return;
  ensureBucket(sessionId);
  const bucket = messagesBySession.value[sessionId];

  switch (ev.kind) {
    case "message_delta": {
      const delta = ev.payload as string;
      let idx = streamingIdx[sessionId] ?? -1;
      if (idx < 0) {
        // 兜底:若乐观气泡缺失(如热更新后),补建一条。
        bucket.push({ role: "assistant", content: "" });
        idx = bucket.length - 1;
        streamingIdx[sessionId] = idx;
      }
      // 首个 delta 到达,清除可能的 pending/error 态,开始填内容。
      bucket[idx].error = false;
      bucket[idx].content += delta;
      appendDelta(bucket[idx], "text", delta);
      if (sessionId === activeId.value) streaming.value = true;
      break;
    }
    case "reasoning_delta": {
      // 思考内容增量:按序追加为 reasoning 段。工具执行后模型再次思考时,
      // 因末段已是 tool,会自动新开一段 reasoning,从而保留多次思考。
      const delta = ev.payload as string;
      let idx = streamingIdx[sessionId] ?? -1;
      if (idx < 0) {
        bucket.push({ role: "assistant", content: "" });
        idx = bucket.length - 1;
        streamingIdx[sessionId] = idx;
      }
      bucket[idx].error = false;
      appendDelta(bucket[idx], "reasoning", delta);
      if (sessionId === activeId.value) streaming.value = true;
      break;
    }
    case "message_end": {
      const m = { ...(ev.payload as ChatMessage), event_seq: ev.seq };
      // 用户消息由内核事件统一落到界面,而非只在发起请求的客户端乐观插入;
      // 因此同一会话的其它在线客户端也能看到新回合。
      if (m.role === "user") {
        bucket.push(m);
        break;
      }
      const idx = streamingIdx[sessionId] ?? -1;
      if (m.role === "assistant" && idx >= 0) {
        // 内容以流式累积的 delta 为准;仅在无 delta 时用服务端 payload 兜底。
        if (!bucket[idx].content && m.content) {
          bucket[idx].content = m.content;
          appendDelta(bucket[idx], "text", m.content);
        }
        // 思考内容兜底:流式未收到 reasoning 段但服务端 payload 有,则补一段。
        if (m.reasoning && !bucket[idx].segments?.some((s) => s.kind === "reasoning")) {
          ensureSegments(bucket[idx]).unshift({ kind: "reasoning", text: m.reasoning });
        }
        bucket[idx].error = false;
        // 仅当本条消息不携带 tool_calls(即最终回复)时才释放流式槽位。
        // 携带 tool_calls 时回合尚未结束:工具执行后模型会继续输出,
        // 后续 delta 应追加到同一条气泡,而非新建气泡拆成两条消息。
        // streaming 也不在此复位,由 turn_complete / error 统一负责。
        if (!m.tool_calls) {
          streamingIdx[sessionId] = -1;
        }
      }
      // tool 结果消息通过 tool_end 事件展示,不重复插入。
      break;
    }
    case "history_branched": {
      const p = ev.payload as { target_user_seq?: number };
      const target = Number(p?.target_user_seq ?? 0);
      const index = bucket.findIndex(
        (message) => message.role === "user" && message.event_seq === target
      );
      if (index >= 0) bucket.splice(index);
      streamingIdx[sessionId] = -1;
      delete usageBySession.value[sessionId];
      break;
    }
    case "tool_begin": {
      const p = ev.payload as { id: string; name: string; input: string };
      // 找到当前流式助手消息,挂上 tool_call 视图。
      const asstIdx = findLastAssistantIdx(bucket);
      if (asstIdx >= 0) {
        const msg = bucket[asstIdx];
        if (!msg.tool_calls) msg.tool_calls = [];
        // 避免重复(消息回放时可能已存在)。
        if (!msg.tool_calls.find((tc) => tc.id === p.id)) {
          const tool = {
            id: p.id,
            name: p.name,
            input: p.input,
            status: "running" as const,
          };
          msg.tool_calls.push(tool);
          // 按序追加为 tool 段:插在当前思考/文本之后,后续思考会另起新段。
          ensureSegments(msg).push({ kind: "tool", tool });
        }
      }
      break;
    }
    case "tool_update": {
      // 执行前回填完整参数(tool_begin 在参数刚开始流式生成时已发出,
      // 那时只有名称;此处补上完整 input)。若卡片因回放等原因不存在则兜底创建。
      const p = ev.payload as { id: string; name: string; input: string };
      const asstIdx = findLastAssistantIdx(bucket);
      if (asstIdx < 0) break;
      const msg = bucket[asstIdx];
      let tc = msg.tool_calls?.find((t) => t.id === p.id);
      if (!tc) {
        if (!msg.tool_calls) msg.tool_calls = [];
        tc = { id: p.id, name: p.name, input: p.input, status: "running" };
        msg.tool_calls.push(tc);
        ensureSegments(msg).push({ kind: "tool", tool: tc });
      } else {
        if (p.name) tc.name = p.name;
        if (p.input) tc.input = p.input;
      }
      const seg = msg.segments?.find(
        (s) => s.kind === "tool" && s.tool.id === p.id
      );
      if (seg && seg.kind === "tool") {
        if (p.name) seg.tool.name = p.name;
        if (p.input) seg.tool.input = p.input;
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
      };
      const asstIdx = findLastAssistantIdx(bucket);
      if (asstIdx >= 0) {
        const tc = bucket[asstIdx].tool_calls?.find((t) => t.id === p.id);
        if (tc) {
          tc.status = p.is_error ? "error" : "done";
          tc.output = p.output;
          if (p.diff) tc.diff = p.diff;
        }
        // 同步更新 segments 中对应的 tool 段(与 tool_calls 是不同对象引用)。
        const seg = bucket[asstIdx].segments?.find(
          (s) => s.kind === "tool" && s.tool.id === p.id
        );
        if (seg && seg.kind === "tool") {
          seg.tool.status = p.is_error ? "error" : "done";
          seg.tool.output = p.output;
          if (p.diff) seg.tool.diff = p.diff;
        }
      }
      break;
    }
    case "approval_request": {
      const p = ev.payload as PendingApproval;
      pendingApprovals.value[p.id] = p;
      break;
    }
    case "approval_resolved": {
      const p = ev.payload as { id: string };
      delete pendingApprovals.value[p.id];
      break;
    }
    case "turn_started": {
      runningSessions.value[sessionId] = true;
      let idx = streamingIdx[sessionId] ?? -1;
      if (idx < 0) {
        bucket.push({ role: "assistant", content: "" });
        idx = bucket.length - 1;
        streamingIdx[sessionId] = idx;
      }
      if (sessionId === activeId.value) streaming.value = true;
      break;
    }
    case "turn_complete":
      streamingIdx[sessionId] = -1;
      delete runningSessions.value[sessionId];
      if (sessionId === activeId.value) streaming.value = false;
      break;
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
      // 会话元数据变更(标题/模型等),按 id 替换本地会话项,侧边栏自动响应。
      const updated = ev.payload as Session;
      if (updated && updated.id) {
        const idx = sessions.value.findIndex((s) => s.id === updated.id);
        if (idx >= 0) sessions.value[idx] = { ...sessions.value[idx], ...updated };
      }
      break;
    }
    case "session_deleted": {
      // 会话被删除(可能来自其他设备):从列表移除,清理本地缓存;
      // 若正在查看该会话,切换到另一个会话或进入草稿态。
      const p = ev.payload as { id?: string };
      const id = p?.id ?? sessionId;
      removeSession(id);
      break;
    }
    case "error": {
      // 优先填入当前回合的空 assistant 气泡,避免多出一条错误消息。
      delete runningSessions.value[sessionId];
      const text = `⚠️ ${String(ev.payload)}`;
      const idx = streamingIdx[sessionId] ?? -1;
      if (idx >= 0) {
        bucket[idx].content = text;
        bucket[idx].error = true;
        streamingIdx[sessionId] = -1;
      } else {
        bucket.push({ role: "assistant", content: text, error: true });
      }
      if (sessionId === activeId.value) streaming.value = false;
      break;
    }
  }
}

// 订阅某会话事件流(幂等)。
async function subscribe(sessionId: string) {
  if (subscribed.has(sessionId)) return;
  subscribed.add(sessionId);
  streamingIdx[sessionId] = -1;
  try {
    await api.subscribeEvents(sessionId, (data) => handleEvent(sessionId, data));
  } catch (error) {
    subscribed.delete(sessionId);
    throw error;
  }
}

// 拉取所有连接及其模型目录。单个连接的目录失败不阻塞其它连接。
async function refreshConnections() {
  modelsLoading.value = true;
  modelsError.value = "";
  try {
    const connections = await api.listConnections();
    connectionModels.value = await Promise.all(
      connections.map(async (connection) => {
        try {
          const catalog = await api.listConnectionModels(connection.id ?? "");
          return { ...connection, ...catalog };
        } catch (cause) {
          return {
            ...connection,
            models: connection.default_model ? [connection.default_model] : [],
            context_windows: {},
            models_error: String(cause),
          };
        }
      })
    );
    if (isDraft.value && !draft.connectionID) {
      const first = connectionModels.value.find((connection) => connection.models.length > 0);
      if (first) {
        draft.connectionID = first.id ?? "";
        draft.model = first.default_model || first.models[0] || "";
      }
    }
  } catch (e) {
    connectionModels.value = [];
    modelsError.value = String(e);
  } finally {
    modelsLoading.value = false;
  }
}

async function registerProject(path: string, name = ""): Promise<ProjectInfo> {
  const project = await api.registerProject(path, name);
  const index = projects.value.findIndex((item) => item.id === project.id);
  if (index >= 0) projects.value[index] = project;
  else projects.value.unshift(project);
  return project;
}

async function refreshProjects() {
  projects.value = await api.listProjects();
  if (
    isDraft.value &&
    draft.projectID &&
    !projects.value.some((project) => project.id === draft.projectID)
  ) {
    draft.projectID = "";
  }
}

// 连接内核:拉会话列表,选中或新建一个会话。
async function connect() {
  if (ready.value || connecting.value) return;
  connecting.value = true;
  connectError.value = "";
  try {
    sessions.value = await api.listSessions();
    projects.value = await api.listProjects();
    sessions.value.sort((a, b) => b.updated_at.localeCompare(a.updated_at));
    if (sessions.value.length > 0) {
      await select(sessions.value[0].id);
    } else {
      activeId.value = "";
      ensureBucket("");
    }
    ready.value = true;
    // 连接就绪后拉取连接目录与模型列表。
    void refreshConnections();
  } catch (e) {
    // 不再静默吞错:把失败原因暴露到界面,便于定位(如内核未就绪、命令缺失)。
    connectError.value = String(e);
    console.error("连接内核失败:", e);
  } finally {
    connecting.value = false;
  }
}

// 进入新对话草稿态:不立即创建会话,直到用户发送第一条消息。
// 从项目分组发起时继承 Project ID;全局新建仍使用无项目草稿。
function newSession(projectID = "") {
  activeId.value = "";
  ensureBucket("");
  streaming.value = false;
  const first = connectionModels.value.find((connection) => connection.models.length > 0);
  draft.connectionID = first?.id ?? "";
  draft.model = first?.default_model || first?.models[0] || "";
  draft.reasoningEffort = "";
  draft.projectID = projectID;
  draft.approvalMode = DEFAULT_APPROVAL;
}

// 把后端扁平历史折叠成带有序 segments 的气泡序列。
// 后端一个回合按步骤产生多条消息:assistant#1(思考+工具调用)→ tool(结果)
// → assistant#2(思考+回复)……前端把「同一回合内连续的 assistant/tool 消息」
// 合并为一个气泡,按真实顺序还原「思考→工具→思考→回复」的交错。
// 回合边界:user 消息。
function normalizeHistory(history: ChatMessage[]): ChatMessage[] {
  const out: ChatMessage[] = [];
  let cur: ChatMessage | null = null; // 当前正在聚合的 assistant 气泡

  for (const m of history) {
    if (m.role === "user") {
      out.push(m);
      cur = null;
      continue;
    }
    if (m.role === "assistant") {
      if (!cur) {
        cur = { role: "assistant", content: "", segments: [], tool_calls: [] };
        out.push(cur);
      }
      const segs = cur.segments!;
      if (m.reasoning) segs.push({ kind: "reasoning", text: m.reasoning });
      if (m.content) {
        segs.push({ kind: "text", text: m.content });
        cur.content += m.content; // 供复制/滚动等仍读 content 的地方使用
      }
      if (m.tool_calls) {
        for (const tc of m.tool_calls) {
          const tool = { ...tc, status: "running" as const };
          cur.tool_calls!.push(tool);
          segs.push({ kind: "tool", tool });
        }
      }
      continue;
    }
    if (m.role === "tool") {
      // 工具结果:回填到当前气泡对应 tool 段(按 tool_call_id 关联)。
      if (cur) {
        const seg = cur.segments!.find(
          (s) => s.kind === "tool" && s.tool.id === m.tool_call_id
        );
        if (seg && seg.kind === "tool") {
          seg.tool.output = m.content;
          seg.tool.status = m.error ? "error" : "done";
          if (m.diff) seg.tool.diff = m.diff;
        }
        const tc = cur.tool_calls!.find((t) => t.id === m.tool_call_id);
        if (tc) {
          tc.output = m.content;
          tc.status = m.error ? "error" : "done";
          if (m.diff) tc.diff = m.diff;
        }
      }
      continue;
    }
    // system 等其它角色:原样保留(通常不入历史)。
    out.push(m);
    cur = null;
  }
  return out;
}

// 切换到某会话:首次进入时加载历史并订阅。
async function select(id: string) {
  activeId.value = id;
  streaming.value = Boolean(runningSessions.value[id]);
  if (!messagesBySession.value[id] || messagesBySession.value[id].length === 0) {
    const history = await api.loadHistory(id);
    messagesBySession.value[id] = normalizeHistory(history);
  }
  await subscribe(id);
  const [queued, usage] = await Promise.all([
    api.listQueuedMessages(id),
    api.loadUsage(id),
  ]);
  queuedBySession.value[id] = queued;
  if (usage) usageBySession.value[id] = usage;
  else delete usageBySession.value[id];
}

// 局部更新当前会话的可变配置(模型/项目/审批档位)。
// 调用内核 PATCH 接口,并同步更新本地会话对象;审批档位切换对后续工具调用立即生效。
async function updateSession(id: string, patch: UpdateSessionPatch) {
  const updated = await api.updateSession(id, patch);
  const idx = sessions.value.findIndex((s) => s.id === id);
  if (idx >= 0) sessions.value[idx] = updated;
  return updated;
}

// 手动改名:置 title_is_manual,此后内核自动标题不再覆盖。
// 内核会广播 session_updated,本地会话项随之更新。
async function renameSession(id: string, title: string) {
  return updateSession(id, { title });
}

// 置顶/取消置顶:走 PATCH,返回的会话项直接替换本地项。
async function pinSession(id: string, pinned: boolean) {
  return updateSession(id, { pinned });
}

// 从本地集合移除会话并清理缓存;若移除的是当前会话,切到另一个或进入草稿态。
function removeSession(id: string) {
  deletedSessions.add(id);
  const idx = sessions.value.findIndex((s) => s.id === id);
  if (idx >= 0) sessions.value.splice(idx, 1);
  delete messagesBySession.value[id];
  delete queuedBySession.value[id];
  delete usageBySession.value[id];
  subscribed.delete(id);
  delete streamingIdx[id];
  delete runningSessions.value[id];
  delete compactingSessions.value[id];
  // 清理该会话的待处理审批。
  for (const [aid, a] of Object.entries(pendingApprovals.value)) {
    if (a.session === id) delete pendingApprovals.value[aid];
  }
  if (activeId.value === id) {
    const next = sessions.value[0];
    if (next) {
      void select(next.id);
    } else {
      activeId.value = "";
      streaming.value = false;
      ensureBucket("");
    }
  }
}

// 删除会话:调用内核 DELETE(中断回合、清历史、广播),成功后本地移除。
// 非活跃会话不订阅其 SSE,故删除后需这里主动移除;活跃会话的广播也会到达,去重即可。
async function deleteSession(id: string) {
  await api.deleteSession(id);
  removeSession(id);
}

// 回执审批决策(批准/拒绝)。
async function resolveApproval(sessionId: string, requestId: string, decision: string) {
  await api.resolveApproval(sessionId, requestId, decision);
  delete pendingApprovals.value[requestId];
}

// 中断当前会话正在运行的回合(用户点停止)。后端会传播 ctx 取消,
// 中断 provider HTTP 请求、工具执行与审批等待;待发送队列保留并暂停。
async function cancelTurn() {
  const id = activeId.value;
  if (!id) return;
  try {
    await api.cancelTurn(id);
  } catch (e) {
    console.error("中断回合失败:", e);
  }
}

// 确保草稿态拥有一个真实会话。工作台能力和发送消息共用这条创建路径，
// 避免同一份草稿配置在不同入口重复组装。
async function ensureSession(): Promise<string> {
  if (activeId.value) return activeId.value;
  const s = await api.createSession({
    connection_id: draft.connectionID || undefined,
    model: draft.model || undefined,
    reasoning_effort: draft.reasoningEffort || undefined,
    project_id: draft.projectID || undefined,
    approval_mode: draft.approvalMode,
  });
  sessions.value.unshift(s);
  messagesBySession.value[s.id] = messagesBySession.value[""] ?? [];
  delete messagesBySession.value[""];
  activeId.value = s.id;
  await subscribe(s.id);
  queuedBySession.value[s.id] = [];
  return s.id;
}

// 发送一条消息。内核原子决定直接启动或进入队列;用户消息与运行态
// 统一由 SSE 事件投影,从而让多个客户端保持一致。
// 若当前为草稿态(尚未创建会话),先用草稿配置创建会话再发送。
async function send(text: string) {
  if (!text.trim()) return;

  let id = activeId.value;
  if (text.trim() === "/compact") {
    if (!id) return;
    try {
      await api.compactSession(id);
    } catch (e) {
      ensureBucket(id);
      messagesBySession.value[id].push({
        role: "assistant",
        content: `⚠️ 压缩失败：${String(e)}`,
        error: true,
      });
    }
    return;
  }

  if (!id) id = await ensureSession();

  ensureBucket(id);

  try {
    const result = await api.submitTurn(id, text);
    if (result.status === "queued" && result.queued) {
      const current = queuedBySession.value[id] ?? [];
      if (!current.some((item) => item.id === result.queued!.id)) {
        queuedBySession.value[id] = [...current, result.queued];
      }
    }
  } catch (e) {
    messagesBySession.value[id].push({
      role: "assistant",
      content: `⚠️ 发送失败：${String(e)}`,
      error: true,
    });
  }
}

async function editSentMessage(messageSeq: number, text: string) {
  const id = activeId.value;
  const message = text.trim();
  if (!id || !message || streaming.value) return;
  try {
    const result = await api.editTurn(id, messageSeq, message);
    if (result.status === "confirmation_required") {
      pendingHistoryEdit.value = {
        sessionId: id,
        messageSeq,
        message,
        effects: result.effects ?? [],
        headSeq: result.head_seq ?? 0,
      };
    }
  } catch (error) {
    pendingHistoryEdit.value = {
      sessionId: id,
      messageSeq,
      message,
      effects: [],
      headSeq: 0,
      error: `编辑失败：${String(error)}`,
    };
  }
}

async function confirmHistoryEdit() {
  const pending = pendingHistoryEdit.value;
  if (!pending || pending.submitting) return;
  pending.submitting = true;
  pending.error = "";
  try {
    await api.editTurn(
      pending.sessionId,
      pending.messageSeq,
      pending.message,
      true,
      pending.headSeq
    );
    pendingHistoryEdit.value = null;
  } catch (error) {
    pending.submitting = false;
    pending.error = `编辑失败：${String(error)}`;
  }
}

function cancelHistoryEdit() {
  pendingHistoryEdit.value = null;
}

async function editQueuedMessage(messageId: string, text: string) {
  const id = activeId.value;
  if (!id || !text.trim()) return;
  try {
    await api.updateQueuedMessage(id, messageId, { message: text });
    queuedBySession.value[id] = await api.listQueuedMessages(id);
  } catch (e) {
    console.error("编辑待发送消息失败:", e);
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
    console.error("调整待发送顺序失败:", e);
  }
}

async function deleteQueuedMessage(messageId: string) {
  const id = activeId.value;
  if (!id) return;
  try {
    await api.deleteQueuedMessage(id, messageId);
    queuedBySession.value[id] = (queuedBySession.value[id] ?? []).filter(
      (item) => item.id !== messageId
    );
  } catch (e) {
    console.error("删除待发送消息失败:", e);
  }
}

async function dispatchQueuedMessage(messageId: string) {
  const id = activeId.value;
  if (!id) return;
  try {
    await api.dispatchQueuedMessage(id, messageId);
    queuedBySession.value[id] = await api.listQueuedMessages(id);
  } catch (e) {
    console.error("立即发送失败:", e);
  }
}

export function useKernel() {
  return {
    ready,
    connecting,
    connectError,
    streaming,
    sessions,
    projects,
    runningSessions,
    compactingSessions,
    activeId,
    activeSession,
    isDraft,
    draft,
    connectionModels,
    modelsLoading,
    modelsError,
    messages: activeMessages,
    queuedMessages: activeQueuedMessages,
    contextUsage: activeUsage,
    pendingApprovals,
    pendingHistoryEdit,
    connect,
    newSession,
    select,
    send,
    editSentMessage,
    confirmHistoryEdit,
    cancelHistoryEdit,
    cancelTurn,
    ensureSession,
    updateSession,
    renameSession,
    pinSession,
    deleteSession,
    editQueuedMessage,
    reorderQueuedMessage,
    deleteQueuedMessage,
    dispatchQueuedMessage,
    resolveApproval,
    refreshConnections,
    refreshProjects,
    registerProject,
  };
}
