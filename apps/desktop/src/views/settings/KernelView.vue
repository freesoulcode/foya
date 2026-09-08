<script setup lang="ts">
import {
  ArrowLeftIcon,
  CircleCheckIcon,
  GlobeLockIcon,
  LaptopIcon,
  PlusIcon,
  ServerIcon,
} from "@lucide/vue";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import {
  api,
  type KernelConnection,
  type KernelConnectionInput,
  type SshConfigHost,
  type SshHostInput,
} from "@/lib/api";
import { localizeError } from "@/i18n";
import SettingsPage from "@/layouts/settings/SettingsPage.vue";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

type Editor = "ssh" | "https" | null;

const { t } = useI18n();
const connection = ref<KernelConnection | null>(null);
const editor = ref<Editor>(null);
const url = ref("");
const token = ref("");
const sshName = ref("");
const sshTarget = ref("");
const sshPort = ref(22);
const sshHosts = ref<SshConfigHost[]>([]);
const selectedSshHost = ref("");
const loading = ref(true);
const testing = ref(false);
const saving = ref(false);
const status = ref("");
const error = ref("");

const hasConfiguredSsh = computed(() => connection.value?.mode === "ssh" && !!connection.value.ssh);
const editorTitle = computed(() => {
  if (editor.value === "https") return t("HTTPS connection");
  return hasConfiguredSsh.value ? t("Edit SSH connection") : t("Add SSH connection");
});
const canSubmit = computed(() => {
  if (editor.value === "ssh") {
    return sshTarget.value.trim().length > 0 && sshPort.value > 0 && sshPort.value <= 65535;
  }
  if (editor.value === "https") {
    const configuredUrl = connection.value?.url ?? "";
    const keepsConfiguredToken =
      connection.value?.mode === "remote" &&
      connection.value.has_token &&
      url.value.trim().replace(/\/+$/, "") === configuredUrl.replace(/\/+$/, "");
    return url.value.trim().length > 0 && (token.value.trim().length > 0 || keepsConfiguredToken);
  }
  return false;
});

function remoteInput(): KernelConnectionInput {
  return {
    mode: "remote",
    url: url.value.trim(),
    ...(token.value.trim() ? { token: token.value.trim() } : {}),
  };
}

function sshInput(): SshHostInput {
  return {
    name: sshName.value.trim(),
    target: sshTarget.value.trim(),
    port: Number(sshPort.value),
  };
}

function selectSshHost(value: unknown) {
  if (typeof value !== "string") return;
  selectedSshHost.value = value;
  const host = sshHosts.value.find((item) => item.alias === value);
  if (!host) return;
  sshName.value = host.alias;
  sshTarget.value = host.alias;
  sshPort.value = host.port;
}

function openEditor(next: Exclude<Editor, null>) {
  editor.value = next;
  status.value = "";
  error.value = "";
}

function closeEditor() {
  editor.value = null;
  status.value = "";
  error.value = "";
}

async function load() {
  loading.value = true;
  error.value = "";
  try {
    const [configured, hosts] = await Promise.all([
      api.getKernelConnection(),
      api.listSshHosts(),
    ]);
    connection.value = configured;
    url.value = configured.url;
    sshHosts.value = hosts;
    if (configured.ssh) {
      sshName.value = configured.ssh.name;
      sshTarget.value = configured.ssh.target;
      sshPort.value = configured.ssh.port;
      selectedSshHost.value = hosts.some((host) => host.alias === configured.ssh?.target)
        ? configured.ssh.target
        : "";
    }
  } catch (cause) {
    error.value = localizeError(cause);
  } finally {
    loading.value = false;
  }
}

async function testConnection() {
  testing.value = true;
  status.value = "";
  error.value = "";
  try {
    if (editor.value === "ssh") {
      const probe = await api.testSshHost(sshInput());
      status.value = probe.sandbox_available
        ? t("SSH connection succeeded: {os} {arch}", {
            os: probe.os,
            arch: probe.architecture,
          })
        : t("Bubblewrap is required on the remote server");
    } else {
      await api.testKernelConnection(remoteInput());
      status.value = t("Connection succeeded");
    }
  } catch (cause) {
    error.value = localizeError(cause);
  } finally {
    testing.value = false;
  }
}

async function save() {
  saving.value = true;
  status.value = "";
  error.value = "";
  try {
    if (editor.value === "ssh") {
      status.value = t("Deploying remote kernel");
      await api.deploySshKernel(sshInput());
    } else {
      await api.updateKernelConnection(remoteInput());
    }
    status.value = t("Restarting");
  } catch (cause) {
    error.value = localizeError(cause);
    status.value = "";
    saving.value = false;
  }
}

async function useLocalKernel() {
  if (connection.value?.mode === "local") return;
  saving.value = true;
  error.value = "";
  try {
    await api.updateKernelConnection({ mode: "local", url: "" });
  } catch (cause) {
    error.value = localizeError(cause);
    saving.value = false;
  }
}

void load();
</script>

<template>
  <SettingsPage :title="editor ? editorTitle : $t('Kernel')">
    <template v-if="!editor">
      <div class="max-w-3xl space-y-8">
        <section class="space-y-3">
          <div class="flex min-h-9 items-center justify-between gap-4">
            <h3 class="text-sm font-medium">{{ $t("SSH connections from this Mac") }}</h3>
            <Button
              v-if="hasConfiguredSsh"
              variant="outline"
              size="sm"
              :disabled="loading || saving"
              @click="openEditor('ssh')"
            >
              {{ $t("Edit") }}
            </Button>
          </div>

          <div
            v-if="hasConfiguredSsh"
            class="flex min-h-20 items-center gap-4 rounded-md border border-border px-4 py-3"
          >
            <ServerIcon class="size-5 shrink-0 text-muted-foreground" />
            <div class="min-w-0 flex-1">
              <p class="truncate text-sm font-medium">
                {{ connection?.ssh?.name || connection?.ssh?.target }}
              </p>
              <p class="mt-0.5 truncate font-mono text-xs text-muted-foreground">
                {{ connection?.ssh?.target }}:{{ connection?.ssh?.port }}
              </p>
            </div>
            <span class="flex shrink-0 items-center gap-1.5 text-xs text-emerald-700">
              <CircleCheckIcon class="size-4" />
              {{ $t("Current") }}
            </span>
          </div>

          <div
            v-else
            class="flex min-h-44 flex-col items-center justify-center gap-3 rounded-md border border-border px-6 py-8 text-center"
          >
            <ServerIcon class="size-7 text-muted-foreground" />
            <p class="text-sm text-muted-foreground">{{ $t("No SSH connections") }}</p>
            <Button size="sm" :disabled="loading" @click="openEditor('ssh')">
              <PlusIcon />
              {{ $t("Add") }}
            </Button>
          </div>
        </section>

        <section class="space-y-3">
          <h3 class="text-sm font-medium">{{ $t("Other connection methods") }}</h3>
          <div class="overflow-hidden rounded-md border border-border">
            <div class="flex min-h-16 items-center gap-4 px-4 py-3">
              <LaptopIcon class="size-5 shrink-0 text-muted-foreground" />
              <div class="min-w-0 flex-1">
                <p class="text-sm font-medium">{{ $t("This device") }}</p>
              </div>
              <span
                v-if="connection?.mode === 'local'"
                class="flex shrink-0 items-center gap-1.5 text-xs text-emerald-700"
              >
                <CircleCheckIcon class="size-4" />
                {{ $t("Current") }}
              </span>
              <Button
                v-else
                variant="outline"
                size="sm"
                :disabled="loading || saving"
                @click="useLocalKernel"
              >
                {{ $t("Use") }}
              </Button>
            </div>
            <div class="flex min-h-16 items-center gap-4 border-t border-border px-4 py-3">
              <GlobeLockIcon class="size-5 shrink-0 text-muted-foreground" />
              <div class="min-w-0 flex-1">
                <p class="text-sm font-medium">HTTPS</p>
                <p
                  v-if="connection?.mode === 'remote'"
                  class="mt-0.5 truncate font-mono text-xs text-muted-foreground"
                >
                  {{ connection.url }}
                </p>
              </div>
              <span
                v-if="connection?.mode === 'remote'"
                class="flex shrink-0 items-center gap-1.5 text-xs text-emerald-700"
              >
                <CircleCheckIcon class="size-4" />
                {{ $t("Current") }}
              </span>
              <Button
                variant="outline"
                size="sm"
                :disabled="loading || saving"
                @click="openEditor('https')"
              >
                {{ connection?.mode === "remote" ? $t("Edit") : $t("Configure") }}
              </Button>
            </div>
          </div>
        </section>

        <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
      </div>
    </template>

    <template v-else>
      <div class="max-w-2xl space-y-5">
        <Button variant="ghost" size="sm" class="-ml-2" @click="closeEditor">
          <ArrowLeftIcon />
          {{ $t("Back to connections") }}
        </Button>

        <template v-if="editor === 'ssh'">
          <div v-if="sshHosts.length" class="space-y-1.5">
            <Label for="ssh-config-host">{{ $t("SSH configuration") }}</Label>
            <Select :model-value="selectedSshHost" @update:model-value="selectSshHost">
              <SelectTrigger id="ssh-config-host" class="w-full">
                <SelectValue :placeholder="$t('Select an SSH host')" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem v-for="host in sshHosts" :key="host.alias" :value="host.alias">
                  {{ host.alias }}
                  <span v-if="host.hostname" class="ml-2 text-muted-foreground">
                    {{ host.user ? `${host.user}@` : "" }}{{ host.hostname }}
                  </span>
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div class="space-y-1.5">
            <Label for="ssh-name">{{ $t("Connection name") }}</Label>
            <Input
              id="ssh-name"
              v-model="sshName"
              :placeholder="$t('Home server')"
              autocomplete="off"
            />
          </div>
          <div class="grid grid-cols-[minmax(0,1fr)_7rem] gap-3">
            <div class="space-y-1.5">
              <Label for="ssh-target">{{ $t("SSH host") }}</Label>
              <Input
                id="ssh-target"
                v-model="sshTarget"
                class="font-mono"
                placeholder="user@192.168.1.20"
                autocomplete="off"
              />
            </div>
            <div class="space-y-1.5">
              <Label for="ssh-port">{{ $t("Port") }}</Label>
              <Input
                id="ssh-port"
                v-model.number="sshPort"
                type="number"
                min="1"
                max="65535"
              />
            </div>
          </div>
        </template>

        <template v-else>
          <div class="space-y-1.5">
            <Label for="kernel-url">{{ $t("Server URL") }}</Label>
            <Input
              id="kernel-url"
              v-model="url"
              class="font-mono"
              placeholder="https://foya.example.com"
              autocomplete="url"
            />
          </div>
          <div class="space-y-1.5">
            <Label for="kernel-token">{{ $t("Access token") }}</Label>
            <Input
              id="kernel-token"
              v-model="token"
              type="password"
              autocomplete="off"
              :placeholder="
                connection?.mode === 'remote' && connection.has_token
                  ? $t('Leave blank to keep the current value')
                  : $t('Enter access token')
              "
            />
          </div>
        </template>

        <div class="flex items-center justify-end gap-2 border-t border-border pt-4">
          <span v-if="status" class="mr-auto text-sm text-muted-foreground">{{ status }}</span>
          <Button
            variant="outline"
            :disabled="loading || testing || saving || !canSubmit"
            @click="testConnection"
          >
            {{ testing ? $t("Testing") : $t("Test connection") }}
          </Button>
          <Button :disabled="loading || saving || !canSubmit" @click="save">
            {{
              saving
                ? editor === "ssh"
                  ? $t("Deploying")
                  : $t("Restarting")
                : editor === "ssh"
                  ? $t("Deploy and connect")
                  : $t("Save and restart")
            }}
          </Button>
        </div>
        <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
      </div>
    </template>
  </SettingsPage>
</template>
