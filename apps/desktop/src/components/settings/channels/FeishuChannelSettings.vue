<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
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

const props = defineProps<{
  channel: FeishuBotSettings | null;
  creating?: boolean;
}>();

const emit = defineEmits<{
  saved: [channel: FeishuBotSettings];
  deleted: [channelId: string];
  refresh: [];
}>();

const emptySettings = (): FeishuBotSettings => ({
  id: "",
  kind: "feishu",
  name: "飞书 Bot",
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
  if (!registration.value && registrationError.value) return "无法开始创建";
  switch (registration.value?.status) {
    case "pending":
      return "等待扫码确认";
    case "completing":
      return "正在创建并连接 Bot";
    case "completed":
      return "Bot 已创建";
    case "denied":
      return "已拒绝授权";
    case "expired":
      return "二维码已过期";
    case "cancelled":
      return "已取消";
    case "error":
      return "创建失败";
    default:
      return "正在生成二维码";
  }
});

const languageConnections = computed(() =>
  connections.value.filter(
    (item): item is ConnectionConfig & { id: string } =>
      item.type === "language" && typeof item.id === "string" && item.id.length > 0
  )
);

const statusLabel = computed(() => {
  if (props.creating) return "未保存";
  if (settings.value.status === "running") return "运行中";
  if (settings.value.status === "error") return "连接异常";
  return "已停止";
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
    name: settings.value.name.trim() || "飞书 Bot",
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
      registrationError.value = `无法生成二维码：${String(cause)}`;
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
    error.value = "启用前请填写 App ID";
    return;
  }
  if (update.enabled && !update.app_secret && !settings.value.has_app_secret) {
    error.value = "启用前请填写 App Secret";
    return;
  }
  if (
    update.enabled &&
    !update.allow_all &&
    update.allowed_users.length === 0 &&
    update.allowed_chats.length === 0
  ) {
    error.value = "启用前请至少添加一个用户或群聊白名单";
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
        <h3 class="text-base font-semibold">添加飞书 Bot</h3>
      </div>
      <div v-else class="flex min-w-0 items-start gap-1">
        <Button
          v-if="creating"
          size="icon-sm"
          variant="ghost"
          class="mt-0.5 shrink-0"
          title="返回接入方式"
          aria-label="返回接入方式"
          @click="creationMode = 'choose'"
        >
          <ArrowLeftIcon class="size-4" />
        </Button>
        <div class="min-w-0">
          <Input
            v-model="settings.name"
            class="h-8 max-w-64 border-transparent px-0 text-base font-semibold shadow-none hover:border-input focus-visible:px-2.5"
            aria-label="Bot 名称"
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
              aria-label="刷新状态"
              @click="emit('refresh')"
            >
              <RefreshCwIcon class="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>刷新状态</TooltipContent>
        </Tooltip>
        <Tooltip v-if="settings.id">
          <TooltipTrigger as-child>
            <Button
              variant="ghost"
              size="icon-sm"
              :disabled="saving || deleting"
              aria-label="删除 Bot"
              @click="deleteConfirmOpen = true"
            >
              <Trash2Icon class="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>删除 Bot</TooltipContent>
        </Tooltip>
        <label class="flex items-center gap-2 text-sm font-medium">
          <span>{{ settings.enabled ? "已启用" : "未启用" }}</span>
          <Checkbox
            :model-value="settings.enabled"
            :disabled="loading || saving"
            aria-label="启用飞书"
            @update:model-value="settings.enabled = $event === true"
          />
        </label>
      </div>
      <label
        v-else-if="creationMode === 'manual'"
        class="flex items-center gap-2 text-sm font-medium"
      >
        <span>{{ settings.enabled ? "已启用" : "未启用" }}</span>
        <Checkbox
          :model-value="settings.enabled"
          :disabled="loading || saving"
          aria-label="启用飞书"
          @update:model-value="settings.enabled = $event === true"
        />
      </label>
    </header>

    <section
      v-if="creating && creationMode === 'choose'"
      class="max-w-2xl"
    >
      <h4 class="mb-3 text-sm font-semibold">选择接入方式</h4>
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
            <span class="block text-sm font-semibold">扫码创建</span>
            <span class="mt-1 block text-xs text-muted-foreground">
              自动创建应用并配置权限
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
            <span class="block text-sm font-semibold">手动配置</span>
            <span class="mt-1 block text-xs text-muted-foreground">
              使用已有企业自建应用
            </span>
          </span>
          <ChevronRightIcon class="size-4 shrink-0 text-muted-foreground" />
        </button>
      </div>
    </section>

    <section v-if="!creating || creationMode === 'manual'">
      <div class="mb-3 flex items-center justify-between gap-3">
        <div>
          <h4 class="text-sm font-semibold">应用凭证</h4>
          <p class="mt-0.5 text-xs text-muted-foreground">企业自建应用的身份信息</p>
        </div>
        <Button
          variant="ghost"
          size="sm"
          :disabled="saving"
          @click="openUrl('https://open.feishu.cn/app')"
        >
          <ExternalLinkIcon class="size-4" />
          开发者后台
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
              :placeholder="settings.has_app_secret ? '已保存，留空则保持不变' : '输入 App Secret'"
              :disabled="loading || saving"
            />
            <button
              type="button"
              class="absolute inset-y-0 right-0 flex w-9 items-center justify-center text-muted-foreground hover:text-foreground"
              :aria-label="showSecret ? '隐藏 App Secret' : '显示 App Secret'"
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
        <h4 class="text-sm font-semibold">运行配置</h4>
        <p class="mt-0.5 text-xs text-muted-foreground">为飞书会话选择模型、项目和工具权限</p>
      </div>
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-connection">语言模型连接</Label>
          <Select
            :model-value="settings.connection_id || '__default__'"
            :disabled="loading || saving"
            @update:model-value="selectConnection"
          >
            <SelectTrigger id="feishu-connection" class="w-full">
              <SelectValue placeholder="默认语言模型" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__default__">默认语言模型</SelectItem>
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
          <Label for="feishu-model">模型</Label>
          <Input
            id="feishu-model"
            v-model="settings.model"
            class="font-mono"
            placeholder="默认模型"
            :disabled="loading || saving"
          />
        </div>
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-project">工作项目</Label>
          <Select
            :model-value="settings.project_id || '__none__'"
            :disabled="loading || saving"
            @update:model-value="selectProject"
          >
            <SelectTrigger id="feishu-project" class="w-full">
              <SelectValue placeholder="不绑定项目" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__none__">不绑定项目</SelectItem>
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
          <Label>工具审批</Label>
          <ButtonGroup class="w-full" aria-label="工具审批">
            <Button
              class="flex-1 shadow-none"
              size="sm"
              variant="outline"
              :class="settings.approval_mode === 'auto' && 'bg-accent text-accent-foreground'"
              :aria-pressed="settings.approval_mode === 'auto'"
              :disabled="loading || saving"
              @click="settings.approval_mode = 'auto'"
            >
              自动判断
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
              完全访问
            </Button>
          </ButtonGroup>
        </div>
      </div>
    </section>

    <section v-if="!creating || creationMode === 'manual'">
      <div class="mb-3 flex items-center justify-between gap-4">
        <div>
          <h4 class="text-sm font-semibold">访问范围</h4>
          <p class="mt-0.5 text-xs text-muted-foreground">限制可以调用本机 Agent 的会话</p>
        </div>
        <label class="flex items-center gap-2 text-sm">
          <span>允许全部</span>
          <Checkbox
            :model-value="settings.allow_all"
            :disabled="loading || saving"
            aria-label="允许所有用户和群聊"
            @update:model-value="settings.allow_all = $event === true"
          />
        </label>
      </div>
      <div v-if="!settings.allow_all" class="grid gap-3 sm:grid-cols-2">
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-users">用户 Open ID</Label>
          <Textarea
            id="feishu-users"
            v-model="allowedUsers"
            class="min-h-20 resize-y font-mono text-xs"
            placeholder="ou_xxx&#10;ou_yyy"
            :disabled="loading || saving"
          />
        </div>
        <div class="min-w-0 space-y-1.5">
          <Label for="feishu-chats">群聊 ID</Label>
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
        {{ saving ? "保存中..." : settings.enabled ? "保存并启动" : "保存" }}
      </Button>
    </div>

    <Dialog v-model:open="registrationOpen">
      <DialogContent class="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>扫码添加飞书 Bot</DialogTitle>
          <DialogDescription>{{ registrationStatusLabel }}</DialogDescription>
        </DialogHeader>

        <div class="flex min-h-56 items-center justify-center">
          <img
            v-if="registrationQRCode && registration?.status === 'pending'"
            :src="registrationQRCode"
            alt="飞书授权二维码"
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
            在浏览器打开
          </Button>
          <Button
            v-if="!registrationActive && registration?.status !== 'completed'"
            @click="startRegistration"
          >
            <RefreshCwIcon class="size-4" />
            重新生成
          </Button>
          <Button
            variant="ghost"
            @click="registrationOpen = false"
          >
            取消
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog v-model:open="deleteConfirmOpen">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>删除 {{ settings.name || "飞书 Bot" }}？</DialogTitle>
        </DialogHeader>
        <p class="text-sm text-muted-foreground">
          将停止该 Bot 并删除它的渠道配置，不会删除已有 Foya 会话。
        </p>
        <DialogFooter>
          <Button variant="outline" :disabled="deleting" @click="deleteConfirmOpen = false">
            取消
          </Button>
          <Button variant="destructive" :disabled="deleting" @click="remove">
            {{ deleting ? "删除中..." : "删除" }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
