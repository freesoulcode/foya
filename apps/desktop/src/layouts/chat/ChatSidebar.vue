<script setup lang="ts">
import { computed, nextTick, ref } from "vue";
import { useI18n } from "vue-i18n";
import {
  ChevronRightIcon,
  Clock3Icon,
  EllipsisIcon,
  FolderOpenIcon,
  PencilIcon,
  PackageIcon,
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
import ChannelBindingButton from "@/components/chat/ChannelBindingButton.vue";
import SessionSidebarItem from "@/components/chat/SessionSidebarItem.vue";
import {
  buildChatSidebarGroups,
  type ProjectGroup,
} from "./chatSidebarGroups";

const props = defineProps<{
  isMac: boolean;
  sessions: Session[];
  projects: ProjectInfo[];
  activeId: string;
  isDraft?: boolean;
  automationsActive?: boolean;
  pluginsActive?: boolean;
  // Session IDs with an active AI turn, used to render progress indicators.
  running?: Record<string, boolean>;
  unread?: Record<string, boolean>;
  needsAttention?: Record<string, boolean>;
}>();
const { t } = useI18n();

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
  (e: "open-plugins"): void;
}>();

function title(s: Session) {
  return s.title || t("New chat");
}

// Deletion is confirmed in-app before the delete event is emitted.
const pendingDelete = ref<Session | null>(null);
const pendingProjectDelete = ref<ProjectGroup | null>(null);
const pendingProjectRename = ref<ProjectInfo | null>(null);
const pendingChannelSession = ref<Session | null>(null);
const channelBinding = ref<InstanceType<typeof ChannelBindingButton> | null>(
  null
);
const projectRename = ref("");

function onDelete(s: Session) {
  pendingDelete.value = s;
  emit("delete-dialog-change", true);
}

async function openChannelBinding(session: Session) {
  pendingChannelSession.value = session;
  await nextTick();
  await channelBinding.value?.open();
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
    console.error("Failed to reveal project in Finder:", error);
  }
}

const sidebarGroups = computed(() =>
  buildChatSidebarGroups(props.sessions, props.projects),
);
const pinnedSessions = computed(() => sidebarGroups.value.pinnedSessions);
const ungroupedSessions = computed(
  () => sidebarGroups.value.ungroupedSessions,
);
const projectGroups = computed(() => sidebarGroups.value.projectGroups);

type SidebarSection = "pinned" | "chats" | "projects";

const collapsedSections = ref<Set<SidebarSection>>(new Set());
const collapsedProjects = ref<Set<string>>(new Set());

function toggleSection(section: SidebarSection) {
  const next = new Set(collapsedSections.value);
  if (next.has(section)) next.delete(section);
  else next.add(section);
  collapsedSections.value = next;
}

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

function sectionIsCollapsed(section: SidebarSection): boolean {
  return collapsedSections.value.has(section);
}

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
                :is-active="isDraft && !automationsActive && !pluginsActive"
                :tooltip="$t('New chat')"
                @click="emit('new')"
              >
                <PlusIcon />
                <span>{{ $t("New chat") }}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                class="no-drag"
                :tooltip="$t('Creative workspace')"
                @click="emit('open-studio')"
              >
                <PaletteIcon />
                <span>{{ $t("Creative workspace") }}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                class="no-drag"
                :is-active="automationsActive"
                :tooltip="$t('Automations')"
                @click="emit('open-automations')"
              >
                <Clock3Icon />
                <span>{{ $t("Automations") }}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                class="no-drag"
                :is-active="pluginsActive"
                :tooltip="$t('Plugins')"
                @click="emit('open-plugins')"
              >
                <PackageIcon />
                <span>{{ $t("Plugins") }}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>

      <SidebarGroup v-if="pinnedSessions.length" class="gap-1 p-2 pt-3">
        <SidebarGroupLabel
          as="button"
          type="button"
          class="group/section relative h-6 w-full px-2 text-[11px] transition-colors hover:bg-sidebar-accent/50 hover:text-sidebar-foreground"
          :aria-expanded="!sectionIsCollapsed('pinned')"
          @click="toggleSection('pinned')"
        >
          <span>{{ $t("Pinned") }}</span>
          <ChevronRightIcon
            :class="[
              'absolute right-2 transition-all duration-200 ease-out',
              sectionIsCollapsed('pinned')
                ? 'rotate-0 opacity-100'
                : 'rotate-90 opacity-0 group-hover/section:opacity-100 group-focus-visible/section:opacity-100',
            ]"
          />
        </SidebarGroupLabel>
        <div
          :class="[
            'grid overflow-hidden transition-all duration-200 ease-out',
            sectionIsCollapsed('pinned')
              ? 'grid-rows-[0fr] -translate-y-1 opacity-0'
              : 'grid-rows-[1fr] translate-y-0 opacity-100',
          ]"
        >
          <div class="min-h-0 overflow-hidden">
            <SidebarGroupContent>
              <SidebarMenu>
                <SessionSidebarItem
                  v-for="session in pinnedSessions"
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
                  @connect="openChannelBinding"
                  @delete="onDelete"
                />
              </SidebarMenu>
            </SidebarGroupContent>
          </div>
        </div>
      </SidebarGroup>

      <SidebarGroup v-if="ungroupedSessions.length" class="gap-1 p-2 pt-3">
        <SidebarGroupLabel
          as="button"
          type="button"
          class="group/section relative h-6 w-full px-2 text-[11px] transition-colors hover:bg-sidebar-accent/50 hover:text-sidebar-foreground"
          :aria-expanded="!sectionIsCollapsed('chats')"
          @click="toggleSection('chats')"
        >
          <span>{{ $t("Chats") }}</span>
          <ChevronRightIcon
            :class="[
              'absolute right-2 transition-all duration-200 ease-out',
              sectionIsCollapsed('chats')
                ? 'rotate-0 opacity-100'
                : 'rotate-90 opacity-0 group-hover/section:opacity-100 group-focus-visible/section:opacity-100',
            ]"
          />
        </SidebarGroupLabel>
        <div
          :class="[
            'grid overflow-hidden transition-all duration-200 ease-out',
            sectionIsCollapsed('chats')
              ? 'grid-rows-[0fr] -translate-y-1 opacity-0'
              : 'grid-rows-[1fr] translate-y-0 opacity-100',
          ]"
        >
          <div class="min-h-0 overflow-hidden">
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
                  @connect="openChannelBinding"
                  @delete="onDelete"
                />
              </SidebarMenu>
            </SidebarGroupContent>
          </div>
        </div>
      </SidebarGroup>

      <SidebarGroup v-if="projectGroups.length" class="gap-1 p-2 pt-3">
        <SidebarGroupLabel
          as="button"
          type="button"
          class="group/section relative h-6 w-full px-2 text-[11px] transition-colors hover:bg-sidebar-accent/50 hover:text-sidebar-foreground"
          :aria-expanded="!sectionIsCollapsed('projects')"
          @click="toggleSection('projects')"
        >
          <span>{{ $t("Projects") }}</span>
          <ChevronRightIcon
            :class="[
              'absolute right-2 transition-all duration-200 ease-out',
              sectionIsCollapsed('projects')
                ? 'rotate-0 opacity-100'
                : 'rotate-90 opacity-0 group-hover/section:opacity-100 group-focus-visible/section:opacity-100',
            ]"
          />
        </SidebarGroupLabel>
        <div
          :class="[
            'grid overflow-hidden transition-all duration-200 ease-out',
            sectionIsCollapsed('projects')
              ? 'grid-rows-[0fr] -translate-y-1 opacity-0'
              : 'grid-rows-[1fr] translate-y-0 opacity-100',
          ]"
        >
          <div class="min-h-0 overflow-hidden">
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
                  :class="[
                    'size-3.5 shrink-0 text-sidebar-foreground/60 transition-transform duration-200 ease-out',
                    !collapsedProjects.has(project.project.id) && 'rotate-90',
                  ]"
                />
                <FolderOpenIcon class="size-4 shrink-0" />
                <span class="truncate">{{ project.project.name }}</span>
              </button>
              <PinIcon
                v-if="project.project.pinned"
                class="size-3 shrink-0 text-sidebar-foreground/50"
                :aria-label="$t('Pinned')"
              />
              <DropdownMenu>
                <DropdownMenuTrigger as-child>
                  <button
                    type="button"
                    class="flex size-6 shrink-0 items-center justify-center rounded-md text-sidebar-foreground/60 opacity-0 transition-opacity hover:bg-sidebar-accent hover:text-sidebar-foreground group-hover/project:opacity-100 group-focus-within/project:opacity-100"
                    :aria-label="$t('Project actions', { project: project.project.name })"
                    :title="$t('Project actions', { project: project.project.name })"
                  >
                    <EllipsisIcon class="size-3.5" />
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" class="w-40">
                  <DropdownMenuItem @select="openProjectRename(project.project)">
                    <PencilIcon />
                    {{ $t("Rename") }}
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
                    {{ project.project.pinned ? $t("Unpin") : $t("Pin") }}
                  </DropdownMenuItem>
                  <DropdownMenuItem @select="revealProject(project.project)">
                    <FolderOpenIcon />
                    {{ $t("Reveal in Finder") }}
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    variant="destructive"
                    @select="pendingProjectDelete = project"
                  >
                    <Trash2Icon />
                    {{ $t("Delete project") }}
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
              <button
                type="button"
                class="mr-1 flex size-6 shrink-0 items-center justify-center rounded-md text-sidebar-foreground/60 hover:bg-sidebar-accent hover:text-sidebar-foreground"
                :title="$t('Create chat in project', { project: project.project.name })"
                :aria-label="$t('Create chat in project', { project: project.project.name })"
                @click="newProjectSession(project.project.id)"
              >
                <PlusIcon class="size-3.5" />
              </button>
            </div>

            <div
              :class="[
                'grid overflow-hidden transition-all duration-200 ease-out',
                collapsedProjects.has(project.project.id)
                  ? 'grid-rows-[0fr] -translate-y-1 opacity-0'
                  : 'grid-rows-[1fr] translate-y-0 opacity-100',
              ]"
            >
              <div class="min-h-0 overflow-hidden">
                <div class="ml-[18px] border-l border-sidebar-border pb-1 pl-2 pt-1">
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
                      @connect="openChannelBinding"
                      @delete="onDelete"
                    />
                  </SidebarMenu>
                </div>
              </div>
            </div>
              </div>
            </SidebarGroupContent>
          </div>
        </div>
      </SidebarGroup>
      <p
        v-if="sessions.length === 0"
        class="px-4 py-6 text-center text-xs text-muted-foreground"
      >
        {{ $t("No chats") }}
      </p>
    </SidebarContent>

    <SidebarFooter class="p-2">
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton class="no-drag" :tooltip="$t('Settings')" @click="emit('open-settings')">
            <SettingsIcon />
            <span>{{ $t("Settings") }}</span>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </SidebarFooter>
  </Sidebar>

  <ChannelBindingButton
    v-if="pendingChannelSession"
    ref="channelBinding"
    :session="pendingChannelSession"
    :show-trigger="false"
  />

  <Dialog
    :open="pendingDelete !== null"
    @update:open="(v) => !v && closeDeleteDialog()"
  >
    <DialogContent class="max-w-md">
      <DialogHeader>
        <div class="flex items-center gap-2">
          <Trash2Icon class="size-5 text-destructive" />
          <DialogTitle>{{ $t("Delete chat") }}</DialogTitle>
        </div>
        <DialogDescription>
          {{ $t("Delete chat confirmation", { title: pendingDelete ? title(pendingDelete) : "" }) }}
        </DialogDescription>
      </DialogHeader>
      <DialogFooter class="gap-2">
        <Button variant="outline" @click="closeDeleteDialog">{{ $t("Cancel") }}</Button>
        <Button variant="destructive" @click="confirmDelete">{{ $t("Delete") }}</Button>
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
          <DialogTitle>{{ $t("Delete project") }}</DialogTitle>
        </div>
        <DialogDescription>
          {{ $t("Delete project confirmation", { project: pendingProjectDelete?.project.name ?? "" }) }}
        </DialogDescription>
      </DialogHeader>
      <DialogFooter class="gap-2">
        <Button variant="outline" @click="pendingProjectDelete = null">{{ $t("Cancel") }}</Button>
        <Button variant="destructive" @click="confirmProjectDelete">{{ $t("Delete") }}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>

  <Dialog
    :open="pendingProjectRename !== null"
    @update:open="(v) => !v && (pendingProjectRename = null)"
  >
    <DialogContent class="max-w-md">
      <DialogHeader>
        <DialogTitle>{{ $t("Rename project") }}</DialogTitle>
        <DialogDescription class="sr-only">
          {{ $t("Change the project display name") }}
        </DialogDescription>
      </DialogHeader>
      <form class="space-y-4" @submit.prevent="confirmProjectRename">
        <Input
          v-model="projectRename"
          autofocus
          :aria-label="$t('Project name')"
          :placeholder="$t('Project name')"
        />
        <DialogFooter class="gap-2">
          <Button
            type="button"
            variant="outline"
            @click="pendingProjectRename = null"
          >
            {{ $t("Cancel") }}
          </Button>
          <Button type="submit" :disabled="!projectRename.trim()">
            {{ $t("Save") }}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
