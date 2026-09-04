// 内核 API 封装:所有对 Rust 命令的调用集中在此,组件与 composable 只依赖这层。
import { invoke, Channel } from "@tauri-apps/api/core";
import { open } from "@tauri-apps/plugin-dialog";

// 会话元信息(与 Go session.Session 对齐的子集)。
export type TaskStatus = "pending" | "in_progress" | "completed";

export interface SessionTask {
  content: string;
  status: TaskStatus;
}

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
  agent_mode?: "execute" | "plan" | "plan_ready";
  connection_id: string;
  model: string;
  reasoning_effort?: ReasoningEffort;
  project_id?: string;
  approval_mode?: ApprovalMode;
  title?: string;
  title_is_manual?: boolean;
  pinned?: boolean;
  pinned_at?: string;
  tasks?: SessionTask[];
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

export interface ForkSessionOptions {
  title?: string;
  through_seq?: number;
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
  attachments?: AttachmentRef[];
  browser_elements?: BrowserElementSelection[];
  position: number;
  created_at: string;
  updated_at: string;
}

export interface SubmitTurnResult {
  run_id?: string;
  status: "started" | "queued";
  queued?: QueuedMessage;
}

export interface AttachmentRef {
  id: string;
  name: string;
  kind: "image";
  media_type: string;
  bytes: number;
  width?: number;
  height?: number;
  sha256?: string;
}

export type CanvasNodeType =
  | "image"
  | "video"
  | "text"
  | "generation";

export interface CanvasGenerationSpec {
  connection_id?: string;
  mode: "image" | "video";
  model?: string;
  aspect_ratio?: string;
  quality?: string;
  count?: number;
  duration?: number;
}

export interface CanvasNode {
  id: string;
  type: CanvasNodeType;
  title?: string;
  asset_id?: string;
  text?: string;
  prompt?: string;
  parent_id?: string;
  status?: "idle" | "queued" | "running" | "success" | "error";
  error?: string;
  generation?: CanvasGenerationSpec;
  x: number;
  y: number;
  width: number;
  height: number;
  rotation?: number;
  z_index: number;
}

export interface CanvasEdge {
  id: string;
  from_node_id: string;
  to_node_id: string;
  kind?: "reference" | "variation" | "output";
}

export interface CanvasViewport {
  x: number;
  y: number;
  zoom: number;
}

export interface CanvasAsset {
  id: string;
  name: string;
  kind: "image" | "video";
  media_type: string;
  bytes: number;
  width?: number;
  height?: number;
  sha256: string;
  created_at: string;
}

export interface CanvasDocument {
  id: string;
  title: string;
  session_id?: string;
  project_id?: string;
  revision: number;
  nodes: CanvasNode[];
  edges: CanvasEdge[];
  assets: CanvasAsset[];
  viewport: CanvasViewport;
  background: "dots" | "grid" | "blank";
  created_at: string;
  updated_at: string;
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

export interface DailyUsage {
  date: string;
  message_count: number;
  token_count: number;
}

export interface ModelUsage {
  model: string;
  token_count: number;
  request_count: number;
  share: number;
}

export interface UsageStatistics {
  range_days: 7 | 30;
  total_tokens: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  session_count: number;
  message_count: number;
  active_days: number;
  current_streak: number;
  most_used_model?: string;
  most_used_model_share: number;
  activity: DailyUsage[];
  model_usage: ModelUsage[];
}

export interface CompactSessionResult {
  through_seq: number;
  estimated_tokens_before: number;
  estimated_tokens_after: number;
}

export interface ConnectionModelCatalog {
  models: string[];
  context_windows: Record<string, number>;
  capabilities?: Record<string, { image_input?: boolean }>;
}

export interface ModelSettings {
  context_window?: number;
  max_input_tokens?: number;
  max_output_tokens?: number;
  image_input: boolean;
  image_generation: boolean;
  video_generation: boolean;
  audio_generation: boolean;
  tool_calling: boolean;
  web_search: boolean;
  reasoning_efforts?: ReasoningEffort[];
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

export interface BackgroundCommand {
  command_id: string;
  session_id: string;
  command: string;
  pid?: number;
  running: boolean;
  exit_code?: number;
  stdout?: string;
  stderr?: string;
  started_at: string;
  backgrounded_by?: "agent" | "user";
  stopped_by?: "agent" | "user";
}

export interface BrowserViewport {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface BrowserElementSelection {
  page_url: string;
  page_title: string;
  tag: string;
  selector: string;
  text: string;
  html: string;
}

export interface BrowserActionRequest {
  id: string;
  session_id: string;
  tool_call_id?: string;
  browser_id: string;
  action:
    | "open"
    | "navigate"
    | "snapshot"
    | "click"
    | "type"
    | "press_key"
    | "scroll"
    | "wait"
    | "extract"
    | "back"
    | "reload"
    | "screenshot";
  url?: string;
  ref?: string;
  observation_id?: string;
  text?: string;
  key?: string;
  direction?: "up" | "down" | "left" | "right";
  amount?: number;
  timeout_ms?: number;
  clear?: boolean;
  full_page?: boolean;
  created_at: string;
}

export interface BrowserActionResult {
  url?: string;
  title?: string;
  revision?: number;
  observation_id?: string;
  snapshot?: string;
  screenshot_base64?: string;
  media_type?: string;
  code?: string;
  message?: string;
  pre_url?: string;
  post_url?: string;
  verified?: boolean;
  actual_text?: string;
  trace?: Record<string, unknown>;
  error?: string;
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

export interface QuestionOption {
  label: string;
  description?: string;
  recommended?: boolean;
}

export interface UserQuestion {
  id: string;
  question: string;
  description?: string;
  options?: QuestionOption[];
  allow_custom?: boolean;
}

export interface PendingQuestionBatch {
  id: string;
  session_id: string;
  run_id?: string;
  tool_call_id?: string;
  questions: UserQuestion[];
  created_at: string;
}

export interface QuestionAnswer {
  question_id: string;
  value: string;
}

// 对话消息(与 Go message.Message 对齐)。
// error 为前端乐观态:发送失败时标记气泡,不进后端。
export interface ToolCallView {
  id: string;
  name: string;
  input: string;
  status: "queued" | "running" | "done" | "error";
  output?: string;
  child_session_id?: string;
  agent_ref?: string;
  agent_name?: string;
  agent_run?: AgentRunSnapshot;
  child_messages?: ChatMessage[];
  // 文件变更 diff(仅 write/edit 工具),统一 diff 文本,前端行内着色展示。
  diff?: string;
  attachments?: AttachmentRef[];
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
  command?: string;
  attachments?: AttachmentRef[];
  browser_elements?: BrowserElementSelection[];
  event_seq?: number;
  reasoning?: string;
  tool_calls?: ToolCallView[];
  // 有序段落(仅 assistant)。存在时优先按其渲染;缺失时回退到 reasoning/tool_calls/content 扁平字段。
  segments?: MessageSegment[];
  tool_call_id?: string;
  // 文件变更 diff(仅 tool 角色历史消息),用于历史回放时回填工具段。
  diff?: string;
  // 最终 assistant 消息携带的通用回合生命周期时间。
  turn_started_at?: string;
  turn_completed_at?: string;
  turn_status?: "completed" | "failed" | "cancelled";
  turn_reason?: string;
  error?: boolean;
}

// Connection 是一个独立模型账号或端点。API Key 仅在写入时携带。
export type ConnectionType = "language" | "image" | "video";
export type VideoProtocol = "seedance" | "minimax_h3";

export interface ConnectionConfig {
  id?: string;
  name: string;
  type: ConnectionType;
  video_protocol?: VideoProtocol;
  kind: string;
  auth_kind: "api_key";
  base_url: string;
  model_settings: Record<string, ModelSettings>;
  models?: string[];
  models_cached?: boolean;
  api_key?: string;
  has_api_key?: boolean;
  sort_order: number;
}

export interface ModelRef {
  connection_id: string;
  model: string;
}

export interface DefaultModels {
  language: ModelRef;
  fast: ModelRef;
  image: ModelRef;
  video: ModelRef;
}

export interface SkillInfo {
  ref: string;
  name: string;
  description?: string;
  scope: "builtin" | "global" | "user" | "project";
  path?: string;
  root?: string;
  main_path?: string;
  enabled: boolean;
  pinned?: boolean;
  allowed_tools?: string[];
  required_tools?: string[];
  required_capabilities?: string[];
  resources?: SkillResourceInfo[];
  content_hash?: string;
  diagnostics?: SkillDiagnostic[];
}

export interface SkillResourceInfo {
  path: string;
  size?: number;
  media_type?: string;
}

export interface SkillDiagnostic {
  ref?: string;
  name?: string;
  path?: string;
  code: string;
  severity: string;
  message: string;
  field?: string;
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

export type FeishuBotStatus = "stopped" | "running" | "error";

export interface FeishuBotSettings {
  id: string;
  kind: "feishu";
  name: string;
  enabled: boolean;
  app_id: string;
  has_app_secret: boolean;
  connection_id?: string;
  model?: string;
  project_id?: string;
  approval_mode: "auto" | "full_access";
  allowed_users: string[];
  allowed_chats: string[];
  allow_all: boolean;
  status: FeishuBotStatus;
  last_error?: string;
}

export interface FeishuBotUpdate {
  name: string;
  enabled: boolean;
  app_id: string;
  app_secret?: string;
  connection_id?: string;
  model?: string;
  project_id?: string;
  approval_mode: "auto" | "full_access";
  allowed_users: string[];
  allowed_chats: string[];
  allow_all: boolean;
}

export type ChannelCreate = FeishuBotUpdate & { kind: "feishu" };

export type AutomationRunStatus =
  | "idle"
  | "running"
  | "completed"
  | "failed"
  | "cancelled";

export interface AutomationTask {
  id: string;
  name: string;
  prompt: string;
  cron: string;
  timezone: string;
  enabled: boolean;
  connection_id?: string;
  model?: string;
  project_id?: string;
  approval_mode: "auto" | "full_access";
  last_status: AutomationRunStatus;
  last_error?: string;
  last_session_id?: string;
  last_run_at?: string;
  next_run_at?: string;
  created_at: string;
  updated_at: string;
}

export interface AutomationInput {
  name: string;
  prompt: string;
  cron: string;
  timezone?: string;
  enabled: boolean;
  connection_id?: string;
  model?: string;
  project_id?: string;
  approval_mode: "auto" | "full_access";
}

export type HookEvent =
  | "SessionStart"
  | "UserPromptSubmit"
  | "PreToolUse"
  | "PostToolUse"
  | "Stop"
  | "Notification";

export interface HookConfig {
  id?: string;
  name?: string;
  event: HookEvent;
  matcher?: string;
  command: string;
  timeout?: number;
  enabled?: boolean;
}

export type CommandScope = "builtin" | "global" | "project";
export type CommandKind = "prompt" | "workflow";

export interface CommandInfo {
  ref: string;
  name: string;
  description?: string;
  scope: CommandScope;
  project_id?: string;
  kind: CommandKind;
  path?: string;
  body?: string;
  builtin?: boolean;
  updated_at?: string;
}

export interface CommandExecution {
  command: CommandInfo;
  status: string;
  submission?: SubmitTurnResult;
  workflow?: WorkflowRecord;
  message?: string;
}

export interface WorkflowRecord {
  id: string;
  session_id: string;
  kind: "plan" | "spec" | "goal";
  status: "active" | "ready" | "approved" | "closed";
  goal: string;
  content?: string;
  path?: string;
  revision: number;
  created_at: string;
  updated_at: string;
}

export interface WorkflowApproval {
  workflow: WorkflowRecord;
  submission: SubmitTurnResult;
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

export type ContextItemKind = "rule" | "memory";
export type ContextItemScope = "global" | "project";
export type RuleTrigger = "always" | "glob" | "model_decision" | "manual";

export interface ContextItem {
  id: string;
  name?: string;
  description?: string;
  trigger?: RuleTrigger;
  globs?: string[];
  path?: string;
  scope: ContextItemScope;
  project_id?: string;
  content: string;
  created_at: string;
  updated_at: string;
}

export interface MemorySettings {
  enabled: boolean;
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

  forkSession: (sessionId: string, opts?: ForkSessionOptions) =>
    invoke<string>("fork_session", { sessionId, options: opts ?? null }).then(
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

  loadUsageStatistics: (days: 7 | 30) =>
    invoke<string>("load_usage_statistics", { days }).then(
      (r) => JSON.parse(r) as UsageStatistics
    ),

  submitTurn: (
    sessionId: string,
    message: string,
    attachments: AttachmentRef[] = [],
    browserElements: BrowserElementSelection[] = []
  ) =>
    invoke<string>("submit_turn", {
      sessionId,
      message,
      attachments,
      browserElements,
    }).then(
      (r) => JSON.parse(r) as SubmitTurnResult
    ),

  uploadImage: async (sessionId: string, file: File) => {
    const data = Array.from(new Uint8Array(await file.arrayBuffer()));
    return invoke<string>("upload_image", { sessionId, name: file.name, data }).then(
      (r) => (JSON.parse(r) as { attachment: AttachmentRef }).attachment
    );
  },

  readArtifact: (sessionId: string, artifactId: string) =>
    invoke<number[]>("read_artifact", { sessionId, artifactId }).then(
      (bytes) => new Uint8Array(bytes)
    ),

  deleteArtifact: (sessionId: string, artifactId: string) =>
    invoke("delete_artifact", { sessionId, artifactId }),

  listCanvases: () =>
    invoke<string>("list_canvases", { sessionId: undefined }).then(
      (result) => (JSON.parse(result) as CanvasDocument[]) ?? []
    ),

  createCanvas: (title?: string) =>
    invoke<string>("create_canvas", {
      sessionId: undefined,
      projectId: undefined,
      title,
    }).then(
      (result) => JSON.parse(result) as CanvasDocument
    ),

  getCanvas: (canvasId: string) =>
    invoke<string>("get_canvas", { canvasId }).then(
      (result) => JSON.parse(result) as CanvasDocument
    ),

  updateCanvas: (
    canvasId: string,
    patch: {
      expected_revision: number;
      title?: string;
      nodes?: CanvasNode[];
      edges?: CanvasEdge[];
      viewport?: CanvasViewport;
      background?: CanvasDocument["background"];
    }
  ) =>
    invoke<string>("update_canvas", { canvasId, patch }).then(
      (result) => JSON.parse(result) as CanvasDocument
    ),

  deleteCanvas: (canvasId: string) => invoke("delete_canvas", { canvasId }),

  uploadCanvasAsset: async (canvasId: string, file: File) => {
    const data = Array.from(new Uint8Array(await file.arrayBuffer()));
    return invoke<string>("upload_canvas_asset", {
      canvasId,
      name: file.name,
      mediaType: file.type || "application/octet-stream",
      data,
    }).then(
      (result) => JSON.parse(result) as { asset: CanvasAsset; canvas: CanvasDocument }
    );
  },

  readCanvasAsset: (canvasId: string, assetId: string) =>
    invoke<number[]>("read_canvas_asset", { canvasId, assetId }).then(
      (bytes) => new Uint8Array(bytes)
    ),

  generateCanvasImage: (
    canvasId: string,
    request: {
      expected_revision: number;
      config_node_id: string;
      output_node_id: string;
      connection_id?: string;
    }
  ) =>
    invoke<string>("generate_canvas_image", { canvasId, request }).then(
      (result) => JSON.parse(result) as CanvasDocument
    ),

  generateCanvasVideo: (
    canvasId: string,
    request: {
      expected_revision: number;
      config_node_id: string;
      output_node_id: string;
      connection_id?: string;
    }
  ) =>
    invoke<string>("generate_canvas_video", { canvasId, request }).then(
      (result) => JSON.parse(result) as CanvasDocument
    ),

  subscribeCanvasEvents: (canvasId: string, onEvent: (data: string) => void) => {
    const channel = new Channel<string>();
    channel.onmessage = onEvent;
    return invoke("subscribe_canvas_events", { canvasId, channel });
  },

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

  enqueueMessage: (
    sessionId: string,
    message: string,
    attachments: AttachmentRef[] = [],
    browserElements: BrowserElementSelection[] = []
  ) =>
    invoke<string>("enqueue_message", {
      sessionId,
      message,
      attachments,
      browserElements,
    }).then(
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

  cancelTool: (sessionId: string, toolCallId: string) =>
    invoke("cancel_tool", { sessionId, toolCallId }),

  backgroundTool: (sessionId: string, toolCallId: string) =>
    invoke<string>("background_tool", { sessionId, toolCallId }).then(
      (result) => JSON.parse(result) as BackgroundCommand
    ),

  revealToolCommand: (sessionId: string, toolCallId: string) =>
    invoke<string>("reveal_tool_command", { sessionId, toolCallId }).then(
      (result) => JSON.parse(result) as BackgroundCommand
    ),

  listBackgroundCommands: (sessionId: string) =>
    invoke<string>("list_background_commands", { sessionId }).then(
      (result) => (JSON.parse(result) as BackgroundCommand[]) ?? []
    ),

  getBackgroundCommand: (sessionId: string, commandId: string) =>
    invoke<string>("get_background_command", { sessionId, commandId }).then(
      (result) => JSON.parse(result) as BackgroundCommand
    ),

  stopBackgroundCommand: (sessionId: string, commandId: string) =>
    invoke<string>("stop_background_command", { sessionId, commandId }).then(
      (result) => JSON.parse(result) as BackgroundCommand
    ),

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

  listConnectionModels: (connectionId: string, refresh = false) =>
    invoke<string>("list_connection_models", { connectionId, refresh }).then(
      (r) => {
        const result = JSON.parse(r) as Partial<ConnectionModelCatalog>;
        return {
          models: result.models ?? [],
          context_windows: result.context_windows ?? {},
          capabilities: result.capabilities ?? {},
        } satisfies ConnectionModelCatalog;
      }
    ),

  getDefaultModels: () =>
    invoke<string>("get_default_models").then(
      (r) => JSON.parse(r) as DefaultModels
    ),

  updateDefaultModels: (defaults: DefaultModels) =>
    invoke<string>("update_default_models", { defaults }).then(
      (r) => JSON.parse(r) as DefaultModels
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

  listContextItems: (
    kind: ContextItemKind,
    scope: ContextItemScope,
    projectId?: string
  ) =>
    invoke<string>("list_context_items", { kind, scope, projectId }).then(
      (r) => (JSON.parse(r) as ContextItem[]) ?? []
    ),

  createContextItem: (
    kind: ContextItemKind,
    item: {
      scope: ContextItemScope;
      project_id?: string;
      content: string;
      name?: string;
      description?: string;
      trigger?: RuleTrigger;
      globs?: string[];
      path?: string;
    }
  ) =>
    invoke<string>("create_context_item", { kind, item }).then(
      (r) => JSON.parse(r) as ContextItem
    ),

  updateContextItem: (
    kind: ContextItemKind,
    itemId: string,
    item: {
      content: string;
      name?: string;
      description?: string;
      trigger?: RuleTrigger;
      globs?: string[];
      path?: string;
    }
  ) =>
    invoke<string>("update_context_item", { kind, itemId, item }).then(
      (r) => JSON.parse(r) as ContextItem
    ),

  deleteContextItem: (kind: ContextItemKind, itemId: string) =>
    invoke("delete_context_item", { kind, itemId }),

  subscribeContextEvents: (onEvent: (data: string) => void) => {
    const channel = new Channel<string>();
    channel.onmessage = onEvent;
    return invoke("subscribe_context_events", { channel });
  },

  getMemorySettings: () =>
    invoke<string>("get_memory_settings").then(
      (r) => JSON.parse(r) as MemorySettings
    ),

  updateMemorySettings: (settings: MemorySettings) =>
    invoke<string>("update_memory_settings", { settings }).then(
      (r) => JSON.parse(r) as MemorySettings
    ),

  getAgentLimits: () =>
    invoke<string>("get_agent_limits").then(
      (r) => JSON.parse(r) as AgentLimits
    ),

  updateAgentLimits: (limits: AgentLimits) =>
    invoke<string>("update_agent_limits", { limits }).then(
      (r) => JSON.parse(r) as AgentLimits
    ),

  getFeishuBotSettings: () =>
    invoke<string>("get_feishu_bot_settings").then(
      (r) => JSON.parse(r) as FeishuBotSettings
    ),

  updateFeishuBotSettings: (settings: FeishuBotUpdate) =>
    invoke<string>("update_feishu_bot_settings", { settings }).then(
      (r) => JSON.parse(r) as FeishuBotSettings
    ),

  listChannels: () =>
    invoke<string>("list_channels").then(
      (r) => (JSON.parse(r) as FeishuBotSettings[]) ?? []
    ),

  createChannel: (settings: ChannelCreate) =>
    invoke<string>("create_channel", { settings }).then(
      (r) => JSON.parse(r) as FeishuBotSettings
    ),

  updateChannel: (channelId: string, settings: FeishuBotUpdate) =>
    invoke<string>("update_channel", { channelId, settings }).then(
      (r) => JSON.parse(r) as FeishuBotSettings
    ),

  deleteChannel: (channelId: string) =>
    invoke("delete_channel", { channelId }),

  listAutomations: () =>
    invoke<string>("list_automations").then(
      (r) => (JSON.parse(r) as AutomationTask[]) ?? []
    ),

  createAutomation: (input: AutomationInput) =>
    invoke<string>("create_automation", { input }).then(
      (r) => JSON.parse(r) as AutomationTask
    ),

  updateAutomation: (automationId: string, input: AutomationInput) =>
    invoke<string>("update_automation", { automationId, input }).then(
      (r) => JSON.parse(r) as AutomationTask
    ),

  deleteAutomation: (automationId: string) =>
    invoke("delete_automation", { automationId }),

  runAutomation: (automationId: string) =>
    invoke<string>("run_automation", { automationId }).then(
      (r) => JSON.parse(r) as AutomationTask
    ),

  getHooks: (scope: "global" | "project", projectId?: string) =>
    invoke<string>("get_hooks", { scope, projectId: projectId ?? "" }).then(
      (r) => (JSON.parse(r) as HookConfig[]) ?? []
    ),

  updateHooks: (
    scope: "global" | "project",
    hooks: HookConfig[],
    projectId?: string
  ) =>
    invoke<string>("update_hooks", {
      request: {
        scope,
        project_id: projectId ?? "",
        hooks,
      },
    }).then((r) => (JSON.parse(r) as HookConfig[]) ?? []),

  listCommands: (scope: "global" | "project", projectId?: string) =>
    invoke<string>("list_commands", { scope, projectId: projectId ?? "" }).then(
      (r) => (JSON.parse(r) as CommandInfo[]) ?? []
    ),

  createCommand: (input: {
    scope: "global" | "project";
    project_id?: string;
    name: string;
  }) =>
    invoke<string>("create_command", { request: input }).then(
      (r) => JSON.parse(r) as CommandInfo
    ),

  updateCommand: (
    commandRef: string,
    input: {
      scope: "global" | "project";
      project_id?: string;
      name: string;
      description?: string;
      body: string;
    }
  ) =>
    invoke<string>("update_command", { commandRef, request: input }).then(
      (r) => JSON.parse(r) as CommandInfo
    ),

  deleteCommand: (
    commandRef: string,
    scope: "global" | "project",
    projectId?: string
  ) =>
    invoke("delete_command", {
      commandRef,
      scope,
      projectId: projectId ?? "",
    }),

  listSessionCommands: (sessionId: string) =>
    invoke<string>("list_session_commands", { sessionId }).then(
      (r) => (JSON.parse(r) as CommandInfo[]) ?? []
    ),

  executeCommand: (sessionId: string, name: string, args = "") =>
    invoke<string>("execute_command", { sessionId, name, args }).then(
      (r) => JSON.parse(r) as CommandExecution
    ),

  getWorkflow: (sessionId: string) =>
    invoke<string>("get_workflow", { sessionId }).then(
      (r) => JSON.parse(r) as WorkflowRecord | null
    ),

  approveWorkflow: (sessionId: string, workflowId: string) =>
    invoke<string>("approve_workflow", { sessionId, workflowId }).then(
      (r) => JSON.parse(r) as WorkflowApproval
    ),

  setSkillEnabled: (skillRef: string, enabled: boolean) =>
    invoke("set_skill_enabled", { skillRef, enabled }),

  setSkillPinned: (skillRef: string, pinned: boolean) =>
    invoke("set_skill_pinned", { skillRef, pinned }),

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

  setBrowserElementPicker: (browserId: string, enabled: boolean) =>
    invoke("set_browser_element_picker", { browserId, enabled }),

  executeBrowserAction: (request: BrowserActionRequest) =>
    invoke<BrowserActionResult>("execute_browser_action", { request }),

  resolveBrowserAction: (
    sessionId: string,
    requestId: string,
    result: BrowserActionResult
  ) => invoke("resolve_browser_action", { sessionId, requestId, result }),

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

  answerQuestions: (
    sessionId: string,
    batchId: string,
    answers: QuestionAnswer[]
  ) => invoke("answer_questions", { sessionId, batchId, answers }),

  cancelQuestions: (sessionId: string, batchId: string) =>
    invoke("cancel_questions", { sessionId, batchId }),
};
