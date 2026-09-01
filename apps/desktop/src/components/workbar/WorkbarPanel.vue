<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import {
  FileIcon,
  FolderIcon,
  GlobeIcon,
  PanelRightCloseIcon,
  PlusIcon,
  TerminalIcon,
  XIcon,
  type LucideIcon,
} from "@lucide/vue";
import WindowControls from "@/components/WindowControls.vue";
import { usePlatform } from "@/composables/usePlatform";
import type { ChatMessage } from "@/lib/api";
import {
  useWorkbar,
  type WorkbarLaunchKind,
  type WorkbarTab,
  type WorkbarTabKind,
} from "@/composables/useWorkbar";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import ProjectFilesPanel from "./ProjectFilesPanel.vue";
import TerminalPanel from "./TerminalPanel.vue";
import BackgroundCommandTerminalPanel from "./BackgroundCommandTerminalPanel.vue";
import BrowserPanel from "./BrowserPanel.vue";

const props = defineProps<{
  sessionId?: string;
  projectPath?: string;
  messages: ChatMessage[];
  obscured?: boolean;
  ensureSession: () => Promise<string>;
}>();

const { showCustomWindowControls } = usePlatform();
const {
  items,
  tabs,
  width,
  open,
  activeTabId,
  activeTab,
  minWidth,
  setWidth,
  setOpen,
  addTab,
  openFiles,
  openFile,
  selectTab,
  setTabTitle,
  closeTab,
  closeFileTabs,
  renameEntryTabs,
  resetDeletedEntryTab,
} = useWorkbar();
const resizing = ref(false);
const addMenuOpen = ref(false);
const panel = ref<HTMLElement | null>(null);
const CHAT_MIN_WIDTH = 350;
const CLOSE_VELOCITY_PX_PER_MS = 0.8;
const VELOCITY_WINDOW_MS = 120;

const panelStyle = computed(() => ({
  width: `${width.value}px`,
  maxWidth: `max(0px, calc(100% - ${CHAT_MIN_WIDTH}px))`,
}));
const launcherVisible = computed(() => !activeTab.value);

const icons: Record<WorkbarTabKind, LucideIcon> = {
  file: FileIcon,
  terminal: TerminalIcon,
  browser: GlobeIcon,
  "background-command": TerminalIcon,
};
const launcherItems = computed<
  Array<{
    kind: "files" | WorkbarLaunchKind;
    title: string;
    icon: LucideIcon;
  }>
>(() => [
  ...(props.projectPath
    ? [{ kind: "files" as const, title: "文件", icon: FolderIcon }]
    : []),
  { kind: "browser", title: "浏览器", icon: GlobeIcon },
  { kind: "terminal", title: "终端", icon: TerminalIcon },
]);

function addFeature(kind: WorkbarLaunchKind) {
  addTab(kind);
  addMenuOpen.value = false;
}

function launchFeature(kind: "files" | WorkbarLaunchKind) {
  if (kind === "files") {
    if (props.projectPath) openFiles(props.projectPath);
    return;
  }
  addFeature(kind);
}

function selectFile(path: string) {
  if (props.projectPath) {
    openFile(props.projectPath, path);
  }
}

function setFileMode(mode: "file" | "diff") {
  if (activeTab.value?.kind === "file") activeTab.value.view = mode;
}

function activateTab(tab: WorkbarTab) {
  selectTab(tab.id);
}

function closeWorkbarTab(tab: WorkbarTab) {
  closeTab(tab.id);
}

function entryRenamed(oldPath: string, newPath: string, isDirectory: boolean) {
  if (props.projectPath) {
    renameEntryTabs(props.projectPath, oldPath, newPath, isDirectory);
  }
}

function entryDeleted(path: string, isDirectory: boolean) {
  if (props.projectPath) {
    resetDeletedEntryTab(props.projectPath, path, isDirectory);
  }
}

function closeWorkbar() {
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

watch(
  () => props.projectPath,
  (projectPath, previous) => {
    if (projectPath !== previous) closeFileTabs();
  }
);
</script>

<template>
  <aside
    ref="panel"
    :class="[
      'relative flex min-h-0 shrink-0 border-l border-border bg-background',
      !resizing && 'transition-[width] duration-150',
    ]"
    :style="panelStyle"
    aria-label="工作台"
  >
    <button
      type="button"
      class="absolute -left-1 top-0 z-20 h-full w-2 cursor-col-resize touch-none"
      aria-label="调整工作台宽度"
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
              class="flex min-w-0 flex-1 items-center gap-2 pl-2.5 pr-1 text-sm"
              :title="tab.title"
              @click="activateTab(tab)"
            >
              <component :is="icons[tab.kind]" class="size-3.5 shrink-0" />
              <span class="truncate">{{ tab.title }}</span>
            </button>
            <button
              type="button"
              class="mr-1 flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-background/80 hover:text-foreground"
              :title="`关闭${tab.title}`"
              :aria-label="`关闭${tab.title}`"
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
                title="新建标签页"
                aria-label="新建标签页"
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
                v-for="item in items"
                :key="item.kind"
                type="button"
                size="sm"
                variant="ghost"
                class="w-full justify-start gap-2 px-2 font-normal"
                @click="addFeature(item.kind)"
              >
                <component
                  :is="icons[item.kind]"
                  class="size-3.5 shrink-0 text-muted-foreground"
                />
                <span>{{ item.title }}</span>
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
            title="关闭右侧工作区"
            aria-label="关闭右侧工作区"
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
              <span>{{ item.title }}</span>
            </Button>
          </div>
        </div>

        <ProjectFilesPanel
          v-show="
            !launcherVisible &&
            activeTab?.kind === 'file'
          "
          class="absolute inset-0"
          :project-path="projectPath"
          :selected-path="
            activeTab?.kind === 'file' && activeTab.projectPath === projectPath
              ? activeTab.path
              : undefined
          "
          :selected-mode="
            activeTab?.kind === 'file' && activeTab.projectPath === projectPath
              ? activeTab.view
              : undefined
          "
          :diff="
            activeTab?.kind === 'file' && activeTab.projectPath === projectPath
              ? activeTab.diff
              : undefined
          "
          :messages="messages"
          @select="selectFile"
          @update:selected-mode="setFileMode"
          @entry-renamed="entryRenamed"
          @entry-deleted="entryDeleted"
        />

        <template v-for="tab in tabs" :key="tab.id">
          <TerminalPanel
            v-if="tab.kind === 'terminal'"
            v-show="activeTabId === tab.id"
            class="absolute inset-0"
            :session-id="sessionId"
            :active="open && activeTabId === tab.id"
            :ensure-session="ensureSession"
          />
          <BackgroundCommandTerminalPanel
            v-else-if="
              tab.kind === 'background-command' &&
              tab.sessionId &&
              tab.commandId
            "
            v-show="activeTabId === tab.id"
            class="absolute inset-0"
            :session-id="tab.sessionId"
            :command-id="tab.commandId"
            :active="open && activeTabId === tab.id"
          />
          <BrowserPanel
            v-else-if="tab.kind === 'browser'"
            v-show="activeTabId === tab.id"
            class="absolute inset-0"
            :browser-id="tab.id"
            :active="open && activeTabId === tab.id"
            :obscured="obscured || addMenuOpen"
            @title-change="setTabTitle(tab.id, $event)"
          />
        </template>
      </div>
    </div>
  </aside>
</template>
