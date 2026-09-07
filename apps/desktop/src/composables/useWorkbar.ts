import { computed, reactive, ref } from "vue";

export type WorkbarLaunchKind = "terminal" | "browser";
export type WorkbarTabKind = "file" | "background-command" | WorkbarLaunchKind;

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
  projectPath?: string;
  view?: "file" | "diff";
  diff?: string;
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

const width = ref(storedWidth());
const activeSessionId = ref("");
const sessions = reactive<Record<string, WorkbarSessionState>>({});
const nextInstance = {
  terminal: 0,
  browser: 0,
};

function sessionKey(sessionId: string) {
  return sessionId || DRAFT_SESSION_KEY;
}

function ensureSessionState(sessionId = activeSessionId.value): WorkbarSessionState {
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
  Object.values(sessions).flatMap((session) => session.tabs)
);
const activeTab = computed(() =>
  tabs.value.find((tab) => tab.id === activeTabId.value)
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

function openBrowser(url: string) {
  const sessionId = activeSessionId.value;
  const session = ensureSessionState(sessionId);
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

function openAgentBrowser(sessionId: string, browserId: string) {
  const session = ensureSessionState(sessionId);
  const existing = session.tabs.find(
    (tab) => tab.id === browserId && tab.kind === "browser"
  );
  if (!existing) {
    session.tabs.push({
      id: browserId,
      kind: "browser",
      title: "",
      titleKey: "Agent browser",
      sessionId,
    });
  }
  session.activeTabId = browserId;
  session.open = true;
}

function openFiles(projectPath: string) {
  const sessionId = activeSessionId.value;
  const session = ensureSessionState(sessionId);
  const id = `file:${projectPath}`;
  const existing = session.tabs.find((tab) => tab.id === id);
  if (existing) {
    session.activeTabId = existing.id;
    return;
  }
  session.tabs.push({
    id,
    kind: "file",
    title: "",
    titleKey: "Files",
    sessionId: sessionId || undefined,
    projectPath,
  });
  session.activeTabId = id;
  session.open = true;
}

function openFile(
  projectPath: string,
  path: string,
  view: "file" | "diff" = "file",
  diff?: string
) {
  const sessionId = activeSessionId.value;
  const session = ensureSessionState(sessionId);
  const id = `file:${projectPath}`;
  const title = path.split("/").pop() || path;
  const existing = session.tabs.find((tab) => tab.id === id);
  if (existing) {
    existing.title = title;
    existing.path = path;
    existing.view = view;
    existing.diff = diff;
  } else {
    session.tabs.push({
      id,
      kind: "file",
      title,
      path,
      projectPath,
      view,
      diff,
      sessionId: sessionId || undefined,
    });
  }
  session.activeTabId = id;
  session.open = true;
}

function openBackgroundCommand(
  sessionId: string,
  commandId: string,
  command: string
) {
  const session = ensureSessionState(sessionId);
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

function closeTab(tabId: string) {
  const session = currentSession.value;
  const index = session.tabs.findIndex((tab) => tab.id === tabId);
  if (index < 0) return;
  session.tabs.splice(index, 1);
  if (session.activeTabId !== tabId) return;
  session.activeTabId =
    session.tabs[index]?.id ?? session.tabs[index - 1]?.id ?? null;
}

function closeFileTabs() {
  const session = currentSession.value;
  const fileIds = new Set(
    session.tabs.filter((tab) => tab.kind === "file").map((tab) => tab.id)
  );
  session.tabs = session.tabs.filter((tab) => tab.kind !== "file");
  if (session.activeTabId && fileIds.has(session.activeTabId)) {
    session.activeTabId = session.tabs[0]?.id ?? null;
  }
}

function entryContainsPath(entryPath: string, filePath: string, isDirectory: boolean) {
  return filePath === entryPath || (isDirectory && filePath.startsWith(`${entryPath}/`));
}

function renameEntryTabs(
  projectPath: string,
  oldPath: string,
  newPath: string,
  isDirectory: boolean
) {
  const session = currentSession.value;
  session.tabs = session.tabs.map((tab) => {
    if (
      tab.kind !== "file" ||
      tab.projectPath !== projectPath ||
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
  projectPath: string,
  path: string,
  isDirectory: boolean
) {
  const tab = currentSession.value.tabs.find(
    (candidate) =>
      candidate.kind === "file" &&
      candidate.projectPath === projectPath &&
      Boolean(
        candidate.path &&
          entryContainsPath(path, candidate.path, isDirectory)
      )
  );
  if (!tab) return;
  tab.title = "";
  tab.titleKey = "Files";
  tab.path = undefined;
  tab.diff = undefined;
}

function removeSession(sessionId: string) {
  delete sessions[sessionKey(sessionId)];
}

export function useWorkbar() {
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
    openFiles,
    openFile,
    openBackgroundCommand,
    selectTab,
    setTabTitle,
    closeTab,
    closeFileTabs,
    renameEntryTabs,
    resetDeletedEntryTab,
    removeSession,
  };
}
