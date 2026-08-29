import { computed, ref } from "vue";

export type WorkbarLaunchKind = "terminal" | "browser";
export type WorkbarTabKind = "file" | WorkbarLaunchKind;

export interface WorkbarItem {
  kind: WorkbarLaunchKind;
  title: string;
}

export interface WorkbarTab {
  id: string;
  kind: WorkbarTabKind;
  title: string;
  path?: string;
  workspace?: string;
}

const MIN_WIDTH = 320;
const MAX_WIDTH = 960;
const DEFAULT_WIDTH = 720;
const WIDTH_KEY = "foya-workbar-width-v1";

const items: WorkbarItem[] = [
  { kind: "terminal", title: "终端" },
  { kind: "browser", title: "浏览器" },
];

function storedWidth(): number {
  const value = Number(localStorage.getItem(WIDTH_KEY));
  if (!Number.isFinite(value)) return DEFAULT_WIDTH;
  return Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(value)));
}

const width = ref(storedWidth());
const open = ref(false);
const tabs = ref<WorkbarTab[]>([]);
const activeTabId = ref<string | null>(null);
const nextInstance = {
  terminal: 0,
  browser: 0,
};

function setWidth(value: number, persist = true) {
  width.value = Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(value)));
  if (persist) localStorage.setItem(WIDTH_KEY, String(width.value));
}

function setOpen(value: boolean) {
  open.value = value;
}

function toggle() {
  setOpen(!open.value);
}

function addTab(kind: WorkbarLaunchKind) {
  const item = items.find((candidate) => candidate.kind === kind);
  if (!item) return;
  const instance = ++nextInstance[kind];
  const tab: WorkbarTab = {
    ...item,
    id: `${kind}-${instance}`,
    title:
      kind === "browser"
        ? "新标签页"
        : instance === 1
          ? item.title
          : `${item.title} ${instance}`,
  };
  tabs.value.push(tab);
  activeTabId.value = tab.id;
  setOpen(true);
}

function openFiles(workspace: string) {
  const id = `file:${workspace}`;
  const existing = tabs.value.find((tab) => tab.id === id);
  if (existing) {
    activeTabId.value = existing.id;
    return;
  }
  tabs.value.push({
    id,
    kind: "file",
    title: "文件",
    workspace,
  });
  activeTabId.value = id;
  setOpen(true);
}

function openFile(workspace: string, path: string) {
  const id = `file:${workspace}`;
  const title = path.split("/").pop() || path;
  const existing = tabs.value.find((tab) => tab.id === id);
  if (existing) {
    existing.title = title;
    existing.path = path;
  } else {
    tabs.value.push({ id, kind: "file", title, path, workspace });
  }
  activeTabId.value = id;
}

function selectTab(tabId: string) {
  if (tabs.value.some((tab) => tab.id === tabId)) {
    activeTabId.value = tabId;
  }
}

function setTabTitle(tabId: string, title: string) {
  const tab = tabs.value.find(
    (candidate) => candidate.id === tabId && candidate.kind === "browser"
  );
  if (!tab) return;
  const normalized = title.replace(/\s+/g, " ").trim().slice(0, 80);
  tab.title = normalized || "新标签页";
}

function closeTab(tabId: string) {
  const index = tabs.value.findIndex((tab) => tab.id === tabId);
  if (index < 0) return;
  tabs.value.splice(index, 1);
  if (activeTabId.value !== tabId) return;
  activeTabId.value =
    tabs.value[index]?.id ?? tabs.value[index - 1]?.id ?? null;
}

function closeFileTabs() {
  const fileIds = new Set(
    tabs.value.filter((tab) => tab.kind === "file").map((tab) => tab.id)
  );
  tabs.value = tabs.value.filter((tab) => tab.kind !== "file");
  if (activeTabId.value && fileIds.has(activeTabId.value)) {
    activeTabId.value = tabs.value[0]?.id ?? null;
  }
}

function entryContainsPath(entryPath: string, filePath: string, isDirectory: boolean) {
  return filePath === entryPath || (isDirectory && filePath.startsWith(`${entryPath}/`));
}

function renameEntryTabs(
  workspace: string,
  oldPath: string,
  newPath: string,
  isDirectory: boolean
) {
  tabs.value = tabs.value.map((tab) => {
    if (
      tab.kind !== "file" ||
      tab.workspace !== workspace ||
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
    };
  });
}

function resetDeletedEntryTab(
  workspace: string,
  path: string,
  isDirectory: boolean
) {
  const tab = tabs.value.find(
    (candidate) =>
      candidate.kind === "file" &&
      candidate.workspace === workspace &&
      Boolean(
        candidate.path &&
          entryContainsPath(path, candidate.path, isDirectory)
      )
  );
  if (!tab) return;
  tab.title = "文件";
  tab.path = undefined;
}

export function useWorkbar() {
  return {
    items,
    tabs,
    width,
    open,
    activeTabId,
    activeTab: computed(() =>
      tabs.value.find((tab) => tab.id === activeTabId.value)
    ),
    minWidth: MIN_WIDTH,
    maxWidth: MAX_WIDTH,
    setWidth,
    setOpen,
    toggle,
    addTab,
    openFiles,
    openFile,
    selectTab,
    setTabTitle,
    closeTab,
    closeFileTabs,
    renameEntryTabs,
    resetDeletedEntryTab,
  };
}
