// 内核 API 封装:所有对 Rust 命令的调用集中在此,组件与 composable 只依赖这层。
import { invoke, Channel } from "@tauri-apps/api/core";
import { open } from "@tauri-apps/plugin-dialog";

// 会话元信息(与 Go session.Session 对齐的子集)。
export interface Session {
  id: string;
  phase: string;
  model: string;
  workspace?: string;
  approval_mode?: string;
  title?: string;
  title_is_manual?: boolean;
  pinned?: boolean;
  pinned_at?: string;
  created_at: string;
  updated_at: string;
}

// 新建对话时可由用户指定的选项。
export interface CreateSessionOptions {
  model?: string;
  workspace?: string;
  approval_mode?: string;
}

// 局部更新会话的可变字段(undefined 表示不变)。
export interface UpdateSessionPatch {
  model?: string;
  workspace?: string;
  approval_mode?: string;
  title?: string;
  pinned?: boolean;
}

// 内核持有的待发送消息。position 为会话队列中的零基位置。
export interface QueuedMessage {
  id: string;
  session_id: string;
  text: string;
  position: number;
  created_at: string;
  updated_at: string;
}

export interface SubmitTurnResult {
  run_id?: string;
  status: "started" | "queued";
  queued?: QueuedMessage;
}

export interface UpdateQueuedMessagePatch {
  message?: string;
  position?: number;
}

export interface ContextUsage {
  model: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cached_tokens: number;
}

export interface ModelCatalog {
  models: string[];
  context_windows: Record<string, number>;
}

// 审批档位(与 Go approval.Mode 对齐)。
export type ApprovalMode = "explore" | "ask" | "bypass";

// 对话消息(与 Go message.Message 对齐)。
// error 为前端乐观态:发送失败时标记气泡,不进后端。
export interface ToolCallView {
  id: string;
  name: string;
  input: string;
  status: "running" | "done" | "error";
  output?: string;
  // 文件变更 diff(仅 write/edit 工具),统一 diff 文本,前端行内着色展示。
  diff?: string;
}

// assistant 气泡内的有序段落:一个回合可能是「思考→工具→思考→回复」的交错序列,
// 用有序 segments 表达真实顺序,渲染时逐段展示。
export type MessageSegment =
  | { kind: "reasoning"; text: string }
  | { kind: "text"; text: string }
  | { kind: "tool"; tool: ToolCallView };

export interface ChatMessage {
  role: "user" | "assistant" | "system" | "tool";
  content: string;
  reasoning?: string;
  tool_calls?: ToolCallView[];
  // 有序段落(仅 assistant)。存在时优先按其渲染;缺失时回退到 reasoning/tool_calls/content 扁平字段。
  segments?: MessageSegment[];
  tool_call_id?: string;
  // 文件变更 diff(仅 tool 角色历史消息),用于历史回放时回填工具段。
  diff?: string;
  error?: boolean;
}

// provider 配置(与 Go protocol.ProviderConfig 对齐)。
export interface ProviderConfig {
  kind: string;
  base_url: string;
  model: string;
  api_key?: string;
  has_api_key?: boolean;
}

export const api = {
  createSession: (opts?: CreateSessionOptions) =>
    invoke<string>("create_session", { options: opts ?? null }).then(
      (r) => JSON.parse(r) as Session
    ),

  updateSession: (sessionId: string, patch: UpdateSessionPatch) =>
    invoke<string>("update_session", { sessionId, patch }).then(
      (r) => JSON.parse(r) as Session
    ),

  // 删除会话(中断回合、清除历史、广播移除)。
  deleteSession: (sessionId: string) =>
    invoke("delete_session", { sessionId }),

  pickFolder: () =>
    open({ directory: true, multiple: false, title: "选择工作文件夹" }),

  listSessions: () =>
    invoke<string>("list_sessions").then((r) => (JSON.parse(r) as Session[]) ?? []),

  loadHistory: (sessionId: string) =>
    invoke<string>("load_history", { sessionId }).then(
      (r) => (JSON.parse(r) as ChatMessage[]) ?? []
    ),

  loadUsage: (sessionId: string) =>
    invoke<string>("load_usage", { sessionId }).then(
      (r) => JSON.parse(r) as ContextUsage | null
    ),

  submitTurn: (sessionId: string, message: string) =>
    invoke<string>("submit_turn", { sessionId, message }).then(
      (r) => JSON.parse(r) as SubmitTurnResult
    ),

  listQueuedMessages: (sessionId: string) =>
    invoke<string>("list_queued_messages", { sessionId }).then(
      (r) => (JSON.parse(r) as QueuedMessage[]) ?? []
    ),

  enqueueMessage: (sessionId: string, message: string) =>
    invoke<string>("enqueue_message", { sessionId, message }).then(
      (r) => JSON.parse(r) as QueuedMessage
    ),

  updateQueuedMessage: (
    sessionId: string,
    messageId: string,
    patch: UpdateQueuedMessagePatch
  ) =>
    invoke<string>("update_queued_message", { sessionId, messageId, patch }).then(
      (r) => JSON.parse(r) as QueuedMessage
    ),

  deleteQueuedMessage: (sessionId: string, messageId: string) =>
    invoke("delete_queued_message", { sessionId, messageId }),

  dispatchQueuedMessage: (sessionId: string, messageId: string) =>
    invoke<string>("dispatch_queued_message", { sessionId, messageId }).then(
      (r) => JSON.parse(r) as QueuedMessage
    ),

  // 中断当前回合(用户点停止)。
  cancelTurn: (sessionId: string) =>
    invoke("cancel_turn", { sessionId }),

  getProvider: () =>
    invoke<string>("get_provider").then((r) => JSON.parse(r) as ProviderConfig),

  setProvider: (config: ProviderConfig) =>
    invoke<string>("set_provider", { config }).then((r) => JSON.parse(r) as ProviderConfig),

  // 用已配置的 base_url + api_key 拉取 provider 可用模型列表。
  listModels: () =>
    invoke<string>("list_models").then(
      (r) => {
        const result = JSON.parse(r) as Partial<ModelCatalog>;
        return {
          models: result.models ?? [],
          context_windows: result.context_windows ?? {},
        } satisfies ModelCatalog;
      }
    ),

  subscribeEvents: (sessionId: string, onEvent: (data: string) => void) => {
    const channel = new Channel<string>();
    channel.onmessage = onEvent;
    return invoke("subscribe_events", { sessionId, channel });
  },

  // 回执审批决策(批准/拒绝)。
  resolveApproval: (sessionId: string, requestId: string, decision: string) =>
    invoke("resolve_approval", { sessionId, requestId, decision }),
};
