<script setup lang="ts">
import { computed, ref } from "vue";
import {
  ChevronDownIcon,
  ChevronRightIcon,
  Clock3Icon,
  EllipsisIcon,
  FolderIcon,
  FolderOpenIcon,
  PencilIcon,
  PaletteIcon,
  PinIcon,
  PinOffIcon,
  PlusIcon,
  SettingsIcon,
  Trash2Icon,
} from "@lucide/vue";
import { revealItemInDir } from "@tauri-apps/plugin-opener";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarTrigger,
} from "@/components/ui/sidebar";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { ProjectInfo, Session } from "@/lib/api";
import SessionSidebarItem from "@/components/chat/SessionSidebarItem.vue";

const props = defineProps<{
  isMac: boolean;
  sessions: Session[];
  projects: ProjectInfo[];
  activeId: string;
  isDraft?: boolean;
  automationsActive?: boolean;
  // 正在运行 AI 回合的会话 id 集合,侧边栏据此显示加载动画。
  running?: Record<string, boolean>;
  unread?: Record<string, boolean>;
  needsAttention?: Record<string, boolean>;
}>();

function isRunning(id: string) {
  return !!props.running?.[id];
}

function isUnread(id: string) {
  return !!props.unread?.[id];
}

function needsAttention(id: string) {
  return !!props.needsAttention?.[id];
}

const emit = defineEmits<{
  (e: "new", projectID?: string): void;
  (e: "select", id: string): void;
  (e: "rename", id: string, title: string): void;
  (e: "pin", id: string, pinned: boolean): void;
  (e: "fork", id: string): void;
  (e: "delete", id: string): void;
  (e: "delete-dialog-change", open: boolean): void;
  (e: "delete-project", id: string): void;
  (e: "rename-project", id: string, name: string): void;
  (e: "pin-project", id: string, pinned: boolean): void;
  (e: "open-settings"): void;
  (e: "open-studio"): void;
  (e: "open-automations"): void;
}>();

function title(s: Session) {
  return s.title || "新对话";
}

// 删除确认:点击删除只是打开应用内 Dialog,确认后才真正发出 delete 事件。
const pendingDelete = ref<Session | null>(null);
const pendingProjectDelete = ref<ProjectGroup | null>(null);
const pendingProjectRename = ref<ProjectInfo | null>(null);
const projectRename = ref("");

function onDelete(s: Session) {
  pendingDelete.value = s;
  emit("delete-dialog-change", true);
}

function confirmDelete() {
  if (pendingDelete.value) emit("delete", pendingDelete.value.id);
  pendingDelete.value = null;
  emit("delete-dialog-change", false);
}

function closeDeleteDialog() {
  pendingDelete.value = null;
  emit("delete-dialog-change", false);
}

function confirmProjectDelete() {
  if (pendingProjectDelete.value) {
    emit("delete-project", pendingProjectDelete.value.project.id);
  }
  pendingProjectDelete.value = null;
}

function openProjectRename(project: ProjectInfo) {
  pendingProjectRename.value = project;
  projectRename.value = project.name;
}

function confirmProjectRename() {
  const project = pendingProjectRename.value;
  const name = projectRename.value.trim();
  if (!project || !name) return;
  emit("rename-project", project.id, name);
  pendingProjectRename.value = null;
}

async function revealProject(project: ProjectInfo) {
  try {
    await revealItemInDir(project.path);
  } catch (error) {
    console.error("在访达中打开项目失败:", error);
  }
}

function sortSessions(items: Session[]): Session[] {
  return [...items].sort((a, b) => {
    if (Boolean(a.pinned) !== Boolean(b.pinned)) return a.pinned ? -1 : 1;
    const aTime = a.pinned ? a.pinned_at ?? a.updated_at : a.updated_at;
    const bTime = b.pinned ? b.pinned_at ?? b.updated_at : b.updated_at;
    return bTime.localeCompare(aTime);
  });
}

const ungroupedSessions = computed(() =>
  sortSessions(
    props.sessions.filter(
      (session) =>
        !session.project_id ||
        !props.projects.some((project) => project.id === session.project_id)
    )
  )
);

interface ProjectGroup {
  project: ProjectInfo;
  sessions: Session[];
  updatedAt: string;
}

const collapsedProjects = ref<Set<string>>(new Set());

function toggleProject(projectID: string) {
  const next = new Set(collapsedProjects.value);
  if (next.has(projectID)) next.delete(projectID);
  else next.add(projectID);
  collapsedProjects.value = next;
}

function newProjectSession(projectID: string) {
  const next = new Set(collapsedProjects.value);
  next.delete(projectID);
  collapsedProjects.value = next;
  emit("new", projectID);
}

function projectIsActive(project: ProjectGroup): boolean {
  return project.sessions.some((session) => session.id === props.activeId);
}

const projectGroups = computed<ProjectGroup[]>(() => {
  const grouped = new Map<string, Session[]>(
    props.projects.map((project) => [project.id, []])
  );
  for (const session of props.sessions) {
    const projectID = session.project_id;
    if (!projectID || !grouped.has(projectID)) continue;
    const items = grouped.get(projectID) ?? [];
    items.push(session);
    grouped.set(projectID, items);
  }

  return props.projects
    .map((project) => {
      const items = grouped.get(project.id) ?? [];
      const sorted = sortSessions(items);
      return {
        project,
        sessions: sorted,
        updatedAt: sorted.reduce(
          (latest, session) =>
            session.updated_at > latest ? session.updated_at : latest,
          project.updated_at
        ),
      };
    })
    .sort((a, b) => {
      if (Boolean(a.project.pinned) !== Boolean(b.project.pinned)) {
        return a.project.pinned ? -1 : 1;
      }
      const aTime = a.project.pinned
        ? a.project.pinned_at ?? a.updatedAt
        : a.updatedAt;
      const bTime = b.project.pinned
        ? b.project.pinned_at ?? b.updatedAt
        : b.updatedAt;
      return bTime.localeCompare(aTime);
    });
});
</script>

<template>
  <Sidebar collapsible="offcanvas">
    <SidebarHeader
      data-tauri-drag-region
      class="flex h-9 flex-row items-center gap-1 p-2"
      :class="isMac ? 'pl-[72px]' : ''"
    >
      <SidebarTrigger class="no-drag text-sidebar-foreground/70" />
    </SidebarHeader>

    <SidebarContent>
      <SidebarGroup class="p-2 pt-1 pb-0">
        <div class="flex h-9 items-center px-2 text-lg font-semibold text-sidebar-foreground">
          Foya
        </div>
        <SidebarGroupContent>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton
                class="no-drag"
                :is-active="isDraft && !automationsActive"
                tooltip="新建对话"
                @click="emit('new')"
              >
                <PlusIcon />
                <span>新建对话</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                class="no-drag"
                tooltip="创作工作台"
                @click="emit('open-studio')"
              >
                <PaletteIcon />
                <span>创作工作台</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                class="no-drag"
                :is-active="automationsActive"
                tooltip="自动化"
                @click="emit('open-automations')"
              >
                <Clock3Icon />
                <span>自动化</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>

      <SidebarGroup v-if="ungroupedSessions.length" class="gap-1 p-2 pt-3">
        <SidebarGroupLabel class="h-6 px-2 text-[11px]">
          对话
        </SidebarGroupLabel>
        <SidebarGroupContent>
          <SidebarMenu>
            <SessionSidebarItem
              v-for="session in ungroupedSessions"
              :key="session.id"
              :session="session"
              :active="session.id === activeId"
              :running="isRunning(session.id)"
              :unread="isUnread(session.id)"
              :needs-attention="needsAttention(session.id)"
              @select="emit('select', $event)"
              @rename="(id, value) => emit('rename', id, value)"
              @pin="(id, value) => emit('pin', id, value)"
              @fork="(item) => emit('fork', item.id)"
              @delete="onDelete"
            />
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>

      <SidebarGroup v-if="projectGroups.length" class="gap-1 p-2 pt-3">
        <SidebarGroupLabel class="h-6 px-2 text-[11px]">
          项目
        </SidebarGroupLabel>
        <SidebarGroupContent class="space-y-2">
          <div
            v-for="project in projectGroups"
            :key="project.project.id"
            class="min-w-0"
          >
            <div
              :class="[
                'group/project flex h-8 min-w-0 items-center rounded-md transition-colors',
                projectIsActive(project)
                  ? 'bg-sidebar-accent/70 text-sidebar-accent-foreground'
                  : 'text-sidebar-foreground hover:bg-sidebar-accent/50',
              ]"
            >
              <button
                type="button"
                class="flex h-full min-w-0 flex-1 items-center gap-1.5 px-2 text-left text-sm font-medium"
                :title="project.project.path"
                :aria-expanded="!collapsedProjects.has(project.project.id)"
                @click="toggleProject(project.project.id)"
              >
                <ChevronRightIcon
                  v-if="collapsedProjects.has(project.project.id)"
                  class="size-3.5 shrink-0 text-sidebar-foreground/60"
                />
                <ChevronDownIcon
                  v-else
                  class="size-3.5 shrink-0 text-sidebar-foreground/60"
                />
                <FolderIcon class="size-4 shrink-0" />
                <span class="truncate">{{ project.project.name }}</span>
              </button>
              <PinIcon
                v-if="project.project.pinned"
                class="size-3 shrink-0 text-sidebar-foreground/50"
                aria-label="已置顶"
              />
              <DropdownMenu>
                <DropdownMenuTrigger as-child>
                  <button
                    type="button"
                    class="flex size-6 shrink-0 items-center justify-center rounded-md text-sidebar-foreground/60 opacity-0 transition-opacity hover:bg-sidebar-accent hover:text-sidebar-foreground group-hover/project:opacity-100 group-focus-within/project:opacity-100"
                    :aria-label="`${project.project.name} 项目操作`"
                    title="项目操作"
                  >
                    <EllipsisIcon class="size-3.5" />
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" class="w-40">
                  <DropdownMenuItem @select="openProjectRename(project.project)">
                    <PencilIcon />
                    重命名
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    @select="
                      emit(
                        'pin-project',
                        project.project.id,
                        !project.project.pinned
                      )
                    "
                  >
                    <PinOffIcon v-if="project.project.pinned" />
                    <PinIcon v-else />
                    {{ project.project.pinned ? "取消置顶" : "置顶" }}
                  </DropdownMenuItem>
                  <DropdownMenuItem @select="revealProject(project.project)">
                    <FolderOpenIcon />
                    在访达中打开
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    variant="destructive"
                    @select="pendingProjectDelete = project"
                  >
                    <Trash2Icon />
                    删除项目
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
              <button
                type="button"
                class="mr-1 flex size-6 shrink-0 items-center justify-center rounded-md text-sidebar-foreground/60 hover:bg-sidebar-accent hover:text-sidebar-foreground"
                :title="`在 ${project.project.name} 中新建对话`"
                :aria-label="`在 ${project.project.name} 中新建对话`"
                @click="newProjectSession(project.project.id)"
              >
                <PlusIcon class="size-3.5" />
              </button>
            </div>

            <div
              v-if="!collapsedProjects.has(project.project.id)"
              class="ml-[18px] border-l border-sidebar-border pb-1 pl-2 pt-1"
            >
              <SidebarMenu>
                <SessionSidebarItem
                  v-for="session in project.sessions"
                  :key="session.id"
                  :session="session"
                  :active="session.id === activeId"
                  :running="isRunning(session.id)"
                  :unread="isUnread(session.id)"
                  :needs-attention="needsAttention(session.id)"
                  @select="emit('select', $event)"
                  @rename="(id, value) => emit('rename', id, value)"
                  @pin="(id, value) => emit('pin', id, value)"
                  @fork="(item) => emit('fork', item.id)"
                  @delete="onDelete"
                />
              </SidebarMenu>
            </div>
          </div>
        </SidebarGroupContent>
      </SidebarGroup>
      <p
        v-if="sessions.length === 0"
        class="px-4 py-6 text-center text-xs text-muted-foreground"
      >
        暂无对话
      </p>
    </SidebarContent>

    <SidebarFooter class="p-2">
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton class="no-drag" tooltip="设置" @click="emit('open-settings')">
            <SettingsIcon />
            <span>设置</span>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </SidebarFooter>
  </Sidebar>

  <Dialog
    :open="pendingDelete !== null"
    @update:open="(v) => !v && closeDeleteDialog()"
  >
    <DialogContent class="max-w-md">
      <DialogHeader>
        <div class="flex items-center gap-2">
          <Trash2Icon class="size-5 text-destructive" />
          <DialogTitle>删除对话</DialogTitle>
        </div>
        <DialogDescription>
          确定删除对话「{{ pendingDelete ? title(pendingDelete) : "" }}」吗？此操作不可撤销。
        </DialogDescription>
      </DialogHeader>
      <DialogFooter class="gap-2">
        <Button variant="outline" @click="closeDeleteDialog">取消</Button>
        <Button variant="destructive" @click="confirmDelete">删除</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>

  <Dialog
    :open="pendingProjectDelete !== null"
    @update:open="(v) => !v && (pendingProjectDelete = null)"
  >
    <DialogContent class="max-w-md">
      <DialogHeader>
        <div class="flex items-center gap-2">
          <Trash2Icon class="size-5 text-destructive" />
          <DialogTitle>删除项目</DialogTitle>
        </div>
        <DialogDescription>
          「{{ pendingProjectDelete?.project.name }}」及其关联对话、历史和附件将从 Foya 永久删除，磁盘文件不会被删除。
        </DialogDescription>
      </DialogHeader>
      <DialogFooter class="gap-2">
        <Button variant="outline" @click="pendingProjectDelete = null">取消</Button>
        <Button variant="destructive" @click="confirmProjectDelete">删除</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>

  <Dialog
    :open="pendingProjectRename !== null"
    @update:open="(v) => !v && (pendingProjectRename = null)"
  >
    <DialogContent class="max-w-md">
      <DialogHeader>
        <DialogTitle>重命名项目</DialogTitle>
        <DialogDescription class="sr-only">
          修改项目显示名称
        </DialogDescription>
      </DialogHeader>
      <form class="space-y-4" @submit.prevent="confirmProjectRename">
        <Input
          v-model="projectRename"
          autofocus
          aria-label="项目名称"
          placeholder="项目名称"
        />
        <DialogFooter class="gap-2">
          <Button
            type="button"
            variant="outline"
            @click="pendingProjectRename = null"
          >
            取消
          </Button>
          <Button type="submit" :disabled="!projectRename.trim()">
            保存
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
