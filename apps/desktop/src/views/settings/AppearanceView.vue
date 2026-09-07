<script setup lang="ts">
import {
  ExternalLinkIcon,
  GlobeIcon,
  LanguagesIcon,
  MonitorIcon,
  MoonIcon,
  SunIcon,
} from "@lucide/vue";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useTheme, type Theme } from "@/composables/useTheme";
import { useLocale, type AppLocale } from "@/i18n";
import {
  useLinkPreference,
  type LinkOpenMode,
} from "@/composables/useLinkPreference";
import SettingsPage from "@/layouts/settings/SettingsPage.vue";
import { Button } from "@/components/ui/button";
import { ButtonGroup } from "@/components/ui/button-group";

const { theme, setTheme } = useTheme();
const { linkOpenMode, setLinkOpenMode } = useLinkPreference();
const { t } = useI18n();
const { locale, setLocale } = useLocale();

const themeOptions = computed<Array<{ value: Theme; label: string; icon: typeof SunIcon }>>(() => [
  { value: "system", label: t("Follow system"), icon: MonitorIcon },
  { value: "light", label: t("Light"), icon: SunIcon },
  { value: "dark", label: t("Dark"), icon: MoonIcon },
]);
const localeOptions = computed<Array<{
  value: AppLocale;
  label: string;
  icon: typeof LanguagesIcon;
}>>(() => [
  { value: "system", label: t("Follow system"), icon: MonitorIcon },
  { value: "zh-CN", label: t("Simplified Chinese"), icon: LanguagesIcon },
  { value: "en-US", label: t("English"), icon: LanguagesIcon },
]);
const linkOpenOptions = computed<Array<{
  value: LinkOpenMode;
  label: string;
  icon: typeof GlobeIcon;
}>>(() => [
  { value: "workbar", label: t("Built-in browser"), icon: GlobeIcon },
  { value: "system", label: t("System browser"), icon: ExternalLinkIcon },
]);
</script>

<template>
  <SettingsPage
    :title="$t('Appearance')"
    :description="$t('Adjust the interface theme, language, and link behavior.')"
  >
    <div class="max-w-4xl">
      <div class="flex items-center justify-between border-b border-border py-3">
        <span class="text-sm font-medium">{{ $t("Theme") }}</span>
        <ButtonGroup :aria-label="$t('Theme')">
          <Button
            v-for="option in themeOptions"
            :key="option.value"
            variant="outline"
            size="sm"
            :class="[
              'min-w-20 shadow-none',
              theme === option.value
                ? 'bg-accent text-accent-foreground'
                : 'text-muted-foreground',
            ]"
            :aria-pressed="theme === option.value"
            @click="setTheme(option.value)"
          >
            <component :is="option.icon" />
            {{ option.label }}
          </Button>
        </ButtonGroup>
      </div>
      <div class="flex items-center justify-between gap-6 border-b border-border py-3">
        <span class="text-sm font-medium">{{ $t("Language") }}</span>
        <ButtonGroup :aria-label="$t('Language')">
          <Button
            v-for="option in localeOptions"
            :key="option.value"
            variant="outline"
            size="sm"
            :class="[
              'min-w-24 shadow-none',
              locale === option.value
                ? 'bg-accent text-accent-foreground'
                : 'text-muted-foreground',
            ]"
            :aria-pressed="locale === option.value"
            @click="setLocale(option.value)"
          >
            <component :is="option.icon" />
            {{ option.label }}
          </Button>
        </ButtonGroup>
      </div>
      <div class="flex items-center justify-between gap-6 border-b border-border py-3">
        <span class="text-sm font-medium">{{ $t("Link behavior") }}</span>
        <ButtonGroup :aria-label="$t('Link behavior')">
          <Button
            v-for="option in linkOpenOptions"
            :key="option.value"
            variant="outline"
            size="sm"
            :class="[
              'min-w-24 shadow-none',
              linkOpenMode === option.value
                ? 'bg-accent text-accent-foreground'
                : 'text-muted-foreground',
            ]"
            :aria-pressed="linkOpenMode === option.value"
            @click="setLinkOpenMode(option.value)"
          >
            <component :is="option.icon" />
            {{ option.label }}
          </Button>
        </ButtonGroup>
      </div>
    </div>
  </SettingsPage>
</template>
