<script setup lang="ts">
import { computed, nextTick, ref } from "vue";
import { useI18n } from "vue-i18n";
import {
  CircleAlertIcon,
  EllipsisIcon,
  GitForkIcon,
  Link2Icon,
  Loader2Icon,
  MessageCircleIcon,
  PencilIcon,
  PinIcon,
  PinOffIcon,
  Trash2Icon,
} from "@lucide/vue";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";
import type { Session } from "@/lib/api";
import { formatFullTime, formatSessionTime } from "@/lib/dateTime";

const props = defineProps<{
  session: Session;
  active?: boolean;
  running?: boolean;
  unread?: boolean;
  needsAttention?: boolean;
}>();
const { locale, t } = useI18n();
const sessionTime = computed(() =>
  formatSessionTime(props.session.updated_at, locale.value, new Date(), {
    yesterday: t("Yesterday"),
    dayBeforeYesterday: t("Day before yesterday"),
  }),
);
const sessionFullTime = computed(() =>
  formatFullTime(props.session.updated_at, locale.value),
);

const emit = defineEmits<{
  (event: "select", id: string): void;
  (event: "rename", id: string, title: string): void;
  (event: "pin", id: string, pinned: boolean): void;
  (event: "fork", session: Session): void;
  (event: "connect", session: Session): void;
  (event: "delete", session: Session): void;
}>();

const editing = ref(false);
const editingText = ref("");
const editInput = ref<HTMLInputElement | null>(null);

function title() {
  return props.session.title || t("New chat");
}

async function startRename() {
  editing.value = true;
  editingText.value = props.session.title || "";
  await nextTick();
  editInput.value?.focus();
  editInput.value?.select();
}

function commitRename() {
  if (!editing.value) return;
  editing.value = false;
  const value = editingText.value.trim();
  if (value) emit("rename", props.session.id, value);
}

function cancelRename() {
  editing.value = false;
}

function togglePin(event: Event) {
  event.stopPropagation();
  emit("pin", props.session.id, !props.session.pinned);
}

function requestFork(event: Event) {
  event.stopPropagation();
  emit("fork", props.session);
}

function requestDelete(event: Event) {
  event.stopPropagation();
  emit("delete", props.session);
}

function requestConnect() {
  emit("connect", props.session);
}

function queueRename() {
  setTimeout(() => void startRename(), 0);
}
</script>

<template>
  <SidebarMenuItem class="group/menu-item">
    <div class="relative">
      <SidebarMenuButton
        :as="editing ? 'div' : 'button'"
        :is-active="active"
        :tooltip="title()"
        class="pr-2 group-hover/menu-item:pr-20 group-focus-within/menu-item:pr-20"
        @click="!editing && emit('select', session.id)"
      >
        <MessageCircleIcon />
        <input
          v-if="editing"
          ref="editInput"
          v-model="editingText"
          class="w-full bg-transparent outline-none"
          @click.stop
          @keydown.enter.prevent="commitRename"
          @keydown.esc.prevent="cancelRename"
          @blur="commitRename"
        />
        <span
          v-else
          class="min-w-0 flex-1 truncate"
          @dblclick.stop="startRename"
        >
          {{ title() }}
        </span>
        <time
          v-if="sessionTime && !editing"
          :datetime="session.updated_at"
          :title="sessionFullTime"
          class="shrink-0 text-[11px] font-normal text-sidebar-foreground/50 group-hover/menu-item:hidden group-focus-within/menu-item:hidden"
        >
          {{ sessionTime }}
        </time>
        <CircleAlertIcon
          v-if="needsAttention && !editing"
          class="size-3.5 shrink-0 text-amber-500 group-hover/menu-item:hidden group-focus-within/menu-item:hidden"
          :aria-label="$t('Needs attention')"
          :title="$t('Needs attention')"
        />
        <Loader2Icon
          v-else-if="running && !editing"
          class="size-3.5 shrink-0 animate-spin text-primary group-hover/menu-item:hidden group-focus-within/menu-item:hidden"
          :aria-label="$t('AI is running')"
        />
        <span
          v-else-if="unread && !editing"
          class="size-2 shrink-0 rounded-full bg-emerald-500 group-hover/menu-item:hidden group-focus-within/menu-item:hidden"
          :aria-label="$t('Unread result')"
          :title="$t('Unread result')"
        />
      </SidebarMenuButton>
      <span
        v-if="!editing"
        class="absolute right-1 top-1/2 flex -translate-y-1/2 items-center gap-0.5 opacity-0 transition-opacity group-hover/menu-item:opacity-100 group-focus-within/menu-item:opacity-100"
      >
        <button
          type="button"
          class="rounded p-1 text-sidebar-foreground/60 hover:bg-sidebar-accent hover:text-sidebar-foreground disabled:cursor-not-allowed disabled:opacity-40"
          :title="$t('Duplicate chat')"
          :aria-label="$t('Duplicate chat')"
          :disabled="running || needsAttention"
          @click="requestFork"
        >
          <GitForkIcon class="size-3.5" />
        </button>
        <button
          type="button"
          class="rounded p-1 text-sidebar-foreground/60 hover:bg-sidebar-accent hover:text-sidebar-foreground"
          :title="session.pinned ? $t('Unpin') : $t('Pin')"
          @click="togglePin"
        >
          <PinIcon v-if="!session.pinned" class="size-3.5" />
          <PinOffIcon v-else class="size-3.5 text-primary" />
        </button>
        <DropdownMenu>
          <DropdownMenuTrigger as-child>
            <button
              type="button"
              class="rounded p-1 text-sidebar-foreground/60 hover:bg-sidebar-accent hover:text-sidebar-foreground"
              :title="$t('More')"
              :aria-label="$t('More')"
              @click.stop
            >
              <EllipsisIcon class="size-3.5" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" class="w-48">
            <DropdownMenuItem @select="queueRename">
              <PencilIcon />
              {{ $t("Rename") }}
            </DropdownMenuItem>
            <DropdownMenuItem
              v-if="!session.parent_id"
              @select="requestConnect"
            >
              <Link2Icon />
              {{ $t("Connect external conversation") }}
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              class="text-destructive focus:text-destructive"
              @select="requestDelete"
            >
              <Trash2Icon />
              {{ $t("Delete") }}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </span>
    </div>
  </SidebarMenuItem>
</template>
