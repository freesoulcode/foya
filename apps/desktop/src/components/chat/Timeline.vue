<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
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
const { t } = useI18n();

const emit = defineEmits<{
  select: [index: number];
}>();

// HoverCard manages hover visibility; selection closes it explicitly.
const open = ref(false);

// Keep the active turn visible inside the indicator rail.
const railRef = ref<HTMLElement | null>(null);
const dotRefs = ref<HTMLElement[]>([]);

const points = computed(() =>
  Array.from({ length: Math.max(0, props.turns.length) }, (_, i) => ({
    index: i,
    label: props.turns[i]?.label || t("Turn {number}", { number: i + 1 }),
  }))
);

// ScrollArea needs an explicit root height. Show at most eight 36px rows.
const pickerHeight = computed(() => Math.min(points.value.length * 36 + 8, 296));

function setDotRef(el: HTMLElement | null, i: number) {
  if (el) dotRefs.value[i] = el;
}

// Center the active dot when it moves outside the visible rail.
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
        :aria-label="$t('Chat turn navigation')"
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
