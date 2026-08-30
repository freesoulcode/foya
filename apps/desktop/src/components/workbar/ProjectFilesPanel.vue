<script setup lang="ts">
import {
  computed,
  defineAsyncComponent,
  onBeforeUnmount,
  ref,
  watch,
} from "vue";
import {
  AlertCircleIcon,
  ChevronDownIcon,
  ChevronRightIcon,
  CopyIcon,
  FileCodeIcon,
  FileIcon,
  FilePlus2Icon,
  FolderIcon,
  FolderOpenIcon,
  FolderPlusIcon,
  FolderTreeIcon,
  PencilIcon,
  RefreshCwIcon,
  SearchIcon,
  Trash2Icon,
} from "@lucide/vue";
import { revealItemInDir } from "@tauri-apps/plugin-opener";
import {
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuPortal,
  ContextMenuRoot,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "reka-ui";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type {
  ChatMessage,
  ProjectEntry,
  ToolCallView,
} from "@/lib/api";
import { api } from "@/lib/api";
import {
  diffFilePath,
  diffStats,
  fullDiffLines,
  type FullDiffLine,
} from "@/lib/diff";
import { renderMarkdown } from "@/lib/markdown";

const CodePreview = defineAsyncComponent(() => import("./CodePreview.vue"));

const props = defineProps<{
  projectPath?: string;
  selectedPath?: string;
  selectedMode?: "file" | "diff";
  diff?: string;
  messages: ChatMessage[];
}>();

const emit = defineEmits<{
  (event: "select", path: string): void;
  (event: "update:selected-mode", mode: "file" | "diff"): void;
  (
    event: "entry-renamed",
    oldPath: string,
    newPath: string,
    isDirectory: boolean
  ): void;
  (event: "entry-deleted", path: string, isDirectory: boolean): void;
}>();

interface FileDiff {
  raw: string;
  additions: number;
  deletions: number;
}

type EditKind = "create-file" | "create-directory" | "rename";

const TREE_MIN_WIDTH = 160;
const TREE_MAX_WIDTH = 480;
const PREVIEW_MIN_WIDTH = 240;
const TREE_DEFAULT_WIDTH = 224;
const TREE_WIDTH_KEY = "foya-project-tree-width-v1";

function storedTreeWidth(): number {
  const value = Number(localStorage.getItem(TREE_WIDTH_KEY));
  if (!Number.isFinite(value)) return TREE_DEFAULT_WIDTH;
  return Math.min(TREE_MAX_WIDTH, Math.max(TREE_MIN_WIDTH, Math.round(value)));
}

const panelRoot = ref<HTMLElement | null>(null);
const entries = ref<ProjectEntry[]>([]);
const collapsed = ref<Set<string>>(new Set());
const treeOpen = ref(true);
const treeWidth = ref(storedTreeWidth());
const treeResizing = ref(false);
const treeSelection = ref("");
const query = ref("");
const content = ref("");
const loadingTree = ref(false);
const loadingFile = ref(false);
const treeError = ref("");
const fileError = ref("");
const previewMode = ref<"file" | "diff">("file");
const inlineActionsPath = ref("");
const editOpen = ref(false);
const editKind = ref<EditKind>("create-file");
const editTarget = ref<ProjectEntry>();
const editDirectory = ref("");
const editValue = ref("");
const editError = ref("");
const deleteOpen = ref(false);
const deleteTarget = ref<ProjectEntry>();
const deleteError = ref("");
const operating = ref(false);
const notice = ref("");
let noticeTimer: ReturnType<typeof setTimeout> | undefined;
let fileRequest = 0;
let stopTreeResize: (() => void) | null = null;

const menuItemClass =
  "relative flex h-8 cursor-default select-none items-center gap-2 rounded-sm px-2 text-xs outline-none data-[disabled]:pointer-events-none data-[disabled]:opacity-50 data-[highlighted]:bg-accent data-[highlighted]:text-accent-foreground";
const dangerMenuItemClass = `${menuItemClass} text-destructive data-[highlighted]:bg-destructive/10 data-[highlighted]:text-destructive`;

const projectName = computed(() => {
  if (!props.projectPath) return "";
  const parts = props.projectPath.replace(/\/+$/, "").split(/[\\/]/);
  return parts[parts.length - 1] || props.projectPath;
});

const selectedEntry = computed(() =>
  entries.value.find((entry) => entry.path === treeSelection.value)
);

const editTitle = computed(() => {
  if (editKind.value === "create-file") return "新建文件";
  if (editKind.value === "create-directory") return "新建文件夹";
  return "重命名";
});

const editLabel = computed(() =>
  editKind.value === "rename" ? "新名称" : "名称"
);

const editLocation = computed(() => {
  if (editKind.value === "rename") return editTarget.value?.path ?? "";
  return editDirectory.value
    ? `${projectName.value}/${editDirectory.value}`
    : projectName.value;
});

const markdownPreview = computed(() => renderMarkdown(content.value));
const isMarkdown = computed(() =>
  /\.(md|markdown|mdown|mkd)$/i.test(props.selectedPath ?? "")
);

function collectToolCalls(message: ChatMessage): ToolCallView[] {
  if (message.segments?.length) {
    return message.segments
      .filter((segment) => segment.kind === "tool")
      .map((segment) => (segment.kind === "tool" ? segment.tool : null))
      .filter((tool): tool is ToolCallView => Boolean(tool));
  }
  return message.tool_calls ?? [];
}

function diffPath(diff: string): string {
  return diffFilePath(diff, props.projectPath ?? "");
}

function parseDiff(raw: string): FileDiff {
  const stats = diffStats(raw);
  return {
    raw,
    additions: stats.additions,
    deletions: stats.deletions,
  };
}

const diffs = computed(() => {
  const result = new Map<string, FileDiff>();
  for (const message of props.messages) {
    for (const tool of collectToolCalls(message)) {
      if (!tool.diff) continue;
      const path = diffPath(tool.diff);
      if (path) result.set(path, parseDiff(tool.diff));
    }
  }
  return result;
});

const selectedDiff = computed(() => {
  if (props.diff) return parseDiff(props.diff);
  return props.selectedPath ? diffs.value.get(props.selectedPath) : undefined;
});
const selectedDiffLines = computed(() =>
  selectedDiff.value ? fullDiffLines(selectedDiff.value.raw, content.value) : []
);
const diffSignature = computed(() =>
  Array.from(diffs.value.entries())
    .map(([path, diff]) => `${path}:${diff.raw.length}`)
    .join("|")
);

function isUnderCollapsedDirectory(path: string): boolean {
  const parts = path.split("/");
  for (let index = 1; index < parts.length; index += 1) {
    if (collapsed.value.has(parts.slice(0, index).join("/"))) return true;
  }
  return false;
}

const visibleEntries = computed(() => {
  const normalizedQuery = query.value.trim().toLowerCase();
  return entries.value.filter((entry) => {
    if (normalizedQuery) {
      return !entry.is_dir && entry.path.toLowerCase().includes(normalizedQuery);
    }
    return !isUnderCollapsedDirectory(entry.path);
  });
});

function startTreeResize(event: PointerEvent) {
  event.preventDefault();
  const startX = event.clientX;
  const startWidth = treeWidth.value;
  treeResizing.value = true;

  const move = (next: PointerEvent) => {
    const panelWidth = panelRoot.value?.getBoundingClientRect().width ?? 0;
    const available = Math.max(TREE_MIN_WIDTH, panelWidth - PREVIEW_MIN_WIDTH);
    const maxWidth = Math.min(TREE_MAX_WIDTH, available);
    treeWidth.value = Math.min(
      maxWidth,
      Math.max(TREE_MIN_WIDTH, startWidth + startX - next.clientX)
    );
  };
  const finish = () => {
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", finish);
    window.removeEventListener("pointercancel", finish);
    window.removeEventListener("blur", finish);
    document.body.style.cursor = "";
    document.body.style.userSelect = "";
    treeResizing.value = false;
    localStorage.setItem(TREE_WIDTH_KEY, String(Math.round(treeWidth.value)));
    stopTreeResize = null;
  };

  document.body.style.cursor = "col-resize";
  document.body.style.userSelect = "none";
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", finish, { once: true });
  window.addEventListener("pointercancel", finish, { once: true });
  window.addEventListener("blur", finish, { once: true });
  stopTreeResize = finish;
}

function resetTreeWidth() {
  treeWidth.value = TREE_DEFAULT_WIDTH;
  localStorage.setItem(TREE_WIDTH_KEY, String(TREE_DEFAULT_WIDTH));
}

function lineClass(kind: FullDiffLine["kind"]) {
  if (kind === "add") {
    return "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300";
  }
  if (kind === "delete") {
    return "bg-red-500/10 text-red-700 dark:text-red-300";
  }
  return "text-foreground/75";
}

function parentPath(path: string): string {
  const separator = path.lastIndexOf("/");
  return separator < 0 ? "" : path.slice(0, separator);
}

function joinPath(directory: string, name: string): string {
  return directory ? `${directory}/${name}` : name;
}

function directoryForEntry(entry?: ProjectEntry): string {
  if (entry?.is_dir) return entry.path;
  if (entry) return parentPath(entry.path);
  if (props.selectedPath) return parentPath(props.selectedPath);
  return "";
}

function toggleDirectory(path: string) {
  const next = new Set(collapsed.value);
  if (next.has(path)) next.delete(path);
  else next.add(path);
  collapsed.value = next;
}

function selectTreeEntry(entry: ProjectEntry, event: MouseEvent) {
  treeSelection.value = entry.path;
  inlineActionsPath.value = "";
  if (event.detail > 1) return;
  if (entry.is_dir) toggleDirectory(entry.path);
  else emit("select", entry.path);
}

function showEntryActions(entry: ProjectEntry) {
  treeSelection.value = entry.path;
  inlineActionsPath.value =
    inlineActionsPath.value === entry.path ? "" : entry.path;
}

function openCreateDialog(
  kind: "create-file" | "create-directory",
  entry = selectedEntry.value
) {
  editKind.value = kind;
  editTarget.value = entry;
  editDirectory.value = directoryForEntry(entry);
  editValue.value = "";
  editError.value = "";
  inlineActionsPath.value = "";
  editOpen.value = true;
}

function openRenameDialog(entry = selectedEntry.value) {
  if (!entry) return;
  editKind.value = "rename";
  editTarget.value = entry;
  editDirectory.value = parentPath(entry.path);
  editValue.value = entry.name;
  editError.value = "";
  inlineActionsPath.value = "";
  editOpen.value = true;
}

function requestDelete(entry = selectedEntry.value) {
  if (!entry) return;
  deleteTarget.value = entry;
  deleteError.value = "";
  inlineActionsPath.value = "";
  deleteOpen.value = true;
}

function replacePathPrefix(
  path: string,
  oldPath: string,
  newPath: string
): string {
  if (path === oldPath) return newPath;
  if (path.startsWith(`${oldPath}/`)) {
    return `${newPath}${path.slice(oldPath.length)}`;
  }
  return path;
}

function validateName(name: string): string | undefined {
  if (!name || name === "." || name === "..") return "请输入有效名称";
  if (/[\\/]/.test(name)) return "名称不能包含路径分隔符";
  return undefined;
}

async function submitEdit() {
  if (!props.projectPath) return;
  const name = editValue.value.trim();
  const validationError = validateName(name);
  if (validationError) {
    editError.value = validationError;
    return;
  }

  operating.value = true;
  editError.value = "";
  try {
    if (editKind.value === "rename" && editTarget.value) {
      const target = editTarget.value;
      const newPath = await api.renameProjectEntry(
        props.projectPath,
        target.path,
        name
      );
      treeSelection.value = newPath;
      if (target.is_dir) {
        collapsed.value = new Set(
          Array.from(collapsed.value).map((path) =>
            replacePathPrefix(path, target.path, newPath)
          )
        );
      }
      emit("entry-renamed", target.path, newPath, target.is_dir);
      await loadTree();
    } else {
      const path = joinPath(editDirectory.value, name);
      const createdPath =
        editKind.value === "create-directory"
          ? await api.createProjectDirectory(props.projectPath, path)
          : await api.createProjectFile(props.projectPath, path);
      if (editDirectory.value) {
        const next = new Set(collapsed.value);
        next.delete(editDirectory.value);
        collapsed.value = next;
      }
      await loadTree();
      treeSelection.value = createdPath;
      if (editKind.value === "create-file") emit("select", createdPath);
    }
    editOpen.value = false;
  } catch (cause) {
    editError.value = String(cause);
  } finally {
    operating.value = false;
  }
}

async function confirmDelete() {
  if (!props.projectPath || !deleteTarget.value) return;
  const target = deleteTarget.value;
  operating.value = true;
  deleteError.value = "";
  try {
    await api.deleteProjectEntry(props.projectPath, target.path);
    collapsed.value = new Set(
      Array.from(collapsed.value).filter(
        (path) =>
          path !== target.path && !path.startsWith(`${target.path}/`)
      )
    );
    treeSelection.value = parentPath(target.path);
    emit("entry-deleted", target.path, target.is_dir);
    await loadTree();
    deleteOpen.value = false;
  } catch (cause) {
    deleteError.value = String(cause);
  } finally {
    operating.value = false;
  }
}

function showNotice(message: string) {
  notice.value = message;
  if (noticeTimer) clearTimeout(noticeTimer);
  noticeTimer = setTimeout(() => {
    notice.value = "";
  }, 1800);
}

function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    return navigator.clipboard.writeText(text);
  }
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  const copied = document.execCommand("copy");
  textarea.remove();
  return copied ? Promise.resolve() : Promise.reject(new Error("复制失败"));
}

async function copyEntryPath(entry = selectedEntry.value) {
  if (!props.projectPath) return;
  inlineActionsPath.value = "";
  treeError.value = "";
  try {
    const path = await api.resolveProjectPath(
      props.projectPath,
      entry?.path ?? ""
    );
    await copyText(path);
    showNotice("已复制路径");
  } catch (cause) {
    treeError.value = String(cause);
  }
}

async function revealEntry(entry = selectedEntry.value) {
  if (!props.projectPath) return;
  inlineActionsPath.value = "";
  treeError.value = "";
  try {
    const path = await api.resolveProjectPath(
      props.projectPath,
      entry?.path ?? ""
    );
    await revealItemInDir(path);
  } catch (cause) {
    treeError.value = String(cause);
  }
}

async function onMarkdownClick(event: MouseEvent) {
  const target = event.target as HTMLElement;
  const button = target.closest(".code-block-copy") as HTMLButtonElement | null;
  if (!button) return;
  const code = button
    .closest(".code-block")
    ?.querySelector("code")
    ?.textContent;
  if (code == null) return;
  try {
    await copyText(code);
    button.textContent = "已复制";
    button.classList.add("is-copied");
    setTimeout(() => {
      button.textContent = "复制";
      button.classList.remove("is-copied");
    }, 1500);
  } catch (cause) {
    fileError.value = String(cause);
  }
}

async function loadTree() {
  if (!props.projectPath) {
    entries.value = [];
    return;
  }
  loadingTree.value = true;
  treeError.value = "";
  try {
    entries.value = await api.listProjectFiles(props.projectPath);
    if (
      treeSelection.value &&
      !entries.value.some((entry) => entry.path === treeSelection.value)
    ) {
      treeSelection.value = "";
    }
  } catch (cause) {
    treeError.value = String(cause);
    entries.value = [];
  } finally {
    loadingTree.value = false;
  }
}

async function loadFile() {
  const request = ++fileRequest;
  if (!props.projectPath || !props.selectedPath) {
    content.value = "";
    fileError.value = "";
    return;
  }
  loadingFile.value = true;
  fileError.value = "";
  try {
    const next = await api.readProjectFile(
      props.projectPath,
      props.selectedPath
    );
    if (request === fileRequest) content.value = next;
  } catch (cause) {
    if (request === fileRequest) {
      content.value = "";
      fileError.value = String(cause);
    }
  } finally {
    if (request === fileRequest) loadingFile.value = false;
  }
}

watch(
  () => props.projectPath,
  () => {
    collapsed.value = new Set();
    treeSelection.value = "";
    inlineActionsPath.value = "";
    query.value = "";
    void loadTree();
  },
  { immediate: true }
);

watch(
  () => props.selectedPath,
  (path) => {
    if (path) treeSelection.value = path;
    inlineActionsPath.value = "";
    previewMode.value =
      props.selectedMode === "diff" && selectedDiff.value ? "diff" : "file";
    void loadFile();
  },
  { immediate: true }
);

watch(
  () => props.selectedMode,
  (mode) => {
    previewMode.value =
      mode === "diff" && selectedDiff.value ? "diff" : "file";
  }
);

watch(diffSignature, (value, previous) => {
  if (value !== previous) void loadTree();
});

onBeforeUnmount(() => {
  if (noticeTimer) clearTimeout(noticeTimer);
  stopTreeResize?.();
});
</script>

<template>
  <div ref="panelRoot" class="relative flex h-full min-h-0 bg-background">
    <section class="relative flex min-w-0 flex-1 flex-col">
      <Button
        v-if="projectPath && !treeOpen"
        size="icon"
        variant="ghost"
        class="absolute right-1.5 top-1.5 z-20 size-7"
        title="展开文件列表"
        aria-label="展开文件列表"
        @click="treeOpen = true"
      >
        <FolderTreeIcon class="size-3.5" />
      </Button>

      <div
        v-if="selectedPath"
        :class="[
          'flex h-10 shrink-0 items-center gap-2 border-b border-border pl-3',
          treeOpen ? 'pr-3' : 'pr-11',
        ]"
      >
        <FileCodeIcon class="size-4 shrink-0 text-muted-foreground" />
        <span class="min-w-0 flex-1 truncate font-mono text-xs">
          {{ selectedPath }}
        </span>
        <div
          v-if="selectedDiff"
          class="flex shrink-0 items-center rounded-md bg-muted p-0.5"
        >
          <button
            v-for="mode in ['file', 'diff'] as const"
            :key="mode"
            type="button"
            :class="[
              'rounded px-2 py-1 text-[11px] transition-colors',
              previewMode === mode
                ? 'bg-background text-foreground shadow-sm'
                : 'text-muted-foreground',
            ]"
            @click="
              previewMode = mode;
              emit('update:selected-mode', mode);
            "
          >
            {{ mode === "file" ? "文件" : "变更" }}
          </button>
        </div>
      </div>

      <div
        v-if="!selectedPath"
        class="flex min-h-0 flex-1 items-center justify-center px-8 text-center text-sm text-muted-foreground"
      >
        从文件列表选择文件进行预览
      </div>

      <div
        v-else-if="loadingFile"
        class="flex min-h-0 flex-1 items-center justify-center text-muted-foreground"
      >
        <RefreshCwIcon class="size-4 animate-spin" />
      </div>

      <div
        v-else-if="fileError"
        class="flex min-h-0 flex-1 flex-col items-center justify-center gap-2 px-8 text-center"
      >
        <AlertCircleIcon class="size-5 text-destructive" />
        <p class="text-xs text-muted-foreground">{{ fileError }}</p>
      </div>

      <div
        v-else-if="previewMode === 'diff' && selectedDiff"
        class="min-h-0 flex-1 overflow-auto bg-muted/10 font-mono text-[11px] leading-5"
      >
        <div class="sticky top-0 z-10 flex gap-2 border-b border-border bg-background px-3 py-1.5">
          <span class="text-emerald-600">+{{ selectedDiff.additions }}</span>
          <span class="text-red-600">-{{ selectedDiff.deletions }}</span>
        </div>
        <div
          v-for="(line, index) in selectedDiffLines"
          :key="index"
          :class="['flex min-w-max', lineClass(line.kind)]"
        >
          <span class="w-4 shrink-0 select-none text-center opacity-60">
            {{ line.kind === "add" ? "+" : line.kind === "delete" ? "-" : " " }}
          </span>
          <span class="w-10 shrink-0 select-none border-r border-border/60 pr-2 text-right text-muted-foreground/50">
            {{ line.lineNumber ?? "" }}
          </span>
          <span class="whitespace-pre px-2">{{ line.text || " " }}</span>
        </div>
      </div>

      <article
        v-else-if="isMarkdown"
        class="prose-chat markdown-preview min-h-0 flex-1 overflow-auto px-6 py-4"
        @click="onMarkdownClick"
        v-html="markdownPreview"
      />

      <CodePreview
        v-else
        :path="selectedPath"
        :content="content"
      />
    </section>

    <aside
      v-if="projectPath && treeOpen"
      :class="[
        'relative flex shrink-0 flex-col border-l border-border bg-muted/10',
        !treeResizing && 'transition-[width] duration-150',
      ]"
      :style="{ width: `${treeWidth}px` }"
      aria-label="项目文件"
    >
      <button
        type="button"
        class="absolute -left-1 top-0 z-30 h-full w-2 cursor-col-resize touch-none"
        aria-label="调整文件列表宽度"
        title="拖动调整文件列表宽度，双击恢复默认"
        @pointerdown="startTreeResize"
        @dblclick="resetTreeWidth"
      >
        <span class="absolute left-1/2 top-1/2 h-10 w-0.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-border opacity-0 transition-opacity hover:opacity-100" />
      </button>
      <div class="flex h-10 shrink-0 items-center gap-0.5 border-b border-border px-1.5">
        <span class="min-w-0 flex-1 truncate pl-1 text-xs font-medium">
          {{ projectName }}
        </span>
        <Button
          size="icon"
          variant="ghost"
          class="size-7"
          title="新建文件"
          aria-label="新建文件"
          @click="openCreateDialog('create-file')"
        >
          <FilePlus2Icon class="size-3.5" />
        </Button>
        <Button
          size="icon"
          variant="ghost"
          class="size-7"
          title="新建文件夹"
          aria-label="新建文件夹"
          @click="openCreateDialog('create-directory')"
        >
          <FolderPlusIcon class="size-3.5" />
        </Button>
        <Button
          size="icon"
          variant="ghost"
          class="size-7"
          title="收起文件列表"
          aria-label="收起文件列表"
          @click="treeOpen = false"
        >
          <FolderTreeIcon class="size-3.5" />
        </Button>
      </div>

      <div class="relative shrink-0 border-b border-border p-2">
        <SearchIcon class="pointer-events-none absolute left-4 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          v-model="query"
          class="h-7 pl-7 text-xs"
          placeholder="筛选文件"
        />
      </div>

      <div v-if="treeError" class="px-3 py-2 text-xs text-destructive">
        {{ treeError }}
      </div>

      <div class="min-h-0 flex-1 overflow-y-auto py-1">
          <ContextMenuRoot
            v-for="entry in visibleEntries"
            :key="entry.path"
          >
            <ContextMenuTrigger as-child>
              <button
                type="button"
                :class="[
                  'flex h-7 w-full items-center gap-1.5 pr-2 text-left text-xs hover:bg-muted',
                  treeSelection === entry.path && 'bg-muted text-foreground',
                ]"
                :style="{ paddingLeft: `${8 + (entry.path.split('/').length - 1) * 12}px` }"
                @click="selectTreeEntry(entry, $event)"
                @dblclick.stop="showEntryActions(entry)"
                @contextmenu="treeSelection = entry.path"
              >
                <template v-if="entry.is_dir">
                  <ChevronRightIcon
                    v-if="collapsed.has(entry.path)"
                    class="size-3 shrink-0 text-muted-foreground"
                  />
                  <ChevronDownIcon
                    v-else
                    class="size-3 shrink-0 text-muted-foreground"
                  />
                  <FolderIcon class="size-3.5 shrink-0 text-muted-foreground" />
                </template>
                <template v-else>
                  <span class="w-3 shrink-0" />
                  <FileIcon class="size-3.5 shrink-0 text-muted-foreground" />
                </template>
                <span class="min-w-0 flex-1 truncate">{{ entry.name }}</span>
                <span
                  v-if="!entry.is_dir && diffs.has(entry.path)"
                  class="size-1.5 shrink-0 rounded-full bg-amber-500"
                  title="当前会话有变更"
                />
              </button>
            </ContextMenuTrigger>
            <div
              v-if="inlineActionsPath === entry.path"
              class="mx-2 my-1 flex h-9 items-center justify-end gap-1 rounded-md border border-border bg-background px-1"
            >
              <Button
                size="icon"
                variant="ghost"
                class="size-7"
                title="重命名"
                aria-label="重命名"
                @click="openRenameDialog(entry)"
              >
                <PencilIcon class="size-3.5" />
              </Button>
              <Button
                size="icon"
                variant="ghost"
                class="size-7"
                title="在 Finder 中打开"
                aria-label="在 Finder 中打开"
                @click="revealEntry(entry)"
              >
                <FolderOpenIcon class="size-3.5" />
              </Button>
              <Button
                size="icon"
                variant="ghost"
                class="size-7"
                title="复制路径"
                aria-label="复制路径"
                @click="copyEntryPath(entry)"
              >
                <CopyIcon class="size-3.5" />
              </Button>
              <Button
                size="icon"
                variant="ghost"
                class="size-7 text-destructive hover:bg-destructive/10 hover:text-destructive"
                title="删除"
                aria-label="删除"
                @click="requestDelete(entry)"
              >
                <Trash2Icon class="size-3.5" />
              </Button>
            </div>
            <ContextMenuPortal>
              <ContextMenuContent
                class="z-[80] min-w-48 rounded-md border border-border bg-popover p-1 text-popover-foreground shadow-md"
                :side-offset="4"
              >
                <ContextMenuItem
                  :class="menuItemClass"
                  @select="openCreateDialog('create-file', entry)"
                >
                  <FilePlus2Icon class="size-3.5 text-muted-foreground" />
                  新建文件
                </ContextMenuItem>
                <ContextMenuItem
                  :class="menuItemClass"
                  @select="openCreateDialog('create-directory', entry)"
                >
                  <FolderPlusIcon class="size-3.5 text-muted-foreground" />
                  新建文件夹
                </ContextMenuItem>
                <ContextMenuSeparator class="my-1 h-px bg-border" />
                <ContextMenuItem
                  :class="menuItemClass"
                  @select="openRenameDialog(entry)"
                >
                  <PencilIcon class="size-3.5 text-muted-foreground" />
                  重命名
                </ContextMenuItem>
                <ContextMenuItem
                  :class="menuItemClass"
                  @select="revealEntry(entry)"
                >
                  <FolderOpenIcon class="size-3.5 text-muted-foreground" />
                  在 Finder 中打开
                </ContextMenuItem>
                <ContextMenuItem
                  :class="menuItemClass"
                  @select="copyEntryPath(entry)"
                >
                  <CopyIcon class="size-3.5 text-muted-foreground" />
                  复制路径
                </ContextMenuItem>
                <ContextMenuSeparator class="my-1 h-px bg-border" />
                <ContextMenuItem
                  :class="dangerMenuItemClass"
                  @select="requestDelete(entry)"
                >
                  <Trash2Icon class="size-3.5" />
                  删除
                </ContextMenuItem>
              </ContextMenuContent>
            </ContextMenuPortal>
          </ContextMenuRoot>
      </div>
    </aside>

    <div
      v-if="notice"
      class="pointer-events-none absolute bottom-3 right-12 z-40 rounded-md border border-border bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-md"
    >
      {{ notice }}
    </div>

    <Dialog v-model:open="editOpen">
      <DialogContent class="gap-4 sm:max-w-sm" :show-close-button="!operating">
        <form class="grid gap-4" @submit.prevent="submitEdit">
          <DialogHeader>
            <DialogTitle>{{ editTitle }}</DialogTitle>
            <DialogDescription class="truncate font-mono text-xs">
              {{ editLocation }}
            </DialogDescription>
          </DialogHeader>
          <div class="grid gap-2">
            <Label for="project-entry-name">{{ editLabel }}</Label>
            <Input
              id="project-entry-name"
              v-model="editValue"
              autofocus
              autocomplete="off"
              :disabled="operating"
            />
            <p v-if="editError" class="text-xs text-destructive">
              {{ editError }}
            </p>
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              :disabled="operating"
              @click="editOpen = false"
            >
              取消
            </Button>
            <Button type="submit" :disabled="operating">
              <RefreshCwIcon v-if="operating" class="size-4 animate-spin" />
              确定
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="deleteOpen">
      <DialogContent class="gap-4 sm:max-w-sm" :show-close-button="!operating">
        <DialogHeader>
          <DialogTitle>
            删除{{ deleteTarget?.is_dir ? "文件夹" : "文件" }}
          </DialogTitle>
          <DialogDescription>
            “{{ deleteTarget?.path }}”将被永久删除，此操作无法撤销。
          </DialogDescription>
        </DialogHeader>
        <p v-if="deleteError" class="text-xs text-destructive">
          {{ deleteError }}
        </p>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            :disabled="operating"
            @click="deleteOpen = false"
          >
            取消
          </Button>
          <Button
            type="button"
            variant="destructive"
            :disabled="operating"
            @click="confirmDelete"
          >
            <RefreshCwIcon v-if="operating" class="size-4 animate-spin" />
            删除
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
