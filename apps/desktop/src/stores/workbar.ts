import { computed, reactive, ref } from "vue";
import { defineStore } from "pinia";
import type { AttachmentRef } from "@/lib/api";

export type WorkbarLaunchKind = "terminal" | "browser";
export type WorkbarTabKind =
  | "file"
  | "artifact"
  | "background-command"
  | "side-chat"
  | WorkbarLaunchKind;

export interface WorkbarItem {
  kind: WorkbarLaunchKind;
  titleKey: string;
}

export interface WorkbarTab {
  id: string;
  kind: WorkbarTabKind;
  title: string;
  titleKey?: string;
  titleNumber?: number;
  path?: string;
  workspacePath?: string;
  view?: "file" | "diff";
  diff?: string;
  artifact?: AttachmentRef;
  sessionId?: string;
  commandId?: string;
  url?: string;
}

interface WorkbarSessionState {
  open: boolean;
  tabs: WorkbarTab[];
  activeTabId: string | null;
}

const MIN_WIDTH = 320;
const MAX_WIDTH = 960;
const DEFAULT_WIDTH = 720;
const WIDTH_KEY = "foya-workbar-width-v1";
const DRAFT_SESSION_KEY = "__draft__";

const items: WorkbarItem[] = [
  { kind: "terminal", titleKey: "Terminal" },
  { kind: "browser", titleKey: "Browser" },
];

function storedWidth(): number {
  const value = Number(localStorage.getItem(WIDTH_KEY));
  if (!Number.isFinite(value)) return DEFAULT_WIDTH;
  return Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(value)));
}

export const useWorkbarStore = defineStore("workbar", () => {
const width = ref(storedWidth());
const activeSessionId = ref("");
const sessions = reactive<Record<string, WorkbarSessionState>>({});
const nextInstance = {
  terminal: 0,
  browser: 0,
};
let nextSideChat = 0;

function sessionKey(sessionId: string) {
  return sessionId || DRAFT_SESSION_KEY;
}

  function ensureSessionState(
    sessionId = activeSessionId.value,
  ): WorkbarSessionState {
  const key = sessionKey(sessionId);
  if (!sessions[key]) {
    sessions[key] = {
      open: false,
      tabs: [],
      activeTabId: null,
    };
  }
  return sessions[key];
}

const currentSession = computed(() => ensureSessionState());
const open = computed(() => currentSession.value.open);
const tabs = computed(() => currentSession.value.tabs);
const activeTabId = computed(() => currentSession.value.activeTabId);
const allTabs = computed(() =>
    Object.values(sessions).flatMap((session) => session.tabs),
);
const activeTab = computed(() =>
    tabs.value.find((tab) => tab.id === activeTabId.value),
);

function setActiveSession(sessionId: string) {
  if (
    activeSessionId.value === "" &&
    sessionId &&
    sessions[DRAFT_SESSION_KEY] &&
    !sessions[sessionKey(sessionId)]
  ) {
    const draft = sessions[DRAFT_SESSION_KEY];
    for (const tab of draft.tabs) tab.sessionId = sessionId;
    sessions[sessionKey(sessionId)] = draft;
    delete sessions[DRAFT_SESSION_KEY];
  }
  activeSessionId.value = sessionId;
  ensureSessionState(sessionId);
}

function setWidth(value: number, persist = true) {
  width.value = Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(value)));
  if (persist) localStorage.setItem(WIDTH_KEY, String(width.value));
}

function setOpen(value: boolean, sessionId = activeSessionId.value) {
  ensureSessionState(sessionId).open = value;
}

function toggle() {
  setOpen(!currentSession.value.open);
}

function addTab(kind: WorkbarLaunchKind) {
  const item = items.find((candidate) => candidate.kind === kind);
  if (!item) return;
  const sessionId = activeSessionId.value;
  const session = ensureSessionState(sessionId);
  const instance = ++nextInstance[kind];
  const titleKey =
    kind === "browser"
      ? "New tab"
      : instance === 1
        ? item.titleKey
        : "Terminal {number}";
  const tab: WorkbarTab = {
    id: `${kind}-${instance}`,
    kind,
    title: "",
    titleKey,
    titleNumber: instance > 1 ? instance : undefined,
    sessionId: sessionId || undefined,
  };
  session.tabs.push(tab);
  session.activeTabId = tab.id;
  session.open = true;
}

function openBrowser(
  url: string,
  sessionId = activeSessionId.value,
  ownerSessionId = activeSessionId.value,
) {
  const session = ensureSessionState(ownerSessionId);
  const instance = ++nextInstance.browser;
  let title = "Browser";
  try {
    title = new URL(url).hostname.replace(/^www\./, "") || title;
  } catch {
    // BrowserPanel reports invalid URLs without breaking the Workbar.
  }
  const id = `browser-${instance}`;
  session.tabs.push({
    id,
    kind: "browser",
    title,
    sessionId: sessionId || undefined,
    url,
  });
  session.activeTabId = id;
  session.open = true;
}

function openAgentBrowser(
  ownerSessionId: string,
  browserId: string,
  sessionId = ownerSessionId,
) {
  const session = ensureSessionState(ownerSessionId);
  const existing = session.tabs.find(
      (tab) => tab.id === browserId && tab.kind === "browser",
  );
  if (!existing) {
    session.tabs.push({
      id: browserId,
      kind: "browser",
      title: "",
      titleKey: "Agent browser",
      sessionId: sessionId || undefined,
    });
  }
  session.activeTabId = browserId;
  session.open = true;
}

function openSideChat(ownerSessionId: string, sideSessionId: string) {
  const session = ensureSessionState(ownerSessionId);
  const id = `side-chat:${sideSessionId}`;
  const existing = session.tabs.find((tab) => tab.id === id);
  if (!existing) {
    nextSideChat++;
    session.tabs.push({
      id,
      kind: "side-chat",
      title: "",
      titleKey: "Side chat",
      titleNumber: nextSideChat > 1 ? nextSideChat : undefined,
      sessionId: sideSessionId,
    });
  }
  session.activeTabId = id;
  session.open = true;
}

function openFiles(workspacePath: string) {
  const sessionId = activeSessionId.value;
  const session = ensureSessionState(sessionId);
  const id = `file:${workspacePath}`;
  const existing = session.tabs.find((tab) => tab.id === id);
  if (existing) {
    existing.sessionId = sessionId || undefined;
    session.activeTabId = existing.id;
    session.open = true;
    return;
  }
  session.tabs.push({
    id,
    kind: "file",
    title: "",
    titleKey: "Files",
    sessionId: sessionId || undefined,
    workspacePath,
  });
  session.activeTabId = id;
  session.open = true;
}

function openFile(
  workspacePath: string,
  path: string,
  view: "file" | "diff" = "file",
    diff?: string,
) {
  const sessionId = activeSessionId.value;
  const session = ensureSessionState(sessionId);
  const id = `file:${workspacePath}`;
  const title = path.split("/").pop() || path;
  const existing = session.tabs.find((tab) => tab.id === id);
  if (existing) {
    existing.title = title;
    existing.titleKey = undefined;
    existing.path = path;
    existing.workspacePath = workspacePath;
    existing.view = view;
    existing.diff = diff;
    existing.sessionId = sessionId || undefined;
  } else {
    session.tabs.push({
      id,
      kind: "file",
      title,
      path,
      workspacePath,
      view,
      diff,
      sessionId: sessionId || undefined,
    });
  }
  session.activeTabId = id;
  session.open = true;
}

function openArtifact(
  sessionId: string,
  attachment: AttachmentRef,
  ownerSessionId = sessionId,
) {
  const session = ensureSessionState(ownerSessionId);
  const id = `artifact:${attachment.id}`;
  const title = attachment.name || "";
  const existing = session.tabs.find((tab) => tab.id === id);
  if (existing) {
    existing.title = title;
    existing.titleKey = title ? undefined : "Generated file";
    existing.artifact = attachment;
  } else {
    session.tabs.push({
      id,
      kind: "artifact",
      title,
      titleKey: title ? undefined : "Generated file",
      artifact: attachment,
      sessionId,
    });
  }
  session.activeTabId = id;
  session.open = true;
}

function openBackgroundCommand(
  sessionId: string,
  commandId: string,
  command: string,
  ownerSessionId = sessionId,
) {
  const session = ensureSessionState(ownerSessionId);
  const id = `background-command:${commandId}`;
  const existing = session.tabs.find((tab) => tab.id === id);
  if (existing) {
    session.activeTabId = existing.id;
    session.open = true;
    return;
  }
  const normalized = command.replace(/\s+/g, " ").trim();
  session.tabs.push({
    id,
    kind: "background-command",
    title: normalized,
    titleKey: normalized ? undefined : "Background command",
    sessionId,
    commandId,
  });
  session.activeTabId = id;
  session.open = true;
}

function selectTab(tabId: string) {
  const session = currentSession.value;
  if (session.tabs.some((tab) => tab.id === tabId)) {
    session.activeTabId = tabId;
  }
}

function setTabTitle(tabId: string, title: string) {
  const tab = allTabs.value.find((candidate) => candidate.id === tabId);
  if (!tab) return;
  const normalized = title.replace(/\s+/g, " ").trim().slice(0, 80);
  tab.title = normalized;
  tab.titleKey = normalized ? undefined : "New tab";
  tab.titleNumber = undefined;
}

function closeTab(tabId: string): WorkbarTab | undefined {
  const session = currentSession.value;
  const index = session.tabs.findIndex((tab) => tab.id === tabId);
  if (index < 0) return undefined;
  const [removed] = session.tabs.splice(index, 1);
  if (session.activeTabId !== tabId) return removed;
  session.activeTabId =
    session.tabs[index]?.id ?? session.tabs[index - 1]?.id ?? null;
  return removed;
}

function closeFileTabs() {
  const session = currentSession.value;
  const fileIds = new Set(
      session.tabs.filter((tab) => tab.kind === "file").map((tab) => tab.id),
  );
  session.tabs = session.tabs.filter((tab) => tab.kind !== "file");
  if (session.activeTabId && fileIds.has(session.activeTabId)) {
    session.activeTabId = session.tabs[0]?.id ?? null;
  }
}

  function entryContainsPath(
    entryPath: string,
    filePath: string,
    isDirectory: boolean,
  ) {
    return (
      filePath === entryPath ||
      (isDirectory && filePath.startsWith(`${entryPath}/`))
    );
}

function renameEntryTabs(
  workspacePath: string,
  oldPath: string,
  newPath: string,
    isDirectory: boolean,
) {
  const session = currentSession.value;
  session.tabs = session.tabs.map((tab) => {
    if (
      tab.kind !== "file" ||
      tab.workspacePath !== workspacePath ||
      !tab.path ||
      !entryContainsPath(oldPath, tab.path, isDirectory)
    ) {
      return tab;
    }

    const path = `${newPath}${tab.path.slice(oldPath.length)}`;
    return {
      ...tab,
      path,
      title: path.split("/").pop() || path,
      titleKey: undefined,
    };
  });
}

function resetDeletedEntryTab(
  workspacePath: string,
  path: string,
    isDirectory: boolean,
) {
  const tab = currentSession.value.tabs.find(
    (candidate) =>
      candidate.kind === "file" &&
      candidate.workspacePath === workspacePath &&
      Boolean(
        candidate.path &&
            entryContainsPath(path, candidate.path, isDirectory),
        ),
  );
  if (!tab) return;
  tab.title = "";
  tab.titleKey = "Files";
  tab.path = undefined;
  tab.diff = undefined;
}

function ownerSessionForContentSession(sessionId: string): string | undefined {
  for (const [ownerSessionId, session] of Object.entries(sessions)) {
    if (session.tabs.some((tab) => tab.sessionId === sessionId)) {
      return ownerSessionId === DRAFT_SESSION_KEY ? "" : ownerSessionId;
    }
  }
  return undefined;
}

function removeSession(sessionId: string) {
  delete sessions[sessionKey(sessionId)];
  for (const session of Object.values(sessions)) {
    const removedActive = session.activeTabId
      ? session.tabs.some(
          (tab) => tab.id === session.activeTabId && tab.sessionId === sessionId,
        )
      : false;
    session.tabs = session.tabs.filter((tab) => tab.sessionId !== sessionId);
    if (removedActive) {
      session.activeTabId = session.tabs[0]?.id ?? null;
    }
  }
}

  return {
    items,
    tabs,
    allTabs,
    width,
    open,
    activeTabId,
    activeTab,
    minWidth: MIN_WIDTH,
    maxWidth: MAX_WIDTH,
    setActiveSession,
    setWidth,
    setOpen,
    toggle,
    addTab,
    openBrowser,
    openAgentBrowser,
    openSideChat,
    openFiles,
    openFile,
    openArtifact,
    openBackgroundCommand,
    selectTab,
    setTabTitle,
    closeTab,
    closeFileTabs,
    renameEntryTabs,
    resetDeletedEntryTab,
    ownerSessionForContentSession,
    removeSession,
  };
});
