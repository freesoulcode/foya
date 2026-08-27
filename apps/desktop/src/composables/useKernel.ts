import { ref, computed } from "vue";
import { api, type Session, type ChatMessage } from "@/lib/api";

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
const activeId = ref<string>("");
const streaming = ref(false);

// 每个会话的消息与订阅状态(按会话缓存,切换时不丢)。
const messagesBySession = ref<Record<string, ChatMessage[]>>({});
const subscribed = new Set<string>();
const streamingIdx: Record<string, number> = {};

const activeMessages = computed<ChatMessage[]>(() => messagesBySession.value[activeId.value] ?? []);
const activeSession = computed(() => sessions.value.find((s) => s.id === activeId.value));
const isDraft = computed(() => activeId.value === "");

function ensureBucket(id: string) {
  if (!messagesBySession.value[id]) messagesBySession.value[id] = [];
}

// 处理某会话的一条 SSE 事件。
function handleEvent(sessionId: string, data: string) {
  let ev: KernelEvent;
  try {
    ev = JSON.parse(data);
  } catch {
    return;
  }
  ensureBucket(sessionId);
  const bucket = messagesBySession.value[sessionId];

  switch (ev.kind) {
    case "message_delta": {
      const delta = ev.payload as string;
      let idx = streamingIdx[sessionId] ?? -1;
      if (idx < 0) {
        bucket.push({ role: "assistant", content: "" });
        idx = bucket.length - 1;
        streamingIdx[sessionId] = idx;
        if (sessionId === activeId.value) streaming.value = true;
      }
      bucket[idx].content += delta;
      break;
    }
    case "message_end": {
      const m = ev.payload as ChatMessage;
      const idx = streamingIdx[sessionId] ?? -1;
      if (m.role === "assistant" && idx >= 0) {
        bucket[idx] = m;
        streamingIdx[sessionId] = -1;
        if (sessionId === activeId.value) streaming.value = false;
      }
      // user 消息回执已在 send 时乐观插入,忽略以免重复。
      break;
    }
    case "turn_complete":
      streamingIdx[sessionId] = -1;
      if (sessionId === activeId.value) streaming.value = false;
      break;
    case "error":
      bucket.push({ role: "assistant", content: `⚠️ ${String(ev.payload)}` });
      streamingIdx[sessionId] = -1;
      if (sessionId === activeId.value) streaming.value = false;
      break;
  }
}

// 订阅某会话事件流(幂等)。
async function subscribe(sessionId: string) {
  if (subscribed.has(sessionId)) return;
  subscribed.add(sessionId);
  streamingIdx[sessionId] = -1;
  await api.subscribeEvents(sessionId, (data) => handleEvent(sessionId, data));
}

// 连接内核:拉会话列表,选中或新建一个会话。
async function connect() {
  if (ready.value || connecting.value) return;
  connecting.value = true;
  connectError.value = "";
  try {
    sessions.value = await api.listSessions();
    sessions.value.sort((a, b) => b.updated_at.localeCompare(a.updated_at));
    if (sessions.value.length > 0) {
      await select(sessions.value[0].id);
    } else {
      activeId.value = "";
      ensureBucket("");
    }
    ready.value = true;
  } catch (e) {
    // 不再静默吞错:把失败原因暴露到界面,便于定位(如内核未就绪、命令缺失)。
    connectError.value = String(e);
    console.error("连接内核失败:", e);
  } finally {
    connecting.value = false;
  }
}

// 进入新对话草稿态:不立即创建会话,直到用户发送第一条消息。
function newSession() {
  activeId.value = "";
  ensureBucket("");
  streaming.value = false;
}

// 切换到某会话:首次进入时加载历史并订阅。
async function select(id: string) {
  activeId.value = id;
  streaming.value = (streamingIdx[id] ?? -1) >= 0;
  if (!messagesBySession.value[id] || messagesBySession.value[id].length === 0) {
    const history = await api.loadHistory(id);
    messagesBySession.value[id] = history;
  }
  await subscribe(id);
}

// 发送一条消息(乐观插入用户消息)。
// 若当前为草稿态(尚未创建会话),先创建会话再发送。
async function send(text: string) {
  if (!text.trim()) return;

  let id = activeId.value;

  if (!id) {
    const s = await api.createSession();
    sessions.value.unshift(s);
    messagesBySession.value[s.id] = messagesBySession.value[""] ?? [];
    delete messagesBySession.value[""];
    id = s.id;
    activeId.value = id;
    await subscribe(id);
  }

  ensureBucket(id);
  messagesBySession.value[id].push({ role: "user", content: text });
  await api.submitTurn(id, text);
}

export function useKernel() {
  return {
    ready,
    connecting,
    connectError,
    streaming,
    sessions,
    activeId,
    activeSession,
    isDraft,
    messages: activeMessages,
    connect,
    newSession,
    select,
    send,
  };
}
