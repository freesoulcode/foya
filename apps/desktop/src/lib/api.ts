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
}

// 审批档位(与 Go approval.Mode 对齐)。
export type ApprovalMode = "explore" | "ask" | "bypass";

// 对话消息(与 Go message.Message 对齐)。
export interface ChatMessage {
  role: "user" | "assistant" | "system";
  content: string;
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

  pickFolder: () =>
    open({ directory: true, multiple: false, title: "选择工作文件夹" }),

  listSessions: () =>
    invoke<string>("list_sessions").then((r) => (JSON.parse(r) as Session[]) ?? []),

  loadHistory: (sessionId: string) =>
    invoke<string>("load_history", { sessionId }).then(
      (r) => (JSON.parse(r) as ChatMessage[]) ?? []
    ),

  submitTurn: (sessionId: string, message: string) =>
    invoke("submit_turn", { sessionId, message }),

  getProvider: () =>
    invoke<string>("get_provider").then((r) => JSON.parse(r) as ProviderConfig),

  setProvider: (config: ProviderConfig) =>
    invoke<string>("set_provider", { config }).then((r) => JSON.parse(r) as ProviderConfig),

  // 用已配置的 base_url + api_key 拉取 provider 可用模型列表。
  listModels: () =>
    invoke<string>("list_models").then(
      (r) => (JSON.parse(r) as { models: string[] }).models ?? []
    ),

  subscribeEvents: (sessionId: string, onEvent: (data: string) => void) => {
    const channel = new Channel<string>();
    channel.onmessage = onEvent;
    return invoke("subscribe_events", { sessionId, channel });
  },
};
