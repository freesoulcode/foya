<script setup lang="ts">
import { computed, reactive, watch } from "vue";
import {
  CheckIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  MessageSquareTextIcon,
} from "@lucide/vue";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { useKernel } from "@/composables/useKernel";
import type { PendingQuestionBatch } from "@/lib/api";

interface Draft {
  index: number;
  answers: Record<string, string>;
  custom: Record<string, boolean>;
  expanded: boolean;
  submitting: boolean;
  error: string;
}

const props = defineProps<{
  batch: PendingQuestionBatch | null;
}>();

const emit = defineEmits<{
  (event: "expanded-change", expanded: boolean): void;
}>();

const { answerQuestions, cancelQuestions } = useKernel();
const drafts = reactive<Record<string, Draft>>({});

function ensureDraft(batch: PendingQuestionBatch): Draft {
  if (!drafts[batch.id]) {
    drafts[batch.id] = {
      index: 0,
      answers: {},
      custom: {},
      expanded: true,
      submitting: false,
      error: "",
    };
  }
  return drafts[batch.id];
}

const draft = computed(() => props.batch ? ensureDraft(props.batch) : null);
const current = computed(() => {
  if (!props.batch || !draft.value) return null;
  return props.batch.questions[draft.value.index] ?? null;
});
const canAdvance = computed(() =>
  Boolean(current.value && draft.value?.answers[current.value.id]?.trim())
);
const isFinal = computed(() =>
  Boolean(
    props.batch &&
    draft.value &&
    draft.value.index === props.batch.questions.length - 1
  )
);
const allAnswered = computed(() =>
  Boolean(
    props.batch?.questions.every(
      (question) => draft.value?.answers[question.id]?.trim()
    )
  )
);

watch(
  () => [props.batch?.id, draft.value?.expanded] as const,
  () => emit("expanded-change", Boolean(props.batch && draft.value?.expanded)),
  { immediate: true }
);

function choose(value: string) {
  if (!current.value || !draft.value) return;
  draft.value.custom[current.value.id] = false;
  draft.value.answers[current.value.id] = value;
}

function chooseCustom() {
  if (!current.value || !draft.value) return;
  draft.value.custom[current.value.id] = true;
  draft.value.answers[current.value.id] = "";
}

function previous() {
  if (draft.value && draft.value.index > 0) draft.value.index--;
}

function next() {
  if (draft.value && canAdvance.value && !isFinal.value) draft.value.index++;
}

function defer() {
  if (draft.value) draft.value.expanded = false;
}

function expand() {
  if (draft.value) draft.value.expanded = true;
}

async function submit() {
  if (!props.batch || !draft.value || !allAnswered.value || draft.value.submitting) return;
  const batch = props.batch;
  const state = draft.value;
  state.submitting = true;
  state.error = "";
  try {
    await answerQuestions(
      batch.session_id,
      batch.id,
      batch.questions.map((question) => ({
        question_id: question.id,
        value: state.answers[question.id].trim(),
      }))
    );
    delete drafts[batch.id];
  } catch (reason) {
    state.submitting = false;
    state.error = String(reason);
  }
}

async function cancel() {
  if (!props.batch || !draft.value || draft.value.submitting) return;
  const batch = props.batch;
  const state = draft.value;
  state.submitting = true;
  state.error = "";
  try {
    await cancelQuestions(batch.session_id, batch.id);
    delete drafts[batch.id];
  } catch (reason) {
    state.submitting = false;
    state.error = String(reason);
  }
}
</script>

<template>
  <div
    v-if="batch && draft"
    class="shrink-0 px-4 pb-4 pt-2"
  >
    <div class="mx-auto w-full max-w-3xl overflow-hidden rounded-2xl border border-input bg-card shadow-xs">
      <div
        v-if="!draft.expanded"
        class="flex min-h-12 items-center gap-3 px-4 py-2"
      >
        <MessageSquareTextIcon class="size-4 shrink-0 text-primary" />
        <div class="min-w-0 flex-1">
          <p class="truncate text-sm font-medium">Agent 正在等待你的回答</p>
          <p class="text-xs text-muted-foreground">
            {{ batch.questions.length }} 个问题
          </p>
        </div>
        <Button size="sm" variant="outline" @click="expand">回答</Button>
      </div>

      <div v-else class="flex max-h-[45svh] min-h-0 flex-col">
      <div class="min-h-0 flex-1 overflow-y-auto px-4 py-3">
        <div class="space-y-3">
          <div class="flex items-start gap-3">
            <MessageSquareTextIcon class="mt-0.5 size-4 shrink-0 text-primary" />
            <div class="min-w-0 flex-1">
              <div class="flex items-center justify-between gap-3">
                <p class="text-sm font-medium">需要你的输入</p>
                <span class="shrink-0 text-xs tabular-nums text-muted-foreground">
                  {{ draft.index + 1 }} / {{ batch.questions.length }}
                </span>
              </div>
              <p v-if="current" class="mt-2 text-sm leading-6">{{ current.question }}</p>
              <p
                v-if="current?.description"
                class="mt-0.5 text-xs leading-5 text-muted-foreground"
              >
                {{ current.description }}
              </p>
            </div>
          </div>

          <div v-if="current?.options?.length" class="grid gap-1.5 sm:grid-cols-2">
            <button
              v-for="option in current.options"
              :key="option.label"
              type="button"
              :class="[
                'flex min-h-10 items-start gap-2 rounded-md border px-2.5 py-2 text-left transition-colors',
                !draft.custom[current.id] && draft.answers[current.id] === option.label
                  ? 'border-primary bg-primary/5'
                  : 'border-border hover:bg-muted/70',
              ]"
              @click="choose(option.label)"
            >
              <span
                :class="[
                  'mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border',
                  !draft.custom[current.id] && draft.answers[current.id] === option.label
                    ? 'border-primary bg-primary text-primary-foreground'
                    : 'border-muted-foreground/50',
                ]"
              >
                <CheckIcon
                  v-if="!draft.custom[current.id] && draft.answers[current.id] === option.label"
                  class="size-3"
                />
              </span>
              <span class="min-w-0">
                <span class="flex items-center gap-1.5 text-sm font-medium">
                  {{ option.label }}
                  <span
                    v-if="option.recommended"
                    class="text-xs font-normal text-primary"
                  >推荐</span>
                </span>
                <span
                  v-if="option.description"
                  class="mt-0.5 block text-xs leading-4 text-muted-foreground"
                >{{ option.description }}</span>
              </span>
            </button>

            <button
              v-if="current.allow_custom"
              type="button"
              :class="[
                'flex min-h-10 items-center gap-2 rounded-md border px-2.5 py-2 text-left text-sm transition-colors',
                draft.custom[current.id]
                  ? 'border-primary bg-primary/5'
                  : 'border-border hover:bg-muted/70',
              ]"
              @click="chooseCustom"
            >
              <span
                :class="[
                  'flex size-4 shrink-0 items-center justify-center rounded-full border',
                  draft.custom[current.id]
                    ? 'border-primary bg-primary text-primary-foreground'
                    : 'border-muted-foreground/50',
                ]"
              >
                <CheckIcon v-if="draft.custom[current.id]" class="size-3" />
              </span>
              其他
            </button>
          </div>

          <Textarea
            v-if="
              current?.allow_custom &&
              (!current.options?.length || draft.custom[current.id])
            "
            v-model="draft.answers[current.id]"
            class="min-h-20 resize-none"
            placeholder="输入你的回答"
            rows="2"
          />

          <p v-if="draft.error" class="text-xs text-destructive">{{ draft.error }}</p>
        </div>
      </div>

        <div class="flex shrink-0 items-center justify-between gap-2 border-t border-border px-4 py-2.5">
        <Button
          variant="ghost"
          size="sm"
          :disabled="draft.submitting"
          @click="defer"
        >
          稍后回答
        </Button>
        <div class="flex items-center gap-1.5">
          <Button
            variant="ghost"
            size="icon"
            :disabled="draft.index === 0 || draft.submitting"
            title="上一题"
            @click="previous"
          >
            <ChevronLeftIcon class="size-4" />
          </Button>
          <Button
            variant="outline"
            size="sm"
            :disabled="draft.submitting"
            @click="cancel"
          >
            终止提问
          </Button>
          <Button
            v-if="isFinal"
            size="sm"
            :disabled="!allAnswered || draft.submitting"
            @click="submit"
          >
            提交回答
          </Button>
          <Button
            v-else
            size="icon"
            :disabled="!canAdvance || draft.submitting"
            title="下一题"
            @click="next"
          >
            <ChevronRightIcon class="size-4" />
          </Button>
        </div>
        </div>
      </div>
    </div>
  </div>
</template>
