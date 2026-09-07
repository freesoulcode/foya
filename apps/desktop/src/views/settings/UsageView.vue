<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import {
  ActivityIcon,
  CalendarCheck2Icon,
  CalendarDaysIcon,
  ChartNoAxesColumnIncreasingIcon,
  CircleAlertIcon,
  FlameIcon,
  MessageSquareIcon,
  MessagesSquareIcon,
  RefreshCwIcon,
  TrophyIcon,
} from "@lucide/vue";
import SettingsPage from "@/layouts/settings/SettingsPage.vue";
import { Button } from "@/components/ui/button";
import { ButtonGroup } from "@/components/ui/button-group";
import { Skeleton } from "@/components/ui/skeleton";
import { api, type DailyUsage, type UsageStatistics } from "@/lib/api";

type RangeDays = 7 | 30;
const { locale, t } = useI18n();

interface HeatmapDay extends DailyUsage {
  column: number;
  row: number;
  level: number;
}

interface TokenChartPoint extends DailyUsage {
  index: number;
  x: number;
  y: number;
}

interface HeatmapTooltip {
  day: HeatmapDay;
  x: number;
  y: number;
}

const CHART_WIDTH = 720;
const CHART_HEIGHT = 220;
const CHART_PADDING_X = 28;
const CHART_TOP = 22;
const CHART_BOTTOM = 30;

const rangeDays = ref<RangeDays>(30);
const statistics = ref<UsageStatistics | null>(null);
const loading = ref(true);
const refreshing = ref(false);
const error = ref("");
const selectedTokenPoint = ref<TokenChartPoint | null>(null);
const heatmapPanel = ref<HTMLElement | null>(null);
const heatmapTooltip = ref<HeatmapTooltip | null>(null);
let requestSequence = 0;

const numberFormatter = computed(() => new Intl.NumberFormat(locale.value));
const compactNumberFormatter = computed(() =>
  new Intl.NumberFormat(locale.value, {
    notation: "compact",
    maximumFractionDigits: 1,
  })
);
const dateFormatter = computed(() =>
  new Intl.DateTimeFormat(locale.value, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
);

const metrics = computed(() => {
  const value = statistics.value;
  return [
    {
      key: "tokens",
      label: t("Token usage"),
      icon: FlameIcon,
      value: formatCompactNumber(value?.total_tokens ?? 0),
      detail: value
        ? t("Input {input} · Output {output}", {
            input: formatCompactNumber(value.input_tokens),
            output: formatCompactNumber(value.output_tokens),
          })
        : "",
    },
    {
      key: "sessions",
      label: t("Chats"),
      icon: MessagesSquareIcon,
      value: formatNumber(value?.session_count ?? 0),
      detail: "",
    },
    {
      key: "messages",
      label: t("Messages"),
      icon: MessageSquareIcon,
      value: formatNumber(value?.message_count ?? 0),
      detail: "",
    },
    {
      key: "active-days",
      label: t("Active days"),
      icon: CalendarDaysIcon,
      value: formatNumber(value?.active_days ?? 0),
      detail: t("Last {days} days", { days: rangeDays.value }),
    },
    {
      key: "streak",
      label: t("Current streak"),
      icon: CalendarCheck2Icon,
      value: formatNumber(value?.current_streak ?? 0),
      detail: "",
    },
    {
      key: "model",
      label: t("Most used model"),
      icon: ActivityIcon,
      value: value?.most_used_model || t("No data"),
      detail: value?.most_used_model
        ? t("{share}% of token usage", {
            share: value.most_used_model_share,
          })
        : "",
      compact: true,
    },
  ];
});

const heatmapDays = computed<HeatmapDay[]>(() => {
  const activity = statistics.value?.activity ?? [];
  if (activity.length === 0) return [];
  const scores = activity.map(activityScore);
  const maxScore = Math.max(...scores, 1);
  const firstDate = localDate(activity[0].date);
  const leadingDays = firstDate.getDay();

  return activity.map((day, index) => {
    const position = leadingDays + index;
    const score = scores[index];
    return {
      ...day,
      column: Math.floor(position / 7) + 1,
      row: (position % 7) + 1,
      level: score === 0 ? 0 : Math.max(1, Math.ceil((score / maxScore) * 4)),
    };
  });
});

const heatmapColumns = computed(() =>
  Math.max(1, ...heatmapDays.value.map((day) => day.column))
);

const selectedDailyUsage = computed(() =>
  (statistics.value?.activity ?? []).slice(-rangeDays.value)
);

const maxDailyTokens = computed(() =>
  Math.max(0, ...selectedDailyUsage.value.map((day) => day.token_count))
);

const tokenChartPoints = computed<TokenChartPoint[]>(() => {
  const days = selectedDailyUsage.value;
  if (days.length === 0) return [];
  const plotWidth = CHART_WIDTH - CHART_PADDING_X * 2;
  const plotHeight = CHART_HEIGHT - CHART_TOP - CHART_BOTTOM;
  const maxTokens = Math.max(maxDailyTokens.value, 1);
  return days.map((day, index) => ({
    ...day,
    index,
    x:
      days.length === 1
        ? CHART_WIDTH / 2
        : CHART_PADDING_X + (index / (days.length - 1)) * plotWidth,
    y: CHART_TOP + (1 - day.token_count / maxTokens) * plotHeight,
  }));
});

const tokenLinePath = computed(() =>
  tokenChartPoints.value
    .map((point, index) => `${index === 0 ? "M" : "L"} ${point.x} ${point.y}`)
    .join(" ")
);

const tokenAreaPath = computed(() => {
  const points = tokenChartPoints.value;
  if (points.length === 0) return "";
  const baseline = CHART_HEIGHT - CHART_BOTTOM;
  return [
    `M ${points[0].x} ${baseline}`,
    ...points.map((point) => `L ${point.x} ${point.y}`),
    `L ${points[points.length - 1].x} ${baseline}`,
    "Z",
  ].join(" ");
});

const tokenChartDetail = computed(() => {
  const days = selectedDailyUsage.value;
  const total = days.reduce((sum, day) => sum + day.token_count, 0);
  return {
    label: t("Daily average"),
    tokens: days.length > 0 ? Math.round(total / days.length) : 0,
  };
});

const tokenChartDateLabels = computed(() => {
  const days = selectedDailyUsage.value;
  if (days.length === 0) return [];
  const indexes = [...new Set([0, Math.floor((days.length - 1) / 2), days.length - 1])];
  return indexes.map((index) => ({
    index,
    label: new Intl.DateTimeFormat(locale.value, {
      month: "numeric",
      day: "numeric",
    }).format(localDate(days[index].date)),
  }));
});

const modelRanking = computed(() => statistics.value?.model_usage?.slice(0, 8) ?? []);

function formatNumber(value: number) {
  return numberFormatter.value.format(value);
}

function formatCompactNumber(value: number) {
  if (value < 10_000) return formatNumber(value);
  return compactNumberFormatter.value.format(value);
}

function localDate(date: string) {
  return new Date(`${date}T00:00:00`);
}

function activityScore(day: DailyUsage) {
  return day.message_count > 0 ? day.message_count : day.token_count > 0 ? 1 : 0;
}

function heatmapClass(level: number) {
  return [
    "border-border/60 bg-muted/60",
    "border-teal-300/50 bg-teal-200 dark:border-teal-900 dark:bg-teal-950",
    "border-teal-400/50 bg-teal-300 dark:border-teal-800 dark:bg-teal-800",
    "border-emerald-500/50 bg-emerald-400 dark:border-emerald-700 dark:bg-emerald-600",
    "border-emerald-600 bg-emerald-600 dark:border-emerald-500 dark:bg-emerald-500",
  ][level];
}

function heatmapLabel(day: HeatmapDay) {
  const date = dateFormatter.value.format(localDate(day.date));
  return t("{date}: {messages} messages, {tokens} tokens", {
    date,
    messages: formatNumber(day.message_count),
    tokens: formatNumber(day.token_count),
  });
}

function updateTokenHover(event: MouseEvent) {
  const points = tokenChartPoints.value;
  if (points.length === 0) return;
  const bounds = (event.currentTarget as SVGElement).getBoundingClientRect();
  const chartX = ((event.clientX - bounds.left) / bounds.width) * CHART_WIDTH;
  const plotWidth = CHART_WIDTH - CHART_PADDING_X * 2;
  const ratio = Math.min(1, Math.max(0, (chartX - CHART_PADDING_X) / plotWidth));
  const index = Math.round(ratio * (points.length - 1));
  selectedTokenPoint.value = points[index];
}

function showHeatmapTooltip(day: HeatmapDay, event: MouseEvent | FocusEvent) {
  const panel = heatmapPanel.value;
  const target = event.currentTarget as HTMLElement;
  if (!panel) return;
  const panelBounds = panel.getBoundingClientRect();
  const targetBounds = target.getBoundingClientRect();
  const rawX = targetBounds.left + targetBounds.width / 2 - panelBounds.left;
  heatmapTooltip.value = {
    day,
    x: Math.min(Math.max(rawX, 112), Math.max(112, panelBounds.width - 112)),
    y: targetBounds.top - panelBounds.top - 8,
  };
}

function hideHeatmapTooltip() {
  heatmapTooltip.value = null;
}

function modelBarClass(index: number) {
  return [
    "bg-teal-600 dark:bg-teal-500",
    "bg-sky-500",
    "bg-amber-500",
    "bg-muted-foreground/65",
  ][Math.min(index, 3)];
}

async function loadStatistics() {
  const sequence = ++requestSequence;
  if (statistics.value) refreshing.value = true;
  else loading.value = true;
  error.value = "";
  try {
    const result = await api.loadUsageStatistics(rangeDays.value);
    if (sequence === requestSequence) statistics.value = result;
  } catch (cause) {
    if (sequence === requestSequence) error.value = String(cause);
  } finally {
    if (sequence === requestSequence) {
      loading.value = false;
      refreshing.value = false;
    }
  }
}

function selectRange(days: RangeDays) {
  if (days === rangeDays.value) return;
  rangeDays.value = days;
  selectedTokenPoint.value = null;
  void loadStatistics();
}

onMounted(loadStatistics);
</script>

<template>
  <SettingsPage
    :title="$t('Usage')"
    :description="$t('View recent local usage and activity trends.')"
    content-class="min-h-0 flex-1 overflow-y-auto pb-2 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
  >
    <template #actions>
      <ButtonGroup :aria-label="$t('Statistics period')">
        <Button
          v-for="days in ([7, 30] as const)"
          :key="days"
          variant="outline"
          size="sm"
          :class="[
            'min-w-20 shadow-none',
            rangeDays === days
              ? 'bg-accent text-accent-foreground'
              : 'text-muted-foreground',
          ]"
          :aria-pressed="rangeDays === days"
          @click="selectRange(days)"
        >
          {{ $t("Last {days} days", { days }) }}
        </Button>
      </ButtonGroup>
    </template>

    <div class="flex max-w-5xl flex-col gap-5">
      <div
        v-if="error"
        role="alert"
        class="flex items-center justify-between gap-4 rounded-lg border border-destructive/30 bg-destructive/5 px-4 py-3 text-sm text-destructive"
      >
        <span class="flex min-w-0 items-center gap-2">
          <CircleAlertIcon class="size-4 shrink-0" />
          <span class="truncate">{{ $t("Failed to load statistics: {error}", { error }) }}</span>
        </span>
        <Button variant="outline" size="sm" @click="loadStatistics">
          <RefreshCwIcon />
          {{ $t("Retry") }}
        </Button>
      </div>

      <div
        v-if="loading"
        class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3"
      >
        <div
          v-for="index in 6"
          :key="index"
          class="min-h-32 rounded-lg border bg-card p-5"
        >
          <Skeleton class="h-4 w-24" />
          <Skeleton class="mt-6 h-8 w-28" />
          <Skeleton class="mt-3 h-3 w-36" />
        </div>
      </div>

      <div
        v-else
        class="relative grid gap-3 sm:grid-cols-2 lg:grid-cols-3"
        :aria-busy="refreshing"
      >
        <article
          v-for="metric in metrics"
          :key="metric.key"
          class="flex min-h-32 min-w-0 flex-col rounded-lg border bg-card p-5"
        >
          <div class="flex items-center gap-2 text-sm font-medium text-muted-foreground">
            <component :is="metric.icon" class="size-4" />
            <span>{{ metric.label }}</span>
          </div>
          <div class="mt-5 min-w-0">
            <p
              class="truncate font-semibold tabular-nums text-foreground"
              :class="metric.compact ? 'text-lg sm:text-xl' : 'text-2xl sm:text-3xl'"
              :title="metric.value"
            >
              {{ metric.value }}
            </p>
            <p v-if="metric.detail" class="mt-1.5 truncate text-xs text-muted-foreground">
              {{ metric.detail }}
            </p>
          </div>
        </article>
        <div
          v-if="refreshing"
          class="absolute inset-0 grid place-items-center bg-background/55 backdrop-blur-[1px]"
        >
          <RefreshCwIcon class="size-5 animate-spin text-muted-foreground" :aria-label="$t('Refreshing')" />
        </div>
      </div>

      <div
        v-if="!loading"
        class="order-2 grid gap-5 xl:grid-cols-[minmax(0,1.55fr)_minmax(19rem,1fr)]"
      >
        <section class="min-w-0 rounded-lg border bg-background p-5">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 class="flex items-center gap-2 text-sm font-medium">
                <ChartNoAxesColumnIncreasingIcon class="size-4 text-muted-foreground" />
                {{ $t("Daily token trend") }}
              </h3>
              <p class="mt-1 text-xs text-muted-foreground">{{ $t("Last {days} days", { days: rangeDays }) }}</p>
            </div>
            <div class="text-right">
              <p class="text-xs text-muted-foreground">{{ tokenChartDetail.label }}</p>
              <p class="mt-0.5 text-sm font-semibold tabular-nums">
                {{ formatCompactNumber(tokenChartDetail.tokens) }}
                <span class="font-normal text-muted-foreground">Tokens</span>
              </p>
            </div>
          </div>

          <div v-if="tokenChartPoints.length && maxDailyTokens > 0" class="mt-4">
            <div
              class="relative"
              @mouseleave="selectedTokenPoint = null"
            >
              <svg
                class="block w-full overflow-visible"
                :viewBox="`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`"
                role="img"
                :aria-label="$t('Daily token usage trend for the last {days} days', { days: rangeDays })"
                @mousemove="updateTokenHover"
              >
                <line
                  v-for="fraction in [0, 0.5, 1]"
                  :key="fraction"
                  :x1="CHART_PADDING_X"
                  :x2="CHART_WIDTH - CHART_PADDING_X"
                  :y1="CHART_TOP + fraction * (CHART_HEIGHT - CHART_TOP - CHART_BOTTOM)"
                  :y2="CHART_TOP + fraction * (CHART_HEIGHT - CHART_TOP - CHART_BOTTOM)"
                  class="stroke-border"
                  stroke-width="1"
                />
                <path :d="tokenAreaPath" class="fill-teal-500/10 stroke-none" />
                <path
                  :d="tokenLinePath"
                  class="fill-none stroke-teal-600 dark:stroke-teal-400"
                  stroke-width="3"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                />
                <circle
                  v-for="point in tokenChartPoints"
                  :key="point.date"
                  :cx="point.x"
                  :cy="point.y"
                  :r="rangeDays === 7 ? 4 : 2.5"
                  class="fill-background stroke-teal-600 dark:stroke-teal-400"
                  stroke-width="2"
                />
                <template v-if="selectedTokenPoint">
                  <line
                    :x1="selectedTokenPoint.x"
                    :x2="selectedTokenPoint.x"
                    :y1="CHART_TOP"
                    :y2="CHART_HEIGHT - CHART_BOTTOM"
                    class="stroke-muted-foreground/50"
                    stroke-dasharray="4 4"
                  />
                  <circle
                    :cx="selectedTokenPoint.x"
                    :cy="selectedTokenPoint.y"
                    r="5.5"
                    class="fill-background stroke-foreground"
                    stroke-width="2.5"
                  />
                </template>
              </svg>

              <div
                v-if="selectedTokenPoint"
                role="tooltip"
                class="pointer-events-none absolute z-20 min-w-44 rounded-md border bg-popover px-3 py-2 text-popover-foreground shadow-lg"
                :class="[
                  selectedTokenPoint.y < 72
                    ? 'mt-3'
                    : '-mt-3 -translate-y-full',
                  selectedTokenPoint.x > CHART_WIDTH * 0.72
                    ? '-ml-3 -translate-x-full'
                    : selectedTokenPoint.x < CHART_WIDTH * 0.28
                      ? 'ml-3'
                      : '-translate-x-1/2',
                ]"
                :style="{
                  left: `${(selectedTokenPoint.x / CHART_WIDTH) * 100}%`,
                  top: `${(selectedTokenPoint.y / CHART_HEIGHT) * 100}%`,
                }"
              >
                <p class="text-xs font-medium">
                  {{ dateFormatter.format(localDate(selectedTokenPoint.date)) }}
                </p>
                <p class="mt-1.5 text-sm font-semibold tabular-nums">
                  {{ formatNumber(selectedTokenPoint.token_count) }}
                  <span class="font-normal text-muted-foreground">Tokens</span>
                </p>
                <p class="mt-0.5 text-xs tabular-nums text-muted-foreground">
                  {{ $t("{count} messages", { count: formatNumber(selectedTokenPoint.message_count) }) }}
                </p>
              </div>
            </div>
            <div class="flex justify-between px-1 text-[11px] tabular-nums text-muted-foreground">
              <span v-for="item in tokenChartDateLabels" :key="item.index">
                {{ item.label }}
              </span>
            </div>
          </div>
          <div
            v-else
            class="mt-5 grid h-48 place-items-center rounded-md border border-dashed text-sm text-muted-foreground"
          >
            {{ $t("No token data") }}
          </div>
        </section>

        <section class="min-w-0 rounded-lg border bg-background p-5">
          <div>
            <h3 class="flex items-center gap-2 text-sm font-medium">
              <TrophyIcon class="size-4 text-muted-foreground" />
              {{ $t("Model usage ranking") }}
            </h3>
            <p class="mt-1 text-xs text-muted-foreground">{{ $t("Last {days} days", { days: rangeDays }) }}</p>
          </div>

          <ol v-if="modelRanking.length" class="mt-5 space-y-4">
            <li
              v-for="(item, index) in modelRanking"
              :key="item.model"
              class="grid min-w-0 grid-cols-[1.5rem_minmax(0,1fr)_auto] items-start gap-x-2"
            >
              <span class="pt-0.5 text-xs font-medium tabular-nums text-muted-foreground">
                {{ index + 1 }}
              </span>
              <div class="min-w-0">
                <p class="truncate text-sm font-medium" :title="item.model">{{ item.model }}</p>
                <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-muted">
                  <div
                    class="h-full rounded-full"
                    :class="modelBarClass(index)"
                    :style="{ width: `${Math.max(item.share > 0 ? 2 : 0, item.share)}%` }"
                  />
                </div>
                <p class="mt-1.5 text-xs tabular-nums text-muted-foreground">
                  {{ $t("{count} requests", { count: formatNumber(item.request_count) }) }}
                </p>
              </div>
              <div class="pl-2 text-right">
                <p class="text-sm font-medium tabular-nums">{{ formatCompactNumber(item.token_count) }}</p>
                <p class="mt-1 text-xs tabular-nums text-muted-foreground">{{ item.share }}%</p>
              </div>
            </li>
          </ol>
          <div
            v-else
            class="mt-5 grid h-48 place-items-center rounded-md border border-dashed text-sm text-muted-foreground"
          >
            {{ $t("No model usage") }}
          </div>
        </section>
      </div>

      <section ref="heatmapPanel" class="relative order-1 rounded-lg border bg-background p-5">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 class="text-sm font-medium">{{ $t("Activity heatmap") }}</h3>
            <p class="mt-1 text-xs text-muted-foreground">{{ $t("Past year") }}</p>
          </div>
          <div class="flex items-center gap-2 text-xs text-muted-foreground" :aria-label="$t('Activity legend')">
            <span>{{ $t("Less") }}</span>
            <span
              v-for="level in [0, 1, 2, 3, 4]"
              :key="level"
              class="size-3 rounded-[3px] border"
              :class="heatmapClass(level)"
            />
            <span>{{ $t("More") }}</span>
          </div>
        </div>

        <div v-if="loading" class="mt-5">
          <Skeleton class="h-28 w-full" />
        </div>
        <div v-else class="mt-5 overflow-x-auto pb-1">
          <div
            class="grid min-w-[42rem] gap-[3px]"
            :style="{
              gridTemplateRows: 'repeat(7, minmax(0, 1fr))',
              gridTemplateColumns: `repeat(${heatmapColumns}, minmax(9px, 1fr))`,
            }"
          >
            <div
              v-for="day in heatmapDays"
              :key="day.date"
              class="aspect-square min-w-0 rounded-[3px] border"
              :class="heatmapClass(day.level)"
              :style="{ gridColumn: day.column, gridRow: day.row }"
              :aria-label="heatmapLabel(day)"
              @mouseenter="showHeatmapTooltip(day, $event)"
              @mouseleave="hideHeatmapTooltip"
            />
          </div>
        </div>

        <div
          v-if="heatmapTooltip"
          class="pointer-events-none absolute z-20 min-w-48 -translate-x-1/2 -translate-y-full rounded-md bg-foreground px-3 py-2 text-background shadow-lg"
          :style="{ left: `${heatmapTooltip.x}px`, top: `${heatmapTooltip.y}px` }"
          role="tooltip"
        >
          <p class="text-xs font-medium">
            {{ dateFormatter.format(localDate(heatmapTooltip.day.date)) }}
          </p>
          <div class="mt-1.5 flex items-center gap-3 text-xs tabular-nums">
            <span>{{ $t("{count} messages", { count: formatNumber(heatmapTooltip.day.message_count) }) }}</span>
            <span>{{ formatNumber(heatmapTooltip.day.token_count) }} Tokens</span>
          </div>
        </div>
      </section>
    </div>
  </SettingsPage>
</template>
