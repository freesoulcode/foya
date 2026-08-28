<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { cn } from "@/lib/utils";
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@/components/ui/hover-card";
import { ScrollArea } from "@/components/ui/scroll-area";

export interface TurnPoint {
  label: string;
}

const props = defineProps<{
  turns: TurnPoint[];
  active: number;
}>();

const emit = defineEmits<{
  select: [index: number];
}>();

// HoverCard 自身管理 hover 显隐;点击后主动关闭。
const open = ref(false);

// 指示器内部滚动容器,用于把当前回合点保持在可视区。
const railRef = ref<HTMLElement | null>(null);
const dotRefs = ref<HTMLElement[]>([]);

const points = computed(() =>
  Array.from({ length: Math.max(0, props.turns.length) }, (_, i) => ({
    index: i,
    label: props.turns[i]?.label || `第 ${i + 1} 回合`,
  }))
);

// ScrollArea 的 viewport 使用 100% 高度，因此必须给根节点明确高度。
// 每行 36px，加上下 8px 内边距，最多展示 8 行。
const pickerHeight = computed(() => Math.min(points.value.length * 36 + 8, 296));

function setDotRef(el: HTMLElement | null, i: number) {
  if (el) dotRefs.value[i] = el;
}

// 当前回合变化时,把对应圆点滚动到 rail 中部(回合超出可视高度时)。
watch(
  () => props.active,
  async () => {
    await nextTick();
    const rail = railRef.value;
    const dot = dotRefs.value[props.active];
    if (!rail || !dot) return;
    const target = dot.offsetTop - rail.clientHeight / 2 + dot.clientHeight / 2;
    rail.scrollTo({ top: target, behavior: "smooth" });
  }
);

function onSelect(i: number) {
  emit("select", i);
  open.value = false;
}
</script>

<template>
  <HoverCard v-if="points.length > 0" v-model:open="open" :open-delay="80" :close-delay="120">
    <HoverCardTrigger as-child>
      <div
        class="rounded-full border border-border/50 bg-card/60 px-1 py-2 shadow-sm backdrop-blur"
        aria-label="对话回合导航"
      >
        <div
          ref="railRef"
          class="no-scrollbar flex max-h-56 flex-col items-center gap-1.5 overflow-y-auto"
        >
          <span
            v-for="p in points"
            :key="p.index"
            :ref="(el) => setDotRef(el as HTMLElement | null, p.index)"
            :class="
              cn(
                'shrink-0 rounded-full transition-all duration-200',
                p.index === active
                  ? 'size-2 bg-foreground'
                  : 'size-1.5 bg-muted-foreground/25 hover:bg-muted-foreground/50'
              )
            "
          />
        </div>
      </div>
    </HoverCardTrigger>

    <HoverCardContent
      side="right"
      align="center"
      :side-offset="8"
      class="w-64 p-0"
    >
      <ScrollArea :style="{ height: `${pickerHeight}px` }">
        <ul class="py-1">
          <li v-for="p in points" :key="p.index">
            <button
              type="button"
              class="flex w-full items-center gap-2.5 px-3 py-2 text-left text-sm transition-colors hover:bg-muted"
              :class="
                p.index === active
                  ? 'bg-muted font-medium text-foreground'
                  : 'text-muted-foreground'
              "
              @click="onSelect(p.index)"
            >
              <span
                :class="
                  cn(
                    'size-1.5 shrink-0 rounded-full',
                    p.index === active ? 'bg-foreground' : 'bg-muted-foreground/40'
                  )
                "
              />
              <span class="truncate">{{ p.label }}</span>
            </button>
          </li>
        </ul>
      </ScrollArea>
    </HoverCardContent>
  </HoverCard>
</template>
