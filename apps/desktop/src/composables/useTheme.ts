import { ref } from "vue";

export type Theme = "system" | "light" | "dark";

const THEME_KEY = "foya-theme-v1";
const systemTheme = window.matchMedia("(prefers-color-scheme: dark)");

function storedTheme(): Theme {
  const value = localStorage.getItem(THEME_KEY);
  return value === "light" || value === "dark" ? value : "system";
}

const theme = ref<Theme>(storedTheme());

function applyTheme(value: Theme) {
  const resolved = value === "system" ? (systemTheme.matches ? "dark" : "light") : value;
  document.documentElement.classList.toggle("dark", resolved === "dark");
  document.documentElement.style.colorScheme = resolved;
}

systemTheme.addEventListener("change", () => {
  if (theme.value === "system") applyTheme("system");
});

export function initializeTheme() {
  applyTheme(theme.value);
}

export function useTheme() {
  function setTheme(value: Theme) {
    theme.value = value;
    localStorage.setItem(THEME_KEY, value);
    applyTheme(value);
  }

  return {
    theme,
    setTheme,
  };
}
