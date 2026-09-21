<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { openUrl } from "@tauri-apps/plugin-opener";
import {
  ArrowLeftIcon,
  CheckIcon,
  CopyIcon,
  ExternalLinkIcon,
  Link2Icon,
  LoaderCircleIcon,
  QrCodeIcon,
  RefreshCwIcon,
  UnlinkIcon,
} from "@lucide/vue";
import {
  api,
  type ChannelConversation,
  type ChannelPairing,
  type FeishuRegistrationState,
  type MessagingChannelKind,
  type MessagingChannelSettings,
  type Session,
  type TelegramChannelUpdate,
} from "@/lib/api";
import { isConfiguredChannel } from "@/lib/channelConnection";
import { Button } from "@/components/ui/button";
import { ButtonGroup } from "@/components/ui/button-group";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useLocale } from "@/i18n";

const props = withDefaults(
  defineProps<{
    session: Session;
    showTrigger?: boolean;
  }>(),
  {
    showTrigger: true,
  }
);
const { t } = useI18n();
const { resolvedLocale } = useLocale();

const open = ref(false);
const loading = ref(false);
const busy = ref(false);
const copied = ref(false);
const error = ref("");
const channels = ref<MessagingChannelSettings[]>([]);
const currentBinding = ref<ChannelConversation | null>(null);
const replacing = ref(false);
const pairing = ref<ChannelPairing | null>(null);
const selectedKind = ref<MessagingChannelKind>("feishu");
const telegramToken = ref("");
const registration = ref<FeishuRegistrationState | null>(null);
const registrationQRCode = ref("");
let loadGeneration = 0;
let pairingGeneration = 0;
let pairingTimer: ReturnType<typeof setTimeout> | undefined;
let registrationGeneration = 0;
let registrationTimer: ReturnType<typeof setTimeout> | undefined;

const currentChannel = computed(
  () =>
    channels.value.find(
      (item) => item.id === currentBinding.value?.channel_id
    ) ?? null
);
const bindingDisplayName = computed(() => {
  if (currentBinding.value?.display_name) {
    return currentBinding.value.display_name;
  }
  if (
    currentBinding.value?.kind === "group" ||
    currentBinding.value?.kind === "supergroup"
  ) {
    return t("Group chat");
  }
  return t("Private chat");
});
const platformChannel = computed(() => {
  const candidates = channels.value.filter(
    (item) => item.kind === selectedKind.value
  );
  const bound = candidates.find(
    (item) => item.id === currentBinding.value?.channel_id
  );
  return (
    bound ??
    candidates.find((item) => item.enabled && item.status === "running") ??
    candidates.find((item) => item.enabled) ??
    candidates[0] ??
    null
  );
});
const canPair = computed(
  () =>
    props.session.approval_mode === "auto" ||
    props.session.approval_mode === "full_access"
);
const channelReady = computed(
  () =>
    platformChannel.value?.enabled &&
    platformChannel.value.status === "running"
);
const pairingPending = computed(() => pairing.value?.status === "pending");
const registrationPending = computed(() =>
  ["starting", "pending", "completing"].includes(
    registration.value?.status ?? ""
  )
);

const defaultApprovalMode = computed<"auto" | "full_access">(() =>
  props.session.approval_mode === "full_access" ? "full_access" : "auto"
);
const pairingStatusLabel = computed(() => {
  switch (pairing.value?.status) {
    case "completed":
      return t("Paired");
    case "expired":
      return t("Pairing code expired");
    case "cancelled":
      return t("Pairing cancelled");
    default:
      return t("Waiting for the command");
  }
});
const registrationStatusLabel = computed(() => {
  switch (registration.value?.status) {
    case "pending":
      return t("Scan with Feishu to continue");
    case "completing":
      return t("Connecting Feishu");
    case "completed":
      return t("Connected");
    case "denied":
      return t("Authorization denied");
    case "expired":
      return t("QR code expired");
    case "error":
      return t("Connection error");
    default:
      return t("Preparing QR code");
  }
});

function upsertChannel(channel: MessagingChannelSettings) {
  const index = channels.value.findIndex((item) => item.id === channel.id);
  if (index >= 0) channels.value[index] = channel;
  else channels.value.push(channel);
}

async function enableChannel(
  channel: MessagingChannelSettings
): Promise<MessagingChannelSettings> {
  if (channel.kind === "feishu") {
    return api.updateChannel(channel.id, {
      name: channel.name,
      locale: channel.locale,
      enabled: true,
      app_id: channel.app_id,
      connection_id: channel.connection_id,
      model: channel.model,
      project_id: channel.project_id,
      approval_mode: channel.approval_mode,
      allowed_users: channel.allowed_users,
      allowed_chats: channel.allowed_chats,
      allow_all: channel.allow_all,
    });
  }
  return api.updateChannel(channel.id, {
    name: channel.name,
    locale: channel.locale,
    enabled: true,
    connection_id: channel.connection_id,
    model: channel.model,
    project_id: channel.project_id,
    approval_mode: channel.approval_mode,
    allowed_users: channel.allowed_users,
    allowed_chats: channel.allowed_chats,
    allow_all: channel.allow_all,
  });
}

async function loadData(preferredKind?: MessagingChannelKind) {
  const generation = ++loadGeneration;
  loading.value = true;
  error.value = "";
  try {
    const [nextChannels, binding] = await Promise.all([
      api.listChannels(),
      api.getSessionChannelBinding(props.session.id),
    ]);
    if (generation !== loadGeneration) return;
    const incomplete = nextChannels.filter(
      (item) => !isConfiguredChannel(item)
    );
    const incompleteIDs = new Set(incomplete.map((item) => item.id));
    if (incomplete.length > 0) {
      await Promise.all(
        incomplete.map((item) =>
          api.deleteChannel(item.id).catch(() => undefined)
        )
      );
    }
    if (generation !== loadGeneration) return;
    const configuredChannels = nextChannels.filter(isConfiguredChannel);
    channels.value = configuredChannels;
    currentBinding.value =
      binding && !incompleteIDs.has(binding.channel_id) ? binding : null;
    const boundChannel = configuredChannels.find(
      (item) => item.id === binding?.channel_id
    );
    selectedKind.value =
      preferredKind ??
      boundChannel?.kind ??
      configuredChannels.find((item) => item.enabled)?.kind ??
      selectedKind.value;
    const selectedChannel = configuredChannels.find(
      (item) => item.kind === selectedKind.value
    );
    if (selectedChannel && !selectedChannel.enabled) {
      try {
        const enabled = await enableChannel(selectedChannel);
        if (generation !== loadGeneration) return;
        upsertChannel(enabled);
      } catch (cause) {
        if (generation === loadGeneration) error.value = String(cause);
      }
    }
  } catch (cause) {
    if (generation === loadGeneration) error.value = String(cause);
  } finally {
    if (generation === loadGeneration) loading.value = false;
  }
}

async function openDialog() {
  open.value = true;
  pairing.value = null;
  copied.value = false;
  error.value = "";
  await loadData();
  replacing.value = !currentBinding.value;
}

defineExpose({ open: openDialog });

function clearPairingTimer() {
  if (pairingTimer) clearTimeout(pairingTimer);
  pairingTimer = undefined;
}

async function cancelPairing() {
  const current = pairing.value;
  pairingGeneration += 1;
  clearPairingTimer();
  pairing.value = null;
  if (current?.status === "pending") {
    await api.cancelChannelPairing(current.id).catch(() => undefined);
  }
}

function schedulePairingPoll(generation: number) {
  clearPairingTimer();
  pairingTimer = setTimeout(() => void pollPairing(generation), 800);
}

async function pollPairing(generation: number) {
  const id = pairing.value?.id;
  if (!id || generation !== pairingGeneration) return;
  try {
    const next = await api.getChannelPairing(id);
    if (generation !== pairingGeneration) return;
    pairing.value = next;
    if (next.status === "completed") {
      currentBinding.value = next.conversation ?? null;
      pairing.value = null;
      replacing.value = false;
      clearPairingTimer();
    } else if (next.status === "pending") {
      schedulePairingPoll(generation);
    }
  } catch (cause) {
    if (generation === pairingGeneration) error.value = String(cause);
    clearPairingTimer();
  }
}

async function startPairing(channelID = platformChannel.value?.id ?? "") {
  if (!channelID || !canPair.value) return;
  await cancelPairing();
  busy.value = true;
  error.value = "";
  const generation = ++pairingGeneration;
  try {
    pairing.value = await api.startSessionChannelPairing(
      props.session.id,
      channelID
    );
    schedulePairingPoll(generation);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    busy.value = false;
  }
}

async function copyCommand() {
  if (!pairing.value?.command) return;
  try {
    await navigator.clipboard.writeText(pairing.value.command);
    copied.value = true;
    setTimeout(() => {
      copied.value = false;
    }, 1_500);
  } catch (cause) {
    error.value = String(cause);
  }
}

function clearRegistrationTimer() {
  if (registrationTimer) clearTimeout(registrationTimer);
  registrationTimer = undefined;
}

async function cancelRegistration() {
  const current = registration.value;
  registrationGeneration += 1;
  clearRegistrationTimer();
  registration.value = null;
  registrationQRCode.value = "";
  if (
    current?.id &&
    ["starting", "pending"].includes(current.status)
  ) {
    await api.cancelFeishuRegistration(current.id).catch(() => undefined);
  }
}

async function applyRegistration(
  next: FeishuRegistrationState,
  generation: number
) {
  if (generation !== registrationGeneration) return;
  registration.value = next;
  if (next.error) error.value = next.error;
  if (next.qr_code_url && !registrationQRCode.value) {
    const { default: QRCode } = await import("qrcode");
    const dataURL = await QRCode.toDataURL(next.qr_code_url, {
      width: 224,
      margin: 1,
      errorCorrectionLevel: "M",
    });
    if (generation === registrationGeneration) {
      registrationQRCode.value = dataURL;
    }
  }
  if (next.status === "completed" && next.channel) {
    clearRegistrationTimer();
    upsertChannel(next.channel);
    registrationQRCode.value = "";
    registration.value = null;
    await loadData("feishu");
    await startPairing(next.channel.id);
  }
}

function scheduleRegistrationPoll(generation: number) {
  clearRegistrationTimer();
  registrationTimer = setTimeout(
    () => void pollRegistration(generation),
    800
  );
}

async function pollRegistration(generation: number) {
  const id = registration.value?.id;
  if (!id || generation !== registrationGeneration) return;
  try {
    const next = await api.getFeishuRegistration(id);
    await applyRegistration(next, generation);
    if (
      generation === registrationGeneration &&
      ["starting", "pending", "completing"].includes(next.status)
    ) {
      scheduleRegistrationPoll(generation);
    }
  } catch (cause) {
    if (generation === registrationGeneration) error.value = String(cause);
    clearRegistrationTimer();
  }
}

async function connectFeishu() {
  await cancelRegistration();
  busy.value = true;
  error.value = "";
  const generation = ++registrationGeneration;
  try {
    const started = await api.startFeishuRegistration({
      name: "Feishu",
      locale: resolvedLocale.value,
      connection_id: props.session.connection_id || undefined,
      model: props.session.model || undefined,
      project_id: props.session.project_id || undefined,
      approval_mode: defaultApprovalMode.value,
      allowed_users: [],
      allowed_chats: [],
      allow_all: false,
    });
    await applyRegistration(started, generation);
    if (
      generation === registrationGeneration &&
      ["starting", "pending", "completing"].includes(
        registration.value?.status ?? ""
      )
    ) {
      scheduleRegistrationPoll(generation);
    }
  } catch (cause) {
    error.value = String(cause);
  } finally {
    busy.value = false;
  }
}

async function connectTelegram() {
  const token = telegramToken.value.trim();
  if (!token) {
    error.value = t("Enter a Telegram token");
    return;
  }
  busy.value = true;
  error.value = "";
  try {
    const existing =
      platformChannel.value?.kind === "telegram"
        ? platformChannel.value
        : null;
    const input: TelegramChannelUpdate = {
      name: "Telegram",
      locale: resolvedLocale.value,
      enabled: true,
      token,
      connection_id: props.session.connection_id || undefined,
      model: props.session.model || undefined,
      project_id: props.session.project_id || undefined,
      approval_mode: defaultApprovalMode.value,
      allowed_users: [],
      allowed_chats: [],
      allow_all: false,
    };
    const saved = existing
      ? await api.updateChannel(existing.id, input)
      : await api.createChannel({ kind: "telegram", ...input });
    if (saved.kind !== "telegram") {
      throw new Error(t("Unexpected channel type"));
    }
    telegramToken.value = "";
    upsertChannel(saved);
    await loadData("telegram");
    await startPairing(saved.id);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    busy.value = false;
  }
}

async function unbind() {
  busy.value = true;
  error.value = "";
  try {
    await api.unbindSessionChannel(props.session.id);
    currentBinding.value = null;
    pairing.value = null;
    replacing.value = true;
  } catch (cause) {
    error.value = String(cause);
  } finally {
    busy.value = false;
  }
}

async function beginReplacement() {
  await cancelPairing();
  selectedKind.value = currentChannel.value?.kind ?? selectedKind.value;
  replacing.value = true;
}

async function cancelReplacement() {
  await cancelPairing();
  replacing.value = false;
  error.value = "";
}

async function selectPlatform(kind: MessagingChannelKind) {
  if (kind === selectedKind.value) return;
  await cancelPairing();
  await cancelRegistration();
  selectedKind.value = kind;
  telegramToken.value = "";
  error.value = "";
  const channel = channels.value.find((item) => item.kind === kind);
  if (!channel || channel.enabled) return;
  busy.value = true;
  try {
    upsertChannel(await enableChannel(channel));
  } catch (cause) {
    error.value = String(cause);
  } finally {
    busy.value = false;
  }
}

watch(
  () => props.session.id,
  async () => {
    await cancelPairing();
    await cancelRegistration();
    if (!open.value) {
      currentBinding.value = null;
      return;
    }
    await loadData();
    replacing.value = !currentBinding.value;
  }
);

watch(open, (value) => {
  if (!value) {
    void cancelPairing();
    void cancelRegistration();
  }
});

onBeforeUnmount(() => {
  void cancelPairing();
  void cancelRegistration();
});
</script>

<template>
  <button
    v-if="showTrigger"
    type="button"
    :class="[
      'flex size-7 items-center justify-center rounded-md transition-colors hover:bg-muted hover:text-foreground',
      currentBinding ? 'text-primary' : 'text-muted-foreground',
    ]"
    :title="
      currentBinding
        ? $t('Bound to {channel}', {
            channel: currentChannel?.name || currentBinding.channel_id,
          })
        : $t('Connect external conversation')
    "
    :aria-label="$t('Connect external conversation')"
    @click="openDialog"
  >
    <Link2Icon class="size-4" />
  </button>

  <Dialog v-model:open="open">
    <DialogContent
      class="max-h-[calc(100vh-2rem)] min-w-0 overflow-x-hidden overflow-y-auto sm:max-w-md"
    >
      <DialogHeader>
        <DialogTitle>{{ $t("Connect external conversation") }}</DialogTitle>
        <DialogDescription>
          {{
            currentBinding && !replacing
              ? $t("This Foya chat is connected to an external conversation.")
              : $t("Choose a platform, then pair the target chat.")
          }}
        </DialogDescription>
      </DialogHeader>

      <div v-if="loading" class="flex h-28 items-center justify-center">
        <LoaderCircleIcon class="size-5 animate-spin text-muted-foreground" />
      </div>

      <div v-else class="min-w-0 space-y-4">
        <div
          v-if="currentBinding && !replacing"
          class="flex min-w-0 max-w-full flex-wrap items-start justify-between gap-3 rounded-md border border-border p-3"
        >
          <div class="min-w-0">
            <div class="flex items-center gap-2 text-sm font-medium">
              <CheckIcon class="size-4 shrink-0 text-emerald-600" />
              <span>{{ $t("Connected") }}</span>
            </div>
            <p class="mt-1 truncate text-xs text-muted-foreground">
              {{ currentChannel?.name || currentBinding.channel_id }}
              ·
              {{ bindingDisplayName }}
            </p>
          </div>
          <div class="ml-auto flex max-w-full flex-wrap items-center justify-end gap-2">
            <Button
              size="sm"
              variant="outline"
              :disabled="busy"
              @click="beginReplacement"
            >
              <Link2Icon class="size-4" />
              {{ $t("Change binding") }}
            </Button>
            <Button
              size="icon-sm"
              variant="ghost"
              :title="$t('Unbind')"
              :aria-label="$t('Unbind')"
              :disabled="busy"
              @click="unbind"
            >
              <UnlinkIcon class="size-4" />
            </Button>
          </div>
        </div>

        <template v-if="!currentBinding || replacing">
          <Button
            v-if="currentBinding"
            size="sm"
            variant="ghost"
            class="w-fit"
            @click="cancelReplacement"
          >
            <ArrowLeftIcon class="size-4" />
            {{ $t("Current binding") }}
          </Button>

        <ButtonGroup
          class="w-full min-w-0 max-w-full overflow-hidden"
          :aria-label="$t('Platform')"
        >
          <Button
            class="min-w-0 flex-1 shadow-none"
            variant="outline"
            :class="selectedKind === 'feishu' && 'bg-accent text-accent-foreground'"
            :aria-pressed="selectedKind === 'feishu'"
            @click="selectPlatform('feishu')"
          >
            {{ $t("Feishu") }}
          </Button>
          <Button
            class="min-w-0 flex-1 shadow-none"
            variant="outline"
            :class="selectedKind === 'telegram' && 'bg-accent text-accent-foreground'"
            :aria-pressed="selectedKind === 'telegram'"
            @click="selectPlatform('telegram')"
          >
            {{ $t("Telegram") }}
          </Button>
        </ButtonGroup>

        <div
          v-if="channelReady && !registration"
          class="flex min-w-0 max-w-full flex-wrap items-center justify-between gap-3 rounded-md border border-border p-3"
        >
          <div class="flex min-w-0 items-center gap-2">
            <CheckIcon class="size-4 shrink-0 text-emerald-600" />
            <span class="truncate text-sm">
              {{ $t("{platform} connected", { platform: selectedKind === "feishu" ? $t("Feishu") : $t("Telegram") }) }}
            </span>
          </div>
          <div class="ml-auto flex max-w-full flex-wrap items-center justify-end gap-1">
            <Button
              v-if="selectedKind === 'feishu'"
              size="sm"
              variant="ghost"
              :disabled="busy"
              @click="connectFeishu"
            >
              {{ $t("Choose another Feishu bot") }}
            </Button>
            <Button
              size="icon-sm"
              variant="ghost"
              :title="$t('Refresh status')"
              :aria-label="$t('Refresh status')"
              @click="loadData(selectedKind)"
            >
              <RefreshCwIcon class="size-4" />
            </Button>
          </div>
        </div>

        <template v-else-if="selectedKind === 'feishu'">
          <div
            v-if="registrationPending || registration"
            class="min-w-0 max-w-full space-y-3 rounded-md border border-border p-3 text-center"
          >
            <p class="text-sm font-medium">{{ registrationStatusLabel }}</p>
            <img
              v-if="registrationQRCode && registration?.status === 'pending'"
              :src="registrationQRCode"
              :alt="$t('Feishu authorization QR code')"
              class="mx-auto size-56 bg-white object-contain"
            />
            <LoaderCircleIcon
              v-else-if="registrationPending"
              class="mx-auto size-5 animate-spin text-muted-foreground"
            />
            <QrCodeIcon v-else class="mx-auto size-10 text-muted-foreground" />
            <Button
              v-if="registration && !registrationPending && registration.status !== 'completed'"
              size="sm"
              @click="connectFeishu"
            >
              {{ $t("Try again") }}
            </Button>
          </div>
          <div v-else class="flex justify-end">
            <Button :disabled="busy || !canPair" @click="connectFeishu">
              <QrCodeIcon class="size-4" />
              {{
                platformChannel
                  ? $t("Choose another Feishu bot")
                  : $t("Choose Feishu bot")
              }}
            </Button>
          </div>
        </template>

        <template v-else>
          <div class="space-y-2">
            <div class="flex items-center justify-between gap-3">
              <Label for="telegram-token">{{ $t("Telegram token") }}</Label>
              <Button
                size="sm"
                variant="ghost"
                @click="openUrl('https://t.me/BotFather')"
              >
                <ExternalLinkIcon class="size-4" />
                BotFather
              </Button>
            </div>
            <Input
              id="telegram-token"
              v-model="telegramToken"
              type="password"
              class="font-mono"
              placeholder="123456:ABC..."
              :disabled="busy"
              @keydown.enter="connectTelegram"
            />
          </div>
          <div class="flex justify-end">
            <Button
              :disabled="busy || !canPair || !telegramToken.trim()"
              @click="connectTelegram"
            >
              {{ platformChannel ? $t("Reconnect Telegram") : $t("Connect Telegram") }}
            </Button>
          </div>
        </template>

        <p
          v-if="platformChannel?.last_error && !channelReady"
          class="text-sm text-destructive"
        >
          {{ platformChannel.last_error }}
        </p>
        <p v-if="!canPair" class="text-sm text-destructive">
          {{ $t("Set tool approval to Automatic or Full access before binding") }}
        </p>

        <div
          v-if="pairing"
          class="min-w-0 max-w-full space-y-3 rounded-md border border-border p-3"
        >
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-sm font-medium">{{ pairingStatusLabel }}</p>
              <p
                v-if="pairingPending"
                class="mt-1 text-xs text-muted-foreground"
              >
                {{ $t("Send this command in the target private chat or group.") }}
              </p>
            </div>
            <LoaderCircleIcon
              v-if="pairingPending"
              class="size-4 shrink-0 animate-spin text-muted-foreground"
            />
          </div>
          <div v-if="pairingPending" class="flex min-w-0 items-center gap-2">
            <code
              class="min-w-0 flex-1 select-all truncate rounded-md bg-muted px-3 py-2 font-mono text-sm"
            >
              {{ pairing.command }}
            </code>
            <Button
              size="icon"
              variant="outline"
              :title="$t('Copy pairing command')"
              :aria-label="$t('Copy pairing command')"
              @click="copyCommand"
            >
              <CheckIcon v-if="copied" class="size-4" />
              <CopyIcon v-else class="size-4" />
            </Button>
          </div>
          <Button
            v-if="pairingPending"
            size="sm"
            variant="ghost"
            @click="cancelPairing"
          >
            {{ $t("Cancel pairing") }}
          </Button>
        </div>

        <div v-if="channelReady && !pairingPending" class="flex justify-end">
          <Button :disabled="busy || !canPair" @click="startPairing()">
            <Link2Icon class="size-4" />
            {{
              pairing
                ? $t("Generate another code")
                : $t("Generate pairing code")
            }}
          </Button>
        </div>
        </template>

        <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
      </div>
    </DialogContent>
  </Dialog>
</template>
