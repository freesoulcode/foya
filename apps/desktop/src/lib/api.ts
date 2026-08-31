// 内核 API 封装:所有对 Rust 命令的调用集中在此,组件与 composable 只依赖这层。
import { invoke, Channel } from "@tauri-apps/api/core";
import { open } from "@tauri-apps/plugin-dialog";

// 会话元信息(与 Go session.Session 对齐的子集)。
export interface Session {
  id: string;
  parent_id?: string;
  spawned_by?: {
    parent_run_id?: string;
    parent_turn_id?: string;
    parent_tool_call_id: string;
  };
  agent_ref?: string;
  agent_name?: string;
  agent_digest?: string;
  phase: string;
  connection_id: string;
  model: string;
  reasoning_effort?: ReasoningEffort;
  project_id?: string;
  approval_mode?: ApprovalMode;
  title?: string;
  title_is_manual?: boolean;
  pinned?: boolean;
  pinned_at?: string;
  created_at: string;
  updated_at: string;
}

// 推理强度与内核 session.ReasoningEffort 对齐。未设置时跟随模型默认值。
export type ReasoningEffort = "" | "low" | "medium" | "high";

// 新建对话时可由用户指定的选项。
export interface CreateSessionOptions {
  connection_id?: string;
  model?: string;
  reasoning_effort?: ReasoningEffort;
  project_id?: string;
  approval_mode?: ApprovalMode;
}

// 局部更新会话配置(undefined 表示不变)。project_id 绑定后不可更换。
export interface UpdateSessionPatch {
  connection_id?: string;
  model?: string;
  reasoning_effort?: ReasoningEffort;
  project_id?: string;
  approval_mode?: ApprovalMode;
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

export interface BranchEffect {
  tool: string;
  detail?: string;
}

export interface EditTurnResult {
  status: "started" | "confirmation_required";
  effects?: BranchEffect[];
  head_seq?: number;
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

export interface CompactSessionResult {
  through_seq: number;
  estimated_tokens_before: number;
  estimated_tokens_after: number;
}

export interface ConnectionModelCatalog {
  models: string[];
  context_windows: Record<string, number>;
}

export interface ConnectionModelGroup extends ConnectionConfig {
  models: string[];
  context_windows: Record<string, number>;
  models_error?: string;
}

export interface TerminalResource {
  ref: string;
  session_id: string;
  running: boolean;
  exit_code?: number;
  buffer?: string;
  seq: number;
}

export interface TerminalDataEvent {
  session_id: string;
  ref: string;
  seq: number;
  data?: string;
  exited?: boolean;
  exit_code?: number;
}

export interface BrowserViewport {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface ProjectEntry {
  path: string;
  name: string;
  is_dir: boolean;
}

// 审批档位(与 Go approval.Mode 对齐)。
export type ApprovalMode = "manual" | "auto" | "full_access";
export type ApprovalDecision =
  | "approved"
  | "approved_for_session"
  | "denied";

// 对话消息(与 Go message.Message 对齐)。
// error 为前端乐观态:发送失败时标记气泡,不进后端。
export interface ToolCallView {
  id: string;
  name: string;
  input: string;
  status: "running" | "done" | "error";
  output?: string;
  child_session_id?: string;
  agent_ref?: string;
  agent_name?: string;
  agent_run?: AgentRunSnapshot;
  child_messages?: ChatMessage[];
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
  event_seq?: number;
  reasoning?: string;
  tool_calls?: ToolCallView[];
  // 有序段落(仅 assistant)。存在时优先按其渲染;缺失时回退到 reasoning/tool_calls/content 扁平字段。
  segments?: MessageSegment[];
  tool_call_id?: string;
  // 文件变更 diff(仅 tool 角色历史消息),用于历史回放时回填工具段。
  diff?: string;
  error?: boolean;
}

// Connection 是一个独立模型账号或端点。API Key 仅在写入时携带。
export interface ConnectionConfig {
  id?: string;
  name: string;
  kind: string;
  auth_kind: "api_key";
  base_url: string;
  default_model: string;
  api_key?: string;
  has_api_key?: boolean;
  sort_order: number;
}

export interface SkillInfo {
  ref: string;
  name: string;
  description?: string;
  scope: "builtin" | "user" | "project";
  path?: string;
  enabled: boolean;
  allowed_tools?: string[];
}

export interface AgentInfo {
  ref: string;
  name: string;
  description: string;
  scope: "builtin" | "user" | "project";
  path?: string;
  model?: string;
  tools?: string[];
  max_turns?: number;
  timeout?: string;
  digest: string;
}

export type AgentRunStatus =
  | "queued"
  | "running"
  | "completed"
  | "failed"
  | "cancelled"
  | "interrupted";

export interface AgentRunSnapshot {
  id: string;
  status: AgentRunStatus;
  root_session_id: string;
  root_run_id: string;
  parent_session_id: string;
  parent_tool_call_id?: string;
  child_session_id?: string;
  agent_ref?: string;
  agent_name?: string;
  task: string;
  depth: number;
  tokens_used?: number;
  output?: string;
  error?: string;
  created_at: string;
  started_at?: string;
  completed_at?: string;
}

export interface AgentBudget {
  root_session_id: string;
  root_run_id: string;
  tokens_used: number;
  max_tokens?: number;
  exceeded: boolean;
}

export interface AgentLimits {
  max_global_concurrency: number;
  max_per_root: number;
  max_tree_tokens: number;
}

export interface StartAgentRequest {
  task: string;
  root_run_id?: string;
  agent_ref?: string;
  context?: {
    mode?: "none" | "selected" | "summary" | "last_n_turns";
    message_seqs?: number[];
    last_turns?: number;
  };
}

export interface ProjectInfo {
  id: string;
  name: string;
  path: string;
  available: boolean;
  pinned?: boolean;
  pinned_at?: string;
  created_at: string;
  updated_at: string;
}

export interface SearchProviderConfig {
  id: string;
  kind: "google_cse" | "bing" | "baidu";
  name: string;
  enabled: boolean;
  api_key?: string;
  has_api_key?: boolean;
  search_engine_id?: string;
  endpoint?: string;
}

export interface WebSearchSettings {
  enabled: boolean;
  default_provider?: string;
  providers: SearchProviderConfig[];
}

export interface WebSearchResult {
  title: string;
  url: string;
  snippet?: string;
  source?: string;
  rank: number;
}

export interface McpServerConfig {
  id: string;
  name: string;
  enabled: boolean;
  transport: "stdio" | "streamable_http" | "sse";
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  cwd?: string;
  url?: string;
  headers?: Record<string, string>;
  bearer_token?: string;
  has_token?: boolean;
}

export interface McpConfig {
  version: number;
  servers: McpServerConfig[];
}

export interface McpStatus {
  id: string;
  name: string;
  state: "disabled" | "disconnected" | "connecting" | "connected" | "error";
  transport: string;
  tool_count: number;
  resource_count: number;
  prompt_count: number;
  error?: string;
}

export interface McpRegistryServer {
  id: string;
  name: string;
  description?: string;
  version?: string;
  installable: boolean;
  reason?: string;
  config: McpServerConfig;
}

function parseWebSearchSettings(raw: string): WebSearchSettings {
  const parsed = JSON.parse(raw) as Partial<WebSearchSettings> | null;
  return {
    enabled: parsed?.enabled ?? true,
    default_provider: parsed?.default_provider,
    providers: Array.isArray(parsed?.providers) ? parsed.providers : [],
  };
}

function parseMcpConfig(raw: string): McpConfig {
  const parsed = JSON.parse(raw) as Partial<McpConfig> | null;
  return {
    version: parsed?.version ?? 1,
    servers: Array.isArray(parsed?.servers) ? parsed.servers : [],
  };
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

  listProjectFiles: (projectPath: string) =>
    invoke<string>("list_project_files", { projectPath }).then(
      (result) => (JSON.parse(result) as ProjectEntry[]) ?? []
    ),

  readProjectFile: (projectPath: string, path: string) =>
    invoke<string>("read_project_file", { projectPath, path }),

  createProjectFile: (projectPath: string, path: string) =>
    invoke<string>("create_project_file", { projectPath, path }),

  createProjectDirectory: (projectPath: string, path: string) =>
    invoke<string>("create_project_directory", { projectPath, path }),

  renameProjectEntry: (projectPath: string, path: string, newName: string) =>
    invoke<string>("rename_project_entry", { projectPath, path, newName }),

  deleteProjectEntry: (projectPath: string, path: string) =>
    invoke("delete_project_entry", { projectPath, path }),

  resolveProjectPath: (projectPath: string, path = "") =>
    invoke<string>("resolve_project_path", { projectPath, path }),

  listSessions: () =>
    invoke<string>("list_sessions").then((r) => (JSON.parse(r) as Session[]) ?? []),

  listChildSessions: (sessionId: string) =>
    invoke<string>("list_child_sessions", { sessionId }).then(
      (r) => (JSON.parse(r) as Session[]) ?? []
    ),

  listAgentRuns: (sessionId: string) =>
    invoke<string>("list_agent_runs", { sessionId }).then(
      (r) => (JSON.parse(r) as AgentRunSnapshot[]) ?? []
    ),

  startAgent: (sessionId: string, request: StartAgentRequest) =>
    invoke<string>("start_agent", { sessionId, request }).then(
      (r) => JSON.parse(r) as AgentRunSnapshot
    ),

  cancelAgent: (sessionId: string, runId: string) =>
    invoke("cancel_agent", { sessionId, runId }),

  loadAgentBudget: (sessionId: string) =>
    invoke<string>("load_agent_budget", { sessionId }).then(
      (r) => JSON.parse(r) as AgentBudget
    ),

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

  editTurn: (
    sessionId: string,
    messageSeq: number,
    message: string,
    confirmEffects = false,
    expectedHeadSeq = 0
  ) =>
    invoke<string>("edit_turn", {
      sessionId,
      messageSeq,
      message,
      confirmEffects,
      expectedHeadSeq,
    }).then((r) => JSON.parse(r) as EditTurnResult),

  compactSession: (sessionId: string) =>
    invoke<string>("compact_session", { sessionId }).then(
      (r) => JSON.parse(r) as CompactSessionResult
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

  listConnections: () =>
    invoke<string>("list_connections").then(
      (r) => (JSON.parse(r) as ConnectionConfig[]) ?? []
    ),

  createConnection: (config: ConnectionConfig) =>
    invoke<string>("create_connection", { config }).then(
      (r) => JSON.parse(r) as ConnectionConfig
    ),

  updateConnection: (connectionId: string, config: ConnectionConfig) =>
    invoke<string>("update_connection", { connectionId, config }).then(
      (r) => JSON.parse(r) as ConnectionConfig
    ),

  deleteConnection: (connectionId: string) =>
    invoke("delete_connection", { connectionId }),

  listConnectionModels: (connectionId: string) =>
    invoke<string>("list_connection_models", { connectionId }).then(
      (r) => {
        const result = JSON.parse(r) as Partial<ConnectionModelCatalog>;
        return {
          models: result.models ?? [],
          context_windows: result.context_windows ?? {},
        } satisfies ConnectionModelCatalog;
      }
    ),

  listSkills: () =>
    invoke<string>("list_skills").then(
      (r) => (JSON.parse(r) as SkillInfo[]) ?? []
    ),

  listAgents: () =>
    invoke<string>("list_agents").then(
      (r) => (JSON.parse(r) as AgentInfo[]) ?? []
    ),

  listProjects: () =>
    invoke<string>("list_projects").then(
      (r) => (JSON.parse(r) as ProjectInfo[]) ?? []
    ),

  registerProject: (path: string, name = "") =>
    invoke<string>("register_project", { path, name: name.trim() }).then(
      (r) => JSON.parse(r) as ProjectInfo
    ),

  updateProject: (
    projectId: string,
    patch: { name?: string; pinned?: boolean }
  ) =>
    invoke<string>("update_project", { projectId, patch }).then(
      (r) => JSON.parse(r) as ProjectInfo
    ),

  deleteProject: (projectId: string) =>
    invoke("delete_project", { projectId }),

  listProjectSkills: (projectId: string) =>
    invoke<string>("list_project_skills", { projectId }).then(
      (r) => (JSON.parse(r) as SkillInfo[]) ?? []
    ),

  listProjectAgents: (projectId: string) =>
    invoke<string>("list_project_agents", { projectId }).then(
      (r) => (JSON.parse(r) as AgentInfo[]) ?? []
    ),

  getAgentLimits: () =>
    invoke<string>("get_agent_limits").then(
      (r) => JSON.parse(r) as AgentLimits
    ),

  updateAgentLimits: (limits: AgentLimits) =>
    invoke<string>("update_agent_limits", { limits }).then(
      (r) => JSON.parse(r) as AgentLimits
    ),

  setSkillEnabled: (skillRef: string, enabled: boolean) =>
    invoke("set_skill_enabled", { skillRef, enabled }),

  getWebSearchSettings: () =>
    invoke<string>("get_web_search_settings").then(
      parseWebSearchSettings
    ),

  updateWebSearchSettings: (settings: WebSearchSettings) =>
    invoke<string>("update_web_search_settings", { settings }).then(
      parseWebSearchSettings
    ),

  testWebSearch: (providerId: string, query: string) =>
    invoke<string>("test_web_search", { providerId, query }).then(
      (r) => (JSON.parse(r) as WebSearchResult[]) ?? []
    ),

  getMcpConfig: () =>
    invoke<string>("get_mcp_config").then(parseMcpConfig),

  updateMcpConfig: (config: McpConfig) =>
    invoke<string>("update_mcp_config", { config }).then(
      parseMcpConfig
    ),

  getMcpStatus: () =>
    invoke<string>("get_mcp_status").then(
      (r) => (JSON.parse(r) as McpStatus[]) ?? []
    ),

  searchMcpRegistry: (query: string) =>
    invoke<string>("search_mcp_registry", { query }).then(
      (r) => (JSON.parse(r) as McpRegistryServer[]) ?? []
    ),

  subscribeEvents: (sessionId: string, onEvent: (data: string) => void) => {
    const channel = new Channel<string>();
    channel.onmessage = onEvent;
    return invoke("subscribe_events", { sessionId, channel });
  },

  startTerminal: (sessionId: string, cols: number, rows: number) =>
    invoke<string>("start_terminal", { sessionId, cols, rows }).then(
      (r) => JSON.parse(r) as TerminalResource
    ),

  attachTerminal: (sessionId: string, terminalRef: string) =>
    invoke<string>("attach_terminal", { sessionId, terminalRef }).then(
      (r) => JSON.parse(r) as TerminalResource
    ),

  writeTerminal: (sessionId: string, terminalRef: string, input: string) =>
    invoke("write_terminal", { sessionId, terminalRef, input }),

  resizeTerminal: (
    sessionId: string,
    terminalRef: string,
    cols: number,
    rows: number
  ) => invoke("resize_terminal", { sessionId, terminalRef, cols, rows }),

  stopTerminal: (sessionId: string, terminalRef: string) =>
    invoke("stop_terminal", { sessionId, terminalRef }),

  subscribeTerminal: (
    sessionId: string,
    terminalRef: string,
    after: number,
    onEvent: (data: string) => void
  ) => {
    const channel = new Channel<string>();
    channel.onmessage = onEvent;
    return invoke("subscribe_terminal", {
      sessionId,
      terminalRef,
      after,
      channel,
    });
  },

  setBrowserViewport: (
    browserId: string,
    viewport: BrowserViewport | null
  ) => invoke("set_browser_viewport", { browserId, viewport }),

  navigateBrowser: (
    browserId: string,
    url: string,
    viewport: BrowserViewport
  ) => invoke("navigate_browser", { browserId, url, viewport }),

  browserBack: (browserId: string) => invoke("browser_back", { browserId }),

  browserForward: (browserId: string) =>
    invoke("browser_forward", { browserId }),

  browserReload: (browserId: string) =>
    invoke("browser_reload", { browserId }),

  hideBrowser: (browserId: string) =>
    invoke("hide_browser", { browserId }),

  closeBrowser: (browserId: string) =>
    invoke("close_browser", { browserId }),

  // 回执审批决策。
  resolveApproval: (
    sessionId: string,
    requestId: string,
    decision: ApprovalDecision
  ) =>
    invoke("resolve_approval", { sessionId, requestId, decision }),
};
