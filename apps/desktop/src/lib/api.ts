// 内核 API 封装:所有对 Rust 命令的调用集中在此,组件与 composable 只依赖这层。
import { invoke, Channel } from "@tauri-apps/api/core";

// 会话元信息(与 Go session.Session 对齐的子集)。
export interface Session {
  id: string;
  phase: string;
  model: string;
  created_at: string;
  updated_at: string;
}

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
  createSession: () => invoke<string>("create_session").then((r) => JSON.parse(r) as Session),

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

  subscribeEvents: (sessionId: string, onEvent: (data: string) => void) => {
    const channel = new Channel<string>();
    channel.onmessage = onEvent;
    return invoke("subscribe_events", { sessionId, channel });
  },
};
