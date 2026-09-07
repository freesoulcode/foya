<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { openUrl } from "@tauri-apps/plugin-opener";
import QRCode from "qrcode";
import {
  ArrowLeftIcon,
  ChevronRightIcon,
  ExternalLinkIcon,
  EyeIcon,
  EyeOffIcon,
  KeyRoundIcon,
  LoaderCircleIcon,
  QrCodeIcon,
  RefreshCwIcon,
  SaveIcon,
  Trash2Icon,
} from "@lucide/vue";
import {
  api,
  type ConnectionConfig,
  type FeishuBotSettings,
  type FeishuRegistrationInput,
  type FeishuRegistrationState,
  type FeishuBotUpdate,
  type ProjectInfo,
} from "@/lib/api";
import { Button } from "@/components/ui/button";
import { ButtonGroup } from "@/components/ui/button-group";
import { Checkbox } from "@/components/ui/checkbox";
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { useLocale } from "@/i18n";

const props = defineProps<{
  channel: FeishuBotSettings | null;
  creating?: boolean;
}>();
const { t } = useI18n();
const { resolvedLocale } = useLocale();

const emit = defineEmits<{
  saved: [channel: FeishuBotSettings];
  deleted: [channelId: string];
  refresh: [];
}>();

const emptySettings = (): FeishuBotSettings => ({
  id: "",
  kind: "feishu",
  name: t("Feishu Bot"),
  locale: resolvedLocale.value,
  enabled: false,
  app_id: "",
  has_app_secret: false,
  approval_mode: "auto",
  allowed_users: [],
  allowed_chats: [],
  allow_all: false,
  status: "stopped",
});

const settings = ref<FeishuBotSettings>(emptySettings());
const appSecret = ref("");
const allowedUsers = ref("");
const allowedChats = ref("");
const connections = ref<ConnectionConfig[]>([]);
const projects = ref<ProjectInfo[]>([]);
const showSecret = ref(false);
const loading = ref(false);
const saving = ref(false);
const deleting = ref(false);
const deleteConfirmOpen = ref(false);
const error = ref("");
const creationMode = ref<"choose" | "manual">("choose");
const registrationOpen = ref(false);
const registration = ref<FeishuRegistrationState>();
const registrationQRCode = ref("");
const registrationError = ref("");
let registrationPollTimer: ReturnType<typeof setTimeout> | undefined;
let registrationGeneration = 0;

const registrationActive = computed(() =>
  ["starting", "pending", "completing"].includes(
    registration.value?.status ?? ""
  )
);

const registrationStatusLabel = computed(() => {
  if (!registration.value && registrationError.value) return t("Unable to start creation");
  switch (registration.value?.status) {
    case "pending":
      return t("Waiting for QR code confirmation");
    case "completing":
      return t("Creating and connecting Bot");
    case "completed":
      return t("Bot created");
    case "denied":
      return t("Authorization denied");
    case "expired":
      return t("QR code expired");
    case "cancelled":
      return t("Cancelled");
    case "error":
      return t("Creation failed");
    default:
      return t("Generating QR code");
  }
});

const languageConnections = computed(() =>
  connections.value.filter(
    (item): item is ConnectionConfig & { id: string } =>
      item.type === "language" && typeof item.id === "string" && item.id.length > 0
  )
);

const statusLabel = computed(() => {
  if (props.creating) return t("Not saved");
  if (settings.value.status === "running") return t("Running");
  if (settings.value.status === "error") return t("Connection error");
  return t("Stopped");
});

const statusClass = computed(() => {
  if (settings.value.status === "running") return "bg-emerald-500";
  if (settings.value.status === "error") return "bg-destructive";
  return "bg-muted-foreground/50";
});

function parseIDs(value: string): string[] {
  return [...new Set(
    value
      .split(/[\s,]+/)
      .map((item) => item.trim())
      .filter(Boolean)
  )];
}

function applySettings(value: FeishuBotSettings | null) {
  settings.value = value
    ? {
        ...emptySettings(),
        ...value,
        allowed_users: value.allowed_users ?? [],
        allowed_chats: value.allowed_chats ?? [],
      }
    : emptySettings();
  allowedUsers.value = settings.value.allowed_users.join("\n");
  allowedChats.value = settings.value.allowed_chats.join("\n");
  appSecret.value = "";
  error.value = "";
}

function buildUpdate(): FeishuBotUpdate {
  return {
    name: settings.value.name.trim() || t("Feishu Bot"),
    locale: resolvedLocale.value,
    enabled: settings.value.enabled,
    app_id: settings.value.app_id.trim(),
    ...(appSecret.value.trim() ? { app_secret: appSecret.value.trim() } : {}),
    connection_id: settings.value.connection_id || undefined,
    model: settings.value.model?.trim() || undefined,
    project_id: settings.value.project_id || undefined,
    approval_mode: settings.value.approval_mode,
    allowed_users: parseIDs(allowedUsers.value),
    allowed_chats: parseIDs(allowedChats.value),
    allow_all: settings.value.allow_all,
  };
}

function buildRegistrationInput(): FeishuRegistrationInput {
  const update = buildUpdate();
  return {
    name: update.name,
    locale: update.locale,
    connection_id: update.connection_id,
    model: update.model,
    project_id: update.project_id,
    approval_mode: update.approval_mode,
    allowed_users: update.allowed_users,
    allowed_chats: update.allowed_chats,
    allow_all: update.allow_all,
  };
}

function clearRegistrationPoll() {
  if (registrationPollTimer) clearTimeout(registrationPollTimer);
  registrationPollTimer = undefined;
}

async function applyRegistrationState(
  next: FeishuRegistrationState,
  generation: number
) {
  if (generation !== registrationGeneration) return;
  registration.value = next;
  registrationError.value = next.error ?? "";

  if (!next.qr_code_url) registrationQRCode.value = "";
  if (next.qr_code_url && !registrationQRCode.value) {
    try {
      const dataURL = await QRCode.toDataURL(next.qr_code_url, {
        width: 224,
        margin: 1,
        errorCorrectionLevel: "M",
        color: { dark: "#111827", light: "#ffffff" },
      });
      if (generation === registrationGeneration) {
        registrationQRCode.value = dataURL;
      }
    } catch (cause) {
      registrationError.value = t("Unable to generate QR code: {error}", {
        error: String(cause),
      });
    }
  }

  if (next.status === "completed" && next.channel) {
    clearRegistrationPoll();
    registrationOpen.value = false;
    emit("saved", next.channel);
  }
}

function scheduleRegistrationPoll(generation: number, delay = 800) {
  clearRegistrationPoll();
  registrationPollTimer = setTimeout(() => {
    void pollRegistration(generation);
  }, delay);
}

async function pollRegistration(generation: number) {
  const id = registration.value?.id;
  if (!id || generation !== registrationGeneration) return;
  try {
    const next = await api.getFeishuRegistration(id);
    await applyRegistrationState(next, generation);
    if (generation === registrationGeneration && registrationActive.value) {
      scheduleRegistrationPoll(generation);
    }
  } catch (cause) {
    if (generation !== registrationGeneration) return;
    registrationError.value = String(cause);
    scheduleRegistrationPoll(generation, 2_000);
  }
}

async function cancelRegistration(stopCompleting = false) {
  const current = registration.value;
  if (current?.status === "completing" && !stopCompleting) return;
  registrationGeneration += 1;
  clearRegistrationPoll();
  if (current?.id && ["starting", "pending"].includes(current.status)) {
    await api.cancelFeishuRegistration(current.id).catch(() => undefined);
  }
}

async function startRegistration() {
  await cancelRegistration();
  const generation = ++registrationGeneration;
  registration.value = undefined;
  registrationQRCode.value = "";
  registrationError.value = "";
  registrationOpen.value = true;
  try {
    const started = await api.startFeishuRegistration(
      buildRegistrationInput()
    );
    await applyRegistrationState(started, generation);
    if (generation === registrationGeneration && registrationActive.value) {
      scheduleRegistrationPoll(generation, 300);
    }
  } catch (cause) {
    if (generation !== registrationGeneration) return;
    registrationError.value = String(cause);
  }
}

function openRegistrationURL() {
  const url = registration.value?.qr_code_url;
  if (url) void openUrl(url);
}

async function save() {
  error.value = "";
  const update = buildUpdate();
  if (update.enabled && !update.app_id) {
    error.value = t("Enter an App ID before enabling");
    return;
  }
  if (update.enabled && !update.app_secret && !settings.value.has_app_secret) {
    error.value = t("Enter an App Secret before enabling");
    return;
  }
  if (
    update.enabled &&
    !update.allow_all &&
    update.allowed_users.length === 0 &&
    update.allowed_chats.length === 0
  ) {
    error.value = t("Add at least one allowed user or group chat before enabling");
    return;
  }

  saving.value = true;
  try {
    const saved = props.creating || !settings.value.id
      ? await api.createChannel({ kind: "feishu", ...update })
      : await api.updateChannel(settings.value.id, update);
    applySettings(saved);
    emit("saved", saved);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

async function remove() {
  if (!settings.value.id) return;
  deleting.value = true;
  error.value = "";
  try {
    const id = settings.value.id;
    await api.deleteChannel(id);
    deleteConfirmOpen.value = false;
    emit("deleted", id);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    deleting.value = false;
  }
}

function selectConnection(value: unknown) {
  settings.value.connection_id = value === "__default__" ? "" : String(value ?? "");
  settings.value.model = "";
}

function selectProject(value: unknown) {
  settings.value.project_id = value === "__none__" ? "" : String(value ?? "");
}

watch(
  () => props.channel,
  (value) => applySettings(value),
  { immediate: true }
);

watch(registrationOpen, (open) => {
  if (!open) void cancelRegistration();
});

onMounted(async () => {
  loading.value = true;
  try {
    [connections.value, projects.value] = await Promise.all([
      api.listConnections(),
      api.listProjects(),
    ]);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
});

onBeforeUnmount(() => {
  void cancelRegistration(true);
});
</script>

<template>
  <div class="w-full space-y-5 pb-5">
    <header class="flex flex-wrap items-center justify-between gap-3">
      <div v-if="creating && creationMode === 'choose'" class="min-w-0">
        <h3 class="text-base font-semibold">{{ $t("Add Feishu Bot") }}</h3>
      </div>
      <div v-else class="flex min-w-0 items-start gap-1">
        <Button
          v-if="creating"
          size="icon-sm"
          variant="ghost"
          class="mt-0.5 shrink-0"
          :title="$t('Back to connection methods')"
          :aria-label="$t('Back to connection methods')"
          @click="creationMode = 'choose'"
        >
          <ArrowLeftIcon class="size-4" />
        </Button>
        <div class="min-w-0">
          <Input
            v-model="settings.name"
            class="h-8 max-w-64 border-transparent px-0 text-base font-semibold shadow-none hover:border-input focus-visible:px-2.5"
            :aria-label="$t('Bot name')"
            :disabled="loading || saving"
          />
          <div class="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
            <span :class="['size-1.5 rounded-full', statusClass]" />
            <span>{{ statusLabel }}</span>
          </div>
        </div>
      </div>
      <div v-if="!creating" class="flex items-center gap-2">
        <Tooltip>
          <TooltipTrigger as-child>
            <Button
              variant="ghost"
              size="icon-sm"
              :disabled="loading || saving"
              :aria-label="$t('Refresh status')"
              @click="emit('refresh')"
            >
              <RefreshCwIcon class="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>{{ $t("Refresh status") }}</TooltipContent>
        </Tooltip>
        <Tooltip v-if="settings.id">
          <TooltipTrigger as-child>
            <Button
              variant="ghost"
              size="icon-sm"
              :disabled="saving || deleting"
              :aria-label="$t('Delete Bot')"
              @click="deleteConfirmOpen = true"
            >
              <Trash2Icon class="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>{{ $t("Delete Bot") }}</TooltipContent>
        </Tooltip>
        <label class="flex items-center gap-2 text-sm font-medium">
          <span>{{ settings.enabled ? $t("Enabled") : $t("Disabled") }}</span>
          <Checkbox
            :model-value="settings.enabled"
            :disabled="loading || saving"
            :aria-label="$t('Enable Feishu')"
            @update:model-value="settings.enabled = $event === true"
          />
        </label>
      </div>
      <label
        v-else-if="creationMode === 'manual'"
        class="flex items-center gap-2 text-sm font-medium"
      >
        <span>{{ settings.enabled ? $t("Enabled") : $t("Disabled") }}</span>
        <Checkbox
          :model-value="settings.enabled"
          :disabled="loading || saving"
          :aria-label="$t('Enable Feishu')"
          @update:model-value="settings.enabled = $event === true"
        />
      </label>
    </header>

    <section
      v-if="creating && creationMode === 'choose'"
      class="max-w-2xl"
    >
      <h4 class="mb-3 text-sm font-semibold">{{ $t("Choose connection method") }}</h4>
      <div class="grid gap-2 sm:grid-cols-2">
        <button
          type="button"
          class="flex min-h-24 items-center gap-3 rounded-md border border-border bg-muted/20 px-4 text-left transition-colors hover:bg-muted/60"
          :disabled="loading || registrationActive"
          @click="startRegistration"
        >
          <span class="flex size-9 shrink-0 items-center justify-center rounded-md bg-foreground text-background">
            <QrCodeIcon class="size-5" />
          </span>
          <span class="min-w-0 flex-1">
            <span class="block text-sm font-semibold">{{ $t("Create with QR code") }}</span>
            <span class="mt-1 block text-xs text-muted-foreground">
              {{ $t("Create the app and configure permissions automatically") }}
            </span>
          </span>
          <ChevronRightIcon class="size-4 shrink-0 text-muted-foreground" />
        </button>
        <button
          type="button"
          class="flex min-h-24 items-center gap-3 rounded-md border border-border px-4 text-left transition-colors hover:bg-muted/60"
          :disabled="loading"
          @click="creationMode = 'manual'"
        >
          <span class="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
            <KeyRoundIcon class="size-5" />
          </span>
          <span class="min-w-0 flex-1">
            <span class="block text-sm font-semibold">{{ $t("Manual setup") }}</span>
            <span class="mt-1 block text-xs text-muted-foreground">
              {{ $t("Use an existing custom enterprise app") }}
            </span>
          </span>
          <ChevronRightIcon class="size-4 shrink-0 text-muted-foreground" />
        </button>
      </div>
    </section>

    <section v-if="!creating || creationMode === 'manual'">
      <div class="mb-3 flex items-center justify-between gap-3">
        <div>
          <h4 class="text-sm font-semibold">{{ $t("App credentials") }}</h4>
          <p class="mt-0.5 text-xs text-muted-foreground">{{ $t("Identity details for the custom enterprise app") }}</p>
        </div>
        <Button
          variant="ghost"
          size="sm"
          :disabled="saving"
          @click="openUrl('https://open.feishu.cn/app')"
        >
          <ExternalLinkIcon class="size-4" />
          {{ $t("Developer console") }}
        </Button>
      </div>
      <div class="grid gap-3 sm:grid-cols-2">
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-app-id">App ID</Label>
          <Input
            id="feishu-app-id"
            v-model="settings.app_id"
            class="font-mono"
            placeholder="cli_xxx"
            :disabled="loading || saving"
          />
        </div>
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-app-secret">App Secret</Label>
          <div class="relative">
            <Input
              id="feishu-app-secret"
              v-model="appSecret"
              class="pr-10 font-mono"
              :type="showSecret ? 'text' : 'password'"
              :placeholder="settings.has_app_secret ? $t('Saved; leave blank to keep it') : $t('Enter App Secret')"
              :disabled="loading || saving"
            />
            <button
              type="button"
              class="absolute inset-y-0 right-0 flex w-9 items-center justify-center text-muted-foreground hover:text-foreground"
              :aria-label="showSecret ? $t('Hide App Secret') : $t('Show App Secret')"
              :disabled="loading || saving"
              @click="showSecret = !showSecret"
            >
              <EyeOffIcon v-if="showSecret" class="size-4" />
              <EyeIcon v-else class="size-4" />
            </button>
          </div>
        </div>
      </div>
    </section>

    <section v-if="!creating || creationMode === 'manual'">
      <div class="mb-3">
        <h4 class="text-sm font-semibold">{{ $t("Runtime configuration") }}</h4>
        <p class="mt-0.5 text-xs text-muted-foreground">{{ $t("Choose the model, project, and tool permissions for Feishu chats") }}</p>
      </div>
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-connection">{{ $t("Language model connection") }}</Label>
          <Select
            :model-value="settings.connection_id || '__default__'"
            :disabled="loading || saving"
            @update:model-value="selectConnection"
          >
            <SelectTrigger id="feishu-connection" class="w-full">
              <SelectValue :placeholder="$t('Default language model')" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__default__">{{ $t("Default language model") }}</SelectItem>
              <SelectItem
                v-for="connection in languageConnections"
                :key="connection.id"
                :value="connection.id"
              >
                {{ connection.name }}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-model">{{ $t("Model") }}</Label>
          <Input
            id="feishu-model"
            v-model="settings.model"
            class="font-mono"
            :placeholder="$t('Default model')"
            :disabled="loading || saving"
          />
        </div>
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-project">{{ $t("Working project") }}</Label>
          <Select
            :model-value="settings.project_id || '__none__'"
            :disabled="loading || saving"
            @update:model-value="selectProject"
          >
            <SelectTrigger id="feishu-project" class="w-full">
              <SelectValue :placeholder="$t('No project')" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__none__">{{ $t("No project") }}</SelectItem>
              <SelectItem
                v-for="project in projects"
                :key="project.id"
                :value="project.id"
              >
                {{ project.name }}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div class="min-w-0 space-y-1.5">
          <Label>{{ $t("Tool approval") }}</Label>
          <ButtonGroup class="w-full" :aria-label="$t('Tool approval')">
            <Button
              class="flex-1 shadow-none"
              size="sm"
              variant="outline"
              :class="settings.approval_mode === 'auto' && 'bg-accent text-accent-foreground'"
              :aria-pressed="settings.approval_mode === 'auto'"
              :disabled="loading || saving"
              @click="settings.approval_mode = 'auto'"
            >
              {{ $t("Automatic") }}
            </Button>
            <Button
              class="flex-1 shadow-none"
              size="sm"
              variant="outline"
              :class="settings.approval_mode === 'full_access' && 'bg-accent text-accent-foreground'"
              :aria-pressed="settings.approval_mode === 'full_access'"
              :disabled="loading || saving"
              @click="settings.approval_mode = 'full_access'"
            >
              {{ $t("Full access") }}
            </Button>
          </ButtonGroup>
        </div>
      </div>
    </section>

    <section v-if="!creating || creationMode === 'manual'">
      <div class="mb-3 flex items-center justify-between gap-4">
        <div>
          <h4 class="text-sm font-semibold">{{ $t("Access scope") }}</h4>
          <p class="mt-0.5 text-xs text-muted-foreground">{{ $t("Limit which chats can invoke the local agent") }}</p>
        </div>
        <label class="flex items-center gap-2 text-sm">
          <span>{{ $t("Allow all") }}</span>
          <Checkbox
            :model-value="settings.allow_all"
            :disabled="loading || saving"
            :aria-label="$t('Allow all users and group chats')"
            @update:model-value="settings.allow_all = $event === true"
          />
        </label>
      </div>
      <div v-if="!settings.allow_all" class="grid gap-3 sm:grid-cols-2">
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-users">{{ $t("User Open IDs") }}</Label>
          <Textarea
            id="feishu-users"
            v-model="allowedUsers"
            class="min-h-20 resize-y font-mono text-xs"
            placeholder="ou_xxx&#10;ou_yyy"
            :disabled="loading || saving"
          />
        </div>
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-chats">{{ $t("Group chat IDs") }}</Label>
          <Textarea
            id="feishu-chats"
            v-model="allowedChats"
            class="min-h-20 resize-y font-mono text-xs"
            placeholder="oc_xxx"
            :disabled="loading || saving"
          />
        </div>
      </div>
    </section>

    <p v-if="settings.last_error" class="text-sm text-destructive">
      {{ settings.last_error }}
    </p>
    <p v-if="error" class="text-sm text-destructive">{{ error }}</p>

    <div
      v-if="!creating || creationMode === 'manual'"
      class="flex justify-end"
    >
      <Button :disabled="loading || saving" @click="save">
        <SaveIcon class="size-4" />
        {{ saving ? $t("Saving") : settings.enabled ? $t("Save and start") : $t("Save") }}
      </Button>
    </div>

    <Dialog v-model:open="registrationOpen">
      <DialogContent class="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{{ $t("Add Feishu Bot with QR code") }}</DialogTitle>
          <DialogDescription>{{ registrationStatusLabel }}</DialogDescription>
        </DialogHeader>

        <div class="flex min-h-56 items-center justify-center">
          <img
            v-if="registrationQRCode && registration?.status === 'pending'"
            :src="registrationQRCode"
            :alt="$t('Feishu authorization QR code')"
            class="size-56 bg-white object-contain"
          />
          <LoaderCircleIcon
            v-else-if="registrationActive"
            class="size-6 animate-spin text-muted-foreground"
          />
          <QrCodeIcon v-else class="size-12 text-muted-foreground/50" />
        </div>

        <p
          v-if="registrationError"
          class="text-center text-xs text-destructive"
        >
          {{ registrationError }}
        </p>

        <DialogFooter>
          <Button
            v-if="registration?.qr_code_url"
            variant="outline"
            @click="openRegistrationURL"
          >
            <ExternalLinkIcon class="size-4" />
            {{ $t("Open in browser") }}
          </Button>
          <Button
            v-if="!registrationActive && registration?.status !== 'completed'"
            @click="startRegistration"
          >
            <RefreshCwIcon class="size-4" />
            {{ $t("Regenerate") }}
          </Button>
          <Button
            variant="ghost"
            @click="registrationOpen = false"
          >
            {{ $t("Cancel") }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="deleteConfirmOpen">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{{ $t("Delete {name}?", { name: settings.name || $t("Feishu Bot") }) }}</DialogTitle>
        </DialogHeader>
        <p class="text-sm text-muted-foreground">
          {{ $t("The Bot will stop and its channel configuration will be deleted. Existing Foya chats will remain.") }}
        </p>
        <DialogFooter>
          <Button variant="outline" :disabled="deleting" @click="deleteConfirmOpen = false">
            {{ $t("Cancel") }}
          </Button>
          <Button variant="destructive" :disabled="deleting" @click="remove">
            {{ deleting ? $t("Deleting") : $t("Delete") }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
