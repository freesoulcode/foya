import { api } from "@/lib/api";

const subscribedSessions = new Set<string>();
const deletedSessions = new Set<string>();
const streamingIndexes: Record<string, number> = {};

export function isDeletedSession(sessionId: string) {
  return deletedSessions.has(sessionId);
}

export function restoreSessionRuntime(sessionId: string) {
  deletedSessions.delete(sessionId);
}

export function deleteSessionRuntime(sessionId: string) {
  deletedSessions.add(sessionId);
  subscribedSessions.delete(sessionId);
  delete streamingIndexes[sessionId];
}

export function getStreamingIndex(sessionId: string) {
  return streamingIndexes[sessionId] ?? -1;
}

export function setStreamingIndex(sessionId: string, index: number) {
  streamingIndexes[sessionId] = index;
}

export async function subscribeSessionEvents(
  sessionId: string,
  onEvent: (data: string) => void,
) {
  if (subscribedSessions.has(sessionId)) return;
  subscribedSessions.add(sessionId);
  setStreamingIndex(sessionId, -1);
  try {
    await api.subscribeEvents(sessionId, onEvent);
  } catch (error) {
    subscribedSessions.delete(sessionId);
    throw error;
  }
}
