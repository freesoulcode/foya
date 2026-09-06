<script setup lang="ts">
import { ref, watch } from "vue";
import {
  CheckIcon,
  GripVerticalIcon,
  ImageIcon,
  MousePointer2Icon,
  PackageIcon,
  PencilIcon,
  SendIcon,
  Trash2Icon,
  XIcon,
} from "@lucide/vue";
import { Textarea } from "@/components/ui/textarea";
import type { QueuedMessage } from "@/lib/api";

const props = withDefaults(
  defineProps<{
    items: QueuedMessage[];
    streaming?: boolean;
  }>(),
  {
    streaming: false,
  }
);

const emit = defineEmits<{
  (e: "edit", id: string, text: string): void;
  (e: "reorder", id: string, position: number): void;
  (e: "dispatch", id: string): void;
  (e: "delete", id: string): void;
}>();

const editingId = ref("");
const editText = ref("");
const draggingId = ref("");
const dragOverId = ref("");

function beginEdit(item: QueuedMessage) {
  editingId.value = item.id;
  editText.value = item.text;
}

function cancelEdit() {
  editingId.value = "";
  editText.value = "";
}

function saveEdit() {
  const text = editText.value.trim();
  if (!editingId.value || !text) return;
  emit("edit", editingId.value, text);
  cancelEdit();
}

function onEditKeydown(event: KeyboardEvent) {
  if (event.isComposing) return;
  if (event.key === "Escape") {
    event.preventDefault();
    cancelEdit();
  } else if (event.key === "Enter" && !event.shiftKey) {
    event.preventDefault();
    saveEdit();
  }
}

function beginDrag(event: DragEvent, item: QueuedMessage) {
  if (editingId.value === item.id) {
    event.preventDefault();
    return;
  }
  draggingId.value = item.id;
  dragOverId.value = "";
  if (!event.dataTransfer) return;
  event.dataTransfer.effectAllowed = "move";
  event.dataTransfer.setData("text/plain", item.id);
  const row = (event.currentTarget as HTMLElement).closest("li");
  if (row) event.dataTransfer.setDragImage(row, 20, row.clientHeight / 2);
}

function markDragTarget(event: DragEvent, item: QueuedMessage) {
  if (!draggingId.value || draggingId.value === item.id) {
    dragOverId.value = "";
    return;
  }
  if (event.dataTransfer) event.dataTransfer.dropEffect = "move";
  dragOverId.value = item.id;
}

function dropAt(event: DragEvent, position: number) {
  const id = event.dataTransfer?.getData("text/plain") || draggingId.value;
  const source = props.items.findIndex((item) => item.id === id);
  if (id && source >= 0 && source !== position) {
    emit("reorder", id, position);
  }
  endDrag();
}

function endDrag() {
  draggingId.value = "";
  dragOverId.value = "";
}

function dragIndicatorEdge(targetIndex: number) {
  const sourceIndex = props.items.findIndex((item) => item.id === draggingId.value);
  return sourceIndex >= 0 && sourceIndex < targetIndex ? "bottom-0" : "top-0";
}

function reorderWithKeyboard(
  event: KeyboardEvent,
  item: QueuedMessage,
  index: number,
  direction: -1 | 1
) {
  event.preventDefault();
  const position = index + direction;
  if (position < 0 || position >= props.items.length) return;
  emit("reorder", item.id, position);
}

watch(
  () => props.items,
  (items) => {
    if (editingId.value && !items.some((item) => item.id === editingId.value)) {
      cancelEdit();
    }
    if (draggingId.value && !items.some((item) => item.id === draggingId.value)) {
      endDrag();
    }
  }
);
</script>

<template>
  <TransitionGroup
    tag="ol"
    class="no-scrollbar max-h-72 divide-y divide-border/60 overflow-y-auto"
    enter-active-class="transition duration-200 ease-out"
    enter-from-class="-translate-y-1 opacity-0"
    enter-to-class="translate-y-0 opacity-100"
    leave-active-class="transition duration-150 ease-in"
    leave-from-class="opacity-100"
    leave-to-class="translate-x-2 opacity-0"
    move-class="transition-transform duration-200"
  >
    <li
      v-for="(item, index) in items"
      :key="item.id"
      class="group relative flex min-w-0 flex-wrap items-start gap-1.5 px-2 py-1.5 transition-colors hover:bg-muted/30 sm:flex-nowrap"
      :class="[
        index === 0 ? 'bg-muted/20' : '',
        draggingId === item.id ? 'opacity-40' : '',
        dragOverId === item.id ? 'bg-primary/5' : '',
      ]"
      @dragover.prevent="markDragTarget($event, item)"
      @drop.prevent="dropAt($event, index)"
    >
          <span
            v-if="dragOverId === item.id"
            class="absolute inset-x-2 h-0.5 bg-primary"
            :class="dragIndicatorEdge(index)"
            aria-hidden="true"
          />
          <span
            v-if="index === 0"
            class="absolute inset-y-1.5 left-0 w-0.5 rounded-r-full bg-primary"
            aria-hidden="true"
          />

          <button
            type="button"
            class="flex size-6 shrink-0 cursor-grab items-center justify-center rounded text-muted-foreground/55 transition-colors hover:bg-muted hover:text-foreground active:cursor-grabbing disabled:cursor-default disabled:opacity-25"
            :draggable="editingId !== item.id"
            :disabled="editingId === item.id"
            :aria-label="`调整第 ${index + 1} 条消息的顺序`"
            title="拖拽调整顺序，方向键可微调"
            @dragstart="beginDrag($event, item)"
            @dragend="endDrag"
            @keydown.up="reorderWithKeyboard($event, item, index, -1)"
            @keydown.down="reorderWithKeyboard($event, item, index, 1)"
          >
            <GripVerticalIcon class="size-3.5" />
          </button>

          <div class="min-w-0 flex-1">
            <template v-if="editingId === item.id">
              <Textarea
                v-model="editText"
                class="max-h-28 min-h-14 resize-none bg-background px-2 py-1.5 text-[13px] leading-[18px]"
                rows="2"
                autofocus
                @keydown="onEditKeydown"
              />
              <div class="mt-1 flex justify-end gap-0.5">
                <button
                  type="button"
                  class="flex size-6 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                  title="取消编辑"
                  @click="cancelEdit"
                >
                  <XIcon class="size-3.5" />
                </button>
                <button
                  type="button"
                  class="flex size-6 items-center justify-center rounded bg-foreground text-background transition-opacity hover:opacity-80 disabled:opacity-30"
                  :disabled="!editText.trim()"
                  title="保存"
                  @click="saveEdit"
                >
                  <CheckIcon class="size-3.5" />
                </button>
              </div>
            </template>
            <template v-else>
              <p
                v-if="item.text"
                class="line-clamp-2 whitespace-pre-wrap break-words text-[12px] leading-[18px] text-foreground"
              >
                {{ item.text }}
              </p>
              <div
                v-if="item.skill_ref"
                class="mt-0.5 flex items-center gap-1 text-[11px] text-muted-foreground"
                :title="item.skill_ref"
              >
                <PackageIcon class="size-3 shrink-0" />
                <span class="truncate">
                  {{ item.skill_ref.split(':').slice(-1)[0] }}
                </span>
              </div>
              <div
                v-if="item.attachments?.length"
                class="mt-0.5 flex items-center gap-1 text-[11px] text-muted-foreground"
              >
                <ImageIcon class="size-3 shrink-0" />
                <span class="truncate">
                  {{ item.attachments.map((attachment) => attachment.name).join("、") }}
                </span>
              </div>
              <div
                v-if="item.browser_elements?.length"
                class="mt-0.5 flex items-center gap-1 text-[11px] text-muted-foreground"
              >
                <MousePointer2Icon class="size-3 shrink-0" />
                <span>{{ item.browser_elements.length }} 个页面元素</span>
              </div>
            </template>
          </div>

          <div
            v-if="editingId !== item.id"
            class="ml-6 flex w-full shrink-0 items-center justify-end gap-0.5 opacity-100 transition-opacity sm:ml-0 sm:w-auto sm:opacity-0 sm:group-hover:opacity-100 sm:group-focus-within:opacity-100"
          >
            <button
              type="button"
              class="flex size-6 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
              title="编辑"
              @click="beginEdit(item)"
            >
              <PencilIcon class="size-3.5" />
            </button>
            <button
              type="button"
              class="flex size-6 items-center justify-center rounded bg-primary/10 text-primary transition-colors hover:bg-primary/20"
              :title="streaming ? '立即发送并中断当前回合' : '立即发送'"
              @click="emit('dispatch', item.id)"
            >
              <SendIcon class="size-3.5" />
            </button>
            <button
              type="button"
              class="flex size-6 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
              title="删除"
              @click="emit('delete', item.id)"
            >
              <Trash2Icon class="size-3.5" />
            </button>
          </div>
    </li>
  </TransitionGroup>
</template>
