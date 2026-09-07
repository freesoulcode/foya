<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { GaugeIcon } from "@lucide/vue";
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@/components/ui/hover-card";
import type { ContextUsage } from "@/lib/api";

const props = withDefaults(
  defineProps<{
    usage?: ContextUsage;
    contextWindow?: number;
  }>(),
  {
    usage: undefined,
    contextWindow: 0,
  }
);
const { locale } = useI18n();

const usedTokens = computed(() => props.usage?.total_tokens ?? 0);
const usagePercent = computed(() =>
  props.contextWindow > 0 ? (usedTokens.value / props.contextWindow) * 100 : 0
);
const cachePercent = computed(() => {
  const input = props.usage?.input_tokens ?? 0;
  return input > 0 ? ((props.usage?.cached_tokens ?? 0) / input) * 100 : 0;
});
const progressWidth = computed(() => `${Math.min(100, usagePercent.value)}%`);
const progressClass = computed(() => {
  if (usagePercent.value >= 90) return "bg-destructive";
  if (usagePercent.value >= 70) return "bg-amber-500";
  return "bg-primary";
});

function compactTokens(value: number) {
  if (value < 1_000) return String(value);
  const divisor = value >= 1_000_000 ? 1_000_000 : 1_000;
  const suffix = value >= 1_000_000 ? "M" : "K";
  const scaled = value / divisor;
  return `${scaled >= 10 ? scaled.toFixed(0) : scaled.toFixed(1).replace(/\.0$/, "")}${suffix}`;
}

function exactTokens(value: number) {
  return new Intl.NumberFormat(locale.value).format(value);
}

function percent(value: number) {
  if (value > 0 && value < 1) return "<1%";
  return `${value.toFixed(value < 10 ? 1 : 0)}%`;
}
</script>

<template>
  <HoverCard v-if="usage" :open-delay="120" :close-delay="100">
    <HoverCardTrigger as-child>
      <button
        type="button"
        class="flex h-7 shrink-0 items-center gap-1 rounded-md px-1.5 text-[11px] tabular-nums text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        :aria-label="$t('View context usage')"
      >
        <GaugeIcon class="size-3.5" />
        <span>
          {{ compactTokens(usedTokens) }}
          <template v-if="contextWindow"> / {{ compactTokens(contextWindow) }}</template>
        </span>
      </button>
    </HoverCardTrigger>

    <HoverCardContent side="top" align="end" :side-offset="8" class="w-64 p-3">
      <div class="flex items-center justify-between gap-3">
        <span class="text-xs font-medium text-foreground">{{ $t("Context usage") }}</span>
        <span v-if="contextWindow" class="text-xs tabular-nums text-muted-foreground">
          {{ percent(usagePercent) }}
        </span>
      </div>

      <div v-if="contextWindow" class="mt-2 h-1.5 overflow-hidden rounded-full bg-muted">
        <div
          class="h-full rounded-full transition-[width] duration-300"
          :class="progressClass"
          :style="{ width: progressWidth }"
        />
      </div>

      <dl class="mt-3 grid grid-cols-[1fr_auto] gap-x-4 gap-y-1.5 text-xs">
        <dt class="text-muted-foreground">{{ $t("Used") }}</dt>
        <dd class="tabular-nums text-foreground">{{ exactTokens(usage.total_tokens) }}</dd>
        <dt class="text-muted-foreground">{{ $t("Input") }}</dt>
        <dd class="tabular-nums text-foreground">{{ exactTokens(usage.input_tokens) }}</dd>
        <dt class="text-muted-foreground">{{ $t("Output") }}</dt>
        <dd class="tabular-nums text-foreground">{{ exactTokens(usage.output_tokens) }}</dd>
        <dt class="text-muted-foreground">{{ $t("Cache hits") }}</dt>
        <dd class="tabular-nums text-foreground">
          {{ exactTokens(usage.cached_tokens) }}
          <span class="text-muted-foreground">· {{ percent(cachePercent) }}</span>
        </dd>
        <template v-if="contextWindow">
          <dt class="text-muted-foreground">{{ $t("Model context window") }}</dt>
          <dd class="tabular-nums text-foreground">{{ compactTokens(contextWindow) }}</dd>
        </template>
      </dl>
    </HoverCardContent>
  </HoverCard>
</template>
