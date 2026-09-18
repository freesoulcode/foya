<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { storeToRefs } from "pinia";
import { useI18n } from "vue-i18n";
import { openUrl } from "@tauri-apps/plugin-opener";
import {
  FileIcon,
  FolderIcon,
  GlobeIcon,
  MessageSquarePlusIcon,
  PanelRightCloseIcon,
  PlusIcon,
  TerminalIcon,
  XIcon,
  type LucideIcon,
} from "@lucide/vue";
import WindowControls from "@/components/WindowControls.vue";
import { useLinkPreference } from "@/composables/useLinkPreference";
import { usePlatform } from "@/composables/usePlatform";
import { diffFilePath } from "@/lib/diff";
import type {
  AttachmentRef,
  BrowserActionRequest,
  BrowserActionResult,
  BrowserElementSelection,
  BackgroundCommand,
  ChatMessage,
  SessionWorkspace,
} from "@/lib/api";
import {
  useWorkbarStore,
  type WorkbarLaunchKind,
  type WorkbarTab,
  type WorkbarTabKind,
} from "@/stores/workbar";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import WorkspaceFilesPanel from "./ProjectFilesPanel.vue";
import TerminalPanel from "./TerminalPanel.vue";
import BackgroundCommandTerminalPanel from "./BackgroundCommandTerminalPanel.vue";
import BrowserPanel from "./BrowserPanel.vue";
import ArtifactPreviewPanel from "./ArtifactPreviewPanel.vue";
import SideChatPanel from "./SideChatPanel.vue";

const props = defineProps<{
  sessionId?: string;
  workspace?: SessionWorkspace | null;
  messages: ChatMessage[];
  backgroundCommands?: BackgroundCommand[];
  browserActions?: BrowserActionRequest[];
  obscured?: boolean;
  visible?: boolean;
  ensureSession: () => Promise<string>;
  ensureWorkspace: () => Promise<SessionWorkspace>;
}>();

const emit = defineEmits<{
  (event: "browser-element-selected", element: BrowserElementSelection): void;
  (event: "project-files-changed", paths: string[]): void;
  (event: "create-side-chat", sessionId?: string, throughSeq?: number): void;
  (event: "close-side-chat", sessionId: string): void;
  (event: "add-project", sessionId?: string): void;
  (event: "open-plugins"): void;
  (
    event: "browser-action-result",
    request: BrowserActionRequest,
    result: BrowserActionResult
  ): void;
}>();

const { showCustomWindowControls } = usePlatform();
const { linkOpenMode } = useLinkPreference();
const { t } = useI18n();
const workbarStore = useWorkbarStore();
const {
  tabs,
  allTabs,
  width,
  open,
  activeTabId,
  activeTab,
} = storeToRefs(workbarStore);
const {
  minWidth,
  setWidth,
  setOpen,
  addTab,
  openFiles,
  openFile,
  openBrowser,
  openArtifact,
  openBackgroundCommand,
  selectTab,
  setTabTitle,
  closeTab,
  renameEntryTabs,
  resetDeletedEntryTab,
  ownerSessionForContentSession,
} = workbarStore;
const resizing = ref(false);
const addMenuOpen = ref(false);
const focused = ref(false);
const panel = ref<HTMLElement | null>(null);
const sideChatBrowserElements = ref<Record<string, BrowserElementSelection[]>>({});
const CHAT_MIN_WIDTH = 350;
const CLOSE_VELOCITY_PX_PER_MS = 0.8;
const VELOCITY_WINDOW_MS = 120;

const panelStyle = computed(() => ({
  width: focused.value ? "100%" : width.value + "px",
  maxWidth: focused.value ? "none" : "max(0px, calc(100% - " + CHAT_MIN_WIDTH + "px))",
}));
const launcherVisible = computed(() => !activeTab.value);
const browserTabs = computed(() =>
  allTabs.value.filter((tab) => tab.kind === "browser")
);
const terminalTabs = computed(() =>
  allTabs.value.filter((tab) => tab.kind === "terminal")
);
const backgroundCommandTabs = computed(() =>
  allTabs.value.filter((tab) => tab.kind === "background-command")
);
const artifactTabs = computed(() =>
  allTabs.value.filter((tab) => tab.kind === "artifact")
);
const activeFileWorkspacePath = computed(() =>
  activeTab.value?.kind === "file"
    ? activeTab.value.workspacePath ?? ""
    : props.workspace?.path ?? ""
);
const workspaceDisplayName = computed(() =>
  props.workspace?.kind === "managed"
    ? t("Workspace")
    : props.workspace?.name ?? ""
);
const sideChatTabs = computed(() =>
  allTabs.value.filter((tab) => tab.kind === "side-chat")
);
const sideChatSessionIds = computed(
  () =>
    new Set(
      sideChatTabs.value
        .map((tab) => tab.sessionId)
        .filter((id): id is string => Boolean(id)),
    ),
);

function isCurrentSessionTab(tab: WorkbarTab) {
  return tabs.value.includes(tab);
}

function isActiveWorkbarTab(tab: WorkbarTab) {
  return (
    props.visible &&
    open.value &&
    isCurrentSessionTab(tab) &&
    activeTabId.value === tab.id
  );
}

const icons: Record<WorkbarTabKind, LucideIcon> = {
  file: FileIcon,
  artifact: FileIcon,
  terminal: TerminalIcon,
  browser: GlobeIcon,
  "background-command": TerminalIcon,
  "side-chat": MessageSquarePlusIcon,
};
const launcherItems = computed<
  Array<{
    kind: "files" | "side-chat" | WorkbarLaunchKind;
    title: string;
    icon: LucideIcon;
  }>
>(() => [
  { kind: "files", title: "Files", icon: FolderIcon },
  ...(props.sessionId
    ? [
        {
          kind: "side-chat" as const,
          title: "Side chat",
          icon: MessageSquarePlusIcon,
        },
      ]
    : []),
  { kind: "browser", title: "Browser", icon: GlobeIcon },
  { kind: "terminal", title: "Terminal", icon: TerminalIcon },
]);
const newTabItems = computed<
  Array<{
    kind: "side-chat" | WorkbarLaunchKind;
    title: string;
    icon: LucideIcon;
  }>
>(() => [
  ...(props.sessionId
    ? [
        {
          kind: "side-chat" as const,
          title: "Side chat",
          icon: MessageSquarePlusIcon,
        },
      ]
    : []),
  { kind: "browser", title: "Browser", icon: GlobeIcon },
  { kind: "terminal", title: "Terminal", icon: TerminalIcon },
]);

function tabTitle(tab: WorkbarTab): string {
  return tab.titleKey
    ? t(tab.titleKey, { number: tab.titleNumber ?? "" })
    : tab.title;
}

function browserElementKey(element: BrowserElementSelection) {
  return `${element.page_url}\n${element.selector}`;
}

function browserElementsForSession(sessionId?: string) {
  return sessionId ? sideChatBrowserElements.value[sessionId] ?? [] : [];
}

function setBrowserElementsForSession(
  sessionId: string,
  elements: BrowserElementSelection[],
) {
  sideChatBrowserElements.value = {
    ...sideChatBrowserElements.value,
    [sessionId]: elements,
  };
}

function addBrowserElementForSession(
  sessionId: string,
  element: BrowserElementSelection,
) {
  const current = browserElementsForSession(sessionId);
  const key = browserElementKey(element);
  if (
    current.some((item) => browserElementKey(item) === key) ||
    current.length >= 8
  ) {
    return;
  }
  setBrowserElementsForSession(sessionId, [...current, element]);
}

function removeBrowserElementForSession(sessionId: string, index: number) {
  setBrowserElementsForSession(
    sessionId,
    browserElementsForSession(sessionId).filter(
      (_, itemIndex) => itemIndex !== index,
    ),
  );
}

function clearBrowserElementsForSession(sessionId: string) {
  setBrowserElementsForSession(sessionId, []);
}

function isSideChatSession(sessionId?: string) {
  return Boolean(sessionId && sideChatSessionIds.value.has(sessionId));
}

function ownerSessionForSideChat(tab: WorkbarTab) {
  if (!tab.sessionId) return props.sessionId ?? "";
  return ownerSessionForContentSession(tab.sessionId) ?? props.sessionId ?? "";
}

function handleBrowserElementSelected(
  tab: WorkbarTab,
  element: BrowserElementSelection,
) {
  if (tab.sessionId && isSideChatSession(tab.sessionId)) {
    addBrowserElementForSession(tab.sessionId, element);
    return;
  }
  emit("browser-element-selected", element);
}

function openSideChatFile(
  projectPath: string,
  path: string,
  view: "file" | "diff" = "file",
  diff?: string,
) {
  if (!projectPath || path.startsWith("/")) return;
  openFile(projectPath, path, view, diff);
}

function openSideChatDiff(projectPath: string, diff: string) {
  if (!projectPath) return;
  const path = diffFilePath(diff, projectPath);
  if (path) openFile(projectPath, path, "diff", diff);
}

function openSideChatWorkflowFile(projectPath: string, path: string) {
  const root = projectPath.replace(/\\/g, "/").replace(/\/+$/, "");
  const target = path.replace(/\\/g, "/");
  if (!root || !target.startsWith(`${root}/`)) return;
  openFile(projectPath, target.slice(root.length + 1));
}

function openSideChatArtifact(tab: WorkbarTab, attachment: AttachmentRef) {
  if (!tab.sessionId) return;
  openArtifact(tab.sessionId, attachment, ownerSessionForSideChat(tab));
}

function openSideChatBackgroundCommand(
  tab: WorkbarTab,
  command: BackgroundCommand,
) {
  openBackgroundCommand(
    command.session_id,
    command.command_id,
    command.command,
    ownerSessionForSideChat(tab),
  );
}

async function openSideChatLink(tab: WorkbarTab, url: string) {
  if (linkOpenMode.value === "system") {
    try {
      await openUrl(url);
    } catch (error) {
      console.error("Failed to open link in system browser:", error);
    }
    return;
  }
  openBrowser(url, tab.sessionId, ownerSessionForSideChat(tab));
}

async function addFeature(kind: WorkbarLaunchKind) {
  addTab(kind);
  addMenuOpen.value = false;
}

async function launchFeature(kind: "files" | "side-chat" | WorkbarLaunchKind) {
  if (kind === "files") {
    const workspace = props.workspace ?? await props.ensureWorkspace();
    openFiles(workspace.path);
    return;
  }
  if (kind === "side-chat") {
    emit("create-side-chat");
    addMenuOpen.value = false;
    return;
  }
  await addFeature(kind);
}

function selectFile(path: string) {
  if (activeFileWorkspacePath.value) {
    openFile(activeFileWorkspacePath.value, path);
  }
}

function setFileMode(mode: "file" | "diff") {
  if (activeTab.value?.kind === "file") activeTab.value.view = mode;
}

function activateTab(tab: WorkbarTab) {
  selectTab(tab.id);
}

function closeWorkbarTab(tab: WorkbarTab) {
  const removed = closeTab(tab.id);
  if (removed?.kind === "side-chat" && removed.sessionId) {
    const next = { ...sideChatBrowserElements.value };
    delete next[removed.sessionId];
    sideChatBrowserElements.value = next;
    emit("close-side-chat", removed.sessionId);
  }
}

watch(
  sideChatSessionIds,
  (ids) => {
    const next = { ...sideChatBrowserElements.value };
    for (const id of Object.keys(next)) {
      if (!ids.has(id)) delete next[id];
    }
    sideChatBrowserElements.value = next;
  },
  { immediate: true },
);

function entryRenamed(oldPath: string, newPath: string, isDirectory: boolean) {
  if (activeFileWorkspacePath.value) {
    renameEntryTabs(activeFileWorkspacePath.value, oldPath, newPath, isDirectory);
  }
}

function entryDeleted(path: string, isDirectory: boolean) {
  if (activeFileWorkspacePath.value) {
    resetDeletedEntryTab(activeFileWorkspacePath.value, path, isDirectory);
  }
}

function closeWorkbar() {
	focused.value = false;
  addMenuOpen.value = false;
  setOpen(false);
}

let stopResize: (() => void) | null = null;

function startResize(event: PointerEvent) {
  event.preventDefault();
  const startX = event.clientX;
  const startWidth = panel.value?.getBoundingClientRect().width ?? width.value;
  const pointerSamples = [{ x: event.clientX, time: event.timeStamp }];
  resizing.value = true;

  const move = (next: PointerEvent) => {
    pointerSamples.push({ x: next.clientX, time: next.timeStamp });
    const cutoff = next.timeStamp - VELOCITY_WINDOW_MS;
    while (
      pointerSamples.length > 2 &&
      pointerSamples[1].time < cutoff
    ) {
      pointerSamples.shift();
    }

    const firstSample = pointerSamples[0];
    const elapsed = next.timeStamp - firstSample.time;
    const velocity =
      elapsed > 0 ? (next.clientX - firstSample.x) / elapsed : 0;
    const requestedWidth = startWidth + startX - next.clientX;
    if (
      requestedWidth <= minWidth &&
      velocity >= CLOSE_VELOCITY_PX_PER_MS
    ) {
      closeWorkbar();
      finish();
      return;
    }

    const parentWidth =
      panel.value?.parentElement?.getBoundingClientRect().width ?? Infinity;
    const availableWidth = Math.max(0, parentWidth - CHAT_MIN_WIDTH);
    setWidth(Math.min(requestedWidth, availableWidth), false);
  };
  const finish = () => {
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", finish);
    window.removeEventListener("pointercancel", finish);
    window.removeEventListener("blur", finish);
    document.body.style.cursor = "";
    document.body.style.userSelect = "";
    resizing.value = false;
    setWidth(width.value);
    stopResize = null;
  };

  document.body.style.cursor = "col-resize";
  document.body.style.userSelect = "none";
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", finish, { once: true });
  window.addEventListener("pointercancel", finish, { once: true });
  window.addEventListener("blur", finish, { once: true });
  stopResize = finish;
}

onBeforeUnmount(() => stopResize?.());
</script>

<template>
  <aside
    ref="panel"
    :class="[
      focused ? 'absolute inset-0 z-40 flex min-h-0 border-l border-border bg-background' : 'relative flex min-h-0 shrink-0 border-l border-border bg-background',
      !resizing && 'transition-[width] duration-150',
    ]"
    :style="panelStyle"
    :aria-label="$t('Workspace')"
  >
    <button
      type="button"
      class="no-drag absolute -left-1 top-0 z-20 h-full w-2 cursor-col-resize touch-none"
      :aria-label="$t('Resize workspace')"
      @pointerdown="startResize"
    >
      <span class="absolute left-1/2 top-1/2 h-10 w-0.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-border opacity-0 transition-opacity hover:opacity-100" />
    </button>

    <div class="flex min-w-0 flex-1 flex-col">
      <div
        data-tauri-drag-region
        class="flex h-11 shrink-0 items-center"
      >
        <div
          data-tauri-drag-region
          class="no-scrollbar flex min-w-0 flex-1 items-center gap-1 overflow-x-auto px-2"
        >
          <div
            v-for="tab in tabs"
            :key="tab.id"
            :class="[
              'no-drag flex h-8 min-w-28 max-w-44 shrink-0 items-center rounded-lg transition-colors',
              activeTabId === tab.id
                ? 'bg-muted text-foreground'
                : 'text-muted-foreground hover:bg-muted hover:text-foreground',
            ]"
          >
            <button
              type="button"
              class="no-drag flex min-w-0 flex-1 items-center gap-2 pl-2.5 pr-1 text-sm"
              :title="tabTitle(tab)"
              @click="activateTab(tab)"
            >
              <component :is="icons[tab.kind]" class="size-3.5 shrink-0" />
              <span class="truncate">{{ tabTitle(tab) }}</span>
            </button>
            <button
              type="button"
              class="no-drag mr-1 flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-background/80 hover:text-foreground"
              :title="$t('Close {name}', { name: tabTitle(tab) })"
              :aria-label="$t('Close {name}', { name: tabTitle(tab) })"
              @click.stop="closeWorkbarTab(tab)"
            >
              <XIcon class="size-3.5" />
            </button>
          </div>

          <Popover v-if="!launcherVisible" v-model:open="addMenuOpen">
            <PopoverTrigger as-child>
              <Button
                type="button"
                size="icon"
                variant="ghost"
                class="no-drag size-8 shrink-0 rounded-lg"
                :title="$t('New tab')"
                :aria-label="$t('New tab')"
              >
                <PlusIcon class="size-4" />
              </Button>
            </PopoverTrigger>
            <PopoverContent
              align="start"
              side="bottom"
              :side-offset="6"
              class="w-40 gap-0.5 rounded-md p-1 shadow-md"
            >
              <Button
                v-for="item in newTabItems"
                :key="item.kind"
                type="button"
                size="sm"
                variant="ghost"
                class="w-full justify-start gap-2 px-2 font-normal"
                @click="launchFeature(item.kind)"
              >
                <component
                  :is="item.icon"
                  class="size-3.5 shrink-0 text-muted-foreground"
                />
                <span>{{ $t(item.title) }}</span>
              </Button>
            </PopoverContent>
          </Popover>
        </div>

        <div class="no-drag flex h-full items-center">
          <Button
            type="button"
            size="icon"
            variant="ghost"
            class="mr-1 size-8 shrink-0 rounded-lg"
            :title="$t('Close side workspace')"
            :aria-label="$t('Close side workspace')"
            @click="closeWorkbar"
          >
            <PanelRightCloseIcon class="size-4" />
          </Button>
          <WindowControls v-if="showCustomWindowControls" />
        </div>
      </div>

      <div class="relative min-h-0 flex-1">
        <div
          v-if="launcherVisible"
          class="absolute inset-0 flex items-center justify-center px-6 pb-8"
        >
          <div class="w-full max-w-56 space-y-0.5">
            <Button
              v-for="item in launcherItems"
              :key="item.kind"
              type="button"
              size="sm"
              variant="ghost"
              class="w-full justify-start gap-2.5 px-2.5 font-normal"
              @click="launchFeature(item.kind)"
            >
              <component
                :is="item.icon"
                class="size-4 shrink-0 text-muted-foreground"
              />
              <span>{{ $t(item.title) }}</span>
            </Button>
          </div>
        </div>

        <WorkspaceFilesPanel
          v-show="
            !launcherVisible &&
            activeTab?.kind === 'file'
          "
          class="absolute inset-0"
          :session-id="sessionId"
          :workspace-path="activeFileWorkspacePath"
          :workspace-name="workspaceDisplayName"
          :selected-path="
            activeTab?.kind === 'file' && activeTab.workspacePath === activeFileWorkspacePath
              ? activeTab.path
              : undefined
          "
          :selected-mode="
            activeTab?.kind === 'file' && activeTab.workspacePath === activeFileWorkspacePath
              ? activeTab.view
              : undefined
          "
          :diff="
            activeTab?.kind === 'file' && activeTab.workspacePath === activeFileWorkspacePath
              ? activeTab.diff
              : undefined
          "
          :messages="messages"
          @select="selectFile"
          @update:selected-mode="setFileMode"
          @entry-renamed="entryRenamed"
          @entry-deleted="entryDeleted"
          @files-changed="emit('project-files-changed', $event)"
        />

        <TerminalPanel
          v-for="tab in terminalTabs"
          :key="tab.id"
          v-show="isActiveWorkbarTab(tab)"
          class="absolute inset-0"
          :session-id="tab.sessionId"
          :active="isActiveWorkbarTab(tab)"
          :ensure-session="ensureSession"
        />
        <template v-for="tab in backgroundCommandTabs" :key="tab.id">
          <BackgroundCommandTerminalPanel
            v-if="tab.sessionId && tab.commandId"
            v-show="isActiveWorkbarTab(tab)"
            class="absolute inset-0"
            :session-id="tab.sessionId"
            :command-id="tab.commandId"
            :command="
              isCurrentSessionTab(tab)
                ? backgroundCommands?.find(
                    (command) => command.command_id === tab.commandId
                  )
                : undefined
            "
            :active="isActiveWorkbarTab(tab)"
          />
        </template>
        <template v-for="tab in artifactTabs" :key="tab.id">
          <ArtifactPreviewPanel
            v-if="tab.sessionId && tab.artifact"
            v-show="isActiveWorkbarTab(tab)"
            class="absolute inset-0"
            :session-id="tab.sessionId"
            :attachment="tab.artifact"
            :active="isActiveWorkbarTab(tab)"
          />
        </template>
        <template v-for="tab in sideChatTabs" :key="tab.id">
          <SideChatPanel
            v-if="tab.sessionId"
            v-show="isActiveWorkbarTab(tab)"
            class="absolute inset-0"
            :session-id="tab.sessionId"
            :owner-session-id="ownerSessionForSideChat(tab)"
            :active="isActiveWorkbarTab(tab)"
            :browser-elements="browserElementsForSession(tab.sessionId)"
            @open-diff="openSideChatDiff"
            @open-review-file="openSideChatFile"
            @open-workflow-file="openSideChatWorkflowFile"
            @open-artifact="openSideChatArtifact(tab, $event)"
            @open-link="openSideChatLink(tab, $event)"
            @open-background-command="openSideChatBackgroundCommand(tab, $event)"
            @add-project="emit('add-project', tab.sessionId)"
            @open-plugins="emit('open-plugins')"
            @remove-browser-element="
              removeBrowserElementForSession(tab.sessionId, $event)
            "
            @clear-browser-elements="clearBrowserElementsForSession(tab.sessionId)"
            @restore-browser-elements="
              setBrowserElementsForSession(tab.sessionId, $event)
            "
            @create-side-chat="
              (sessionId, throughSeq) =>
                emit('create-side-chat', sessionId, throughSeq)
            "
          />
        </template>
        <BrowserPanel
          v-for="tab in browserTabs"
          :key="tab.id"
          v-show="isActiveWorkbarTab(tab)"
          class="absolute inset-0"
          :browser-id="tab.id"
          :initial-url="tab.url"
          :active="isActiveWorkbarTab(tab)"
          :obscured="obscured || addMenuOpen"
          :agent-action="
            browserActions?.find((action) => action.browser_id === tab.id)
          "
          @title-change="setTabTitle(tab.id, $event)"
          @element-selected="handleBrowserElementSelected(tab, $event)"
          @agent-action-result="
            (request, result) =>
              emit('browser-action-result', request, result)
          "
        />
      </div>
    </div>
  </aside>
</template>
